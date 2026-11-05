# Entrypoint → Route → Node → Provider model

Date: 2026-11-05
Status: design approved in conversation; pending builder review

## Intent

The current routing model has three names for what the operator thinks of as
one decision:

```text
Alias (match glob -> logical model)
  -> Route.match.model = logical model
  -> Node.models[logical model] = provider model
  -> provider model
```

The logical model is simultaneously a match key, a node-map key and a stats
label. It is not a thing the operator configures separately, and it makes every
route change require coordinated edits to aliases and node maps.

Replace it with one straight chain:

```text
Entrypoint (client model globs) -> Route (select one Node) -> Node (one provider model) -> upstream model
```

An **Entrypoint** is the client-facing ingress. It owns a name, one or more
client model globs, and exactly one route. A **Route** is only an ordered set of
nodes plus a selection strategy. A **Node** binds exactly one provider and one
provider model. The logical model disappears completely.

## Goals

- Rename and reshape `Alias` into a named `Entrypoint`: `name`, `match` (list of
  globs), `route`.
- Remove the logical model from config, routing, admin API, web UI and stats.
- Route contains only `name`, `nodes`, `strategy`; it no longer matches anything.
- Node contains one `model` instead of a `models` map, plus the existing
  `provider`, `backup`, `weight`, `enabled`.
- Make precedence explicit: entrypoint declaration order is the matching order.
- Keep the existing x hop-group/FIFO machinery and the existing provider
  adapters. This is a routing-model refactor, not a data-plane rewrite.

## Non-goals

- Path or host based matching. Only the client model name matches. `Match.Path`
  and `Match.Host` are deleted with the rest of `Route.Match`.
- Per-entrypoint authentication, rate limits or API keys.
- Automatic migration of old `aliases` / `logical model` configs.
- Request-time recording of the entrypoint for historical stats. Stats
  attribution remains query-time, as aliases do today.
- Changing provider adapters, model catalogs, key handling, health probes,
  stats storage format or the topology API shape beyond the renamed node keys.
- Fallback from one matched route to another route. Once an entrypoint matches,
  only its route is considered; the route's own strategy/backup nodes are the
  fallback mechanism.

## Anti-goals

- Reintroducing the logical model under another name (for example a `model`
  field on Route).
- Keeping `Node.models` as an alias for a single mapping.
- Making route declaration order decide precedence. Precedence must be the
  entrypoint list order.
- Silently accepting an old config and running with no routes.

## Constraints

- The data plane already uses an x `hop.HopGroup` with one entry per route and
  a FIFO group selector. That machinery stays.
- A provider may be used by several nodes; a node may be used by several routes;
  a route may be referenced by several entrypoints.
- Existing x node names `<route>/<provider>/<node>[/<keyIndex>]` are parsed by
  the stats recorder. Keep that shape.
- The admin API already replaces the whole alias list atomically. The
  entrypoint list uses the same pattern because order is semantic.
- Stats rows already have a `client_model` column, which is enough for
  query-time entrypoint attribution.
- This is a breaking config/API change. There is no compatibility layer.

## Approaches considered

### A. Entrypoint entries in the hop group (chosen)

Compile the routes once, then build one hop entry per entrypoint, in entrypoint
order. Each entry's matcher is the union of its model globs; its hop is the
referenced route's hop. The FIFO group selector then gives entrypoint-order
precedence for free.

Trade-off: stats attribution is query-time (same as aliases today), so editing
an entrypoint's name or match changes the historical attribution of old
requests. The design accepts that.

### B. Independent dispatcher before the hop group

Resolve the entrypoint from the request model in a wrapper, record its name per
request, then execute the selected route.

Trade-off: exact historical stats, but it duplicates body reading/routing,
bypasses or wraps the x matcher/hop group, and puts custom logic in the request
path. Rejected as too invasive for this simplification.

### C. One synthesized matcher per route

Keep one hop entry per route and merge the globs of all entrypoints that point
to it.

Trade-off: fewer hop entries, but precedence becomes route declaration order
instead of entrypoint order, and entrypoints pointing at the same route become
indistinguishable. Rejected because it contradicts the named-entrypoint model.

## Configuration

### Schema

```go
type Config struct {
    Gateway     Gateway
    Admin       Admin
    Stats       Stats
    Entrypoints []Entrypoint `yaml:"entrypoints" json:"entrypoints"`
    Providers   []Provider
    Nodes       []Node
    Routes      []Route
}

type Entrypoint struct {
    Name  string   `yaml:"name" json:"name"`
    Match []string `yaml:"match" json:"match"`
    Route string   `yaml:"route" json:"route"`
}

type Node struct {
    Name     string `yaml:"name" json:"name"`
    Provider string `yaml:"provider" json:"provider"`
    Model    string `yaml:"model" json:"model"`
    Backup   bool   `yaml:"backup,omitempty" json:"backup,omitempty"`
    Weight   int    `yaml:"weight,omitempty" json:"weight,omitempty"`
    Enabled  *bool  `yaml:"enabled,omitempty" json:"enabled,omitempty"`
}

type Route struct {
    Name     string   `yaml:"name" json:"name"`
    Nodes    []string `yaml:"nodes" json:"nodes"`
    Strategy string   `yaml:"strategy,omitempty" json:"strategy,omitempty"`
}
```

`Alias`, `Match` and `Node.Models` are deleted. `Provider.Models` remains the
per-model local override map (`disabled: true`).

### Example

```yaml
entrypoints:
  - name: fast
    match:
      - "claude-haiku*"
      - "gpt-4.5*"
    route: fast
  - name: strong
    match:
      - "claude-opus*"
      - "gpt-5.5*"
    route: strong
  - name: default
    match: ["*"]
    route: fast

providers:
  - name: deepseek
    type: deepseek
    chat: https://api.deepseek.com/v1
    keys: [${DEEPSEEK_API_KEY}]

nodes:
  - name: ds-fast
    provider: deepseek
    model: deepseek-v4-flash
  - name: ds-strong
    provider: deepseek
    model: deepseek-v4-pro

routes:
  - name: fast
    nodes: [ds-fast]
    strategy: round
  - name: strong
    nodes: [ds-strong]
```

### Normalization and defaults

- `applyDefaults` trims entrypoint names/route references and match entries,
  drops empty match entries, trims node model and route node references, and
  defaults route strategy to `round` and node weight to `1`.
- Environment expansion still applies to every new string field: entrypoint
  name, match entries, route reference, node model.
- `expandEnv` no longer handles aliases or route match fields.

### Validation

All errors name the offending object.

- Entrypoint:
  - `name` required, no `/`, unique across entrypoints.
  - `match` must contain at least one non-empty glob after trimming.
  - `route` required and must name an existing route.
  - Duplicate globs inside one entrypoint are harmless; they are deduplicated
    during normalization.
- Route:
  - `name` required, no `/`, unique.
  - `nodes` non-empty, every reference exists.
  - Duplicate node references are rejected.
  - `strategy` is one of `fifo`, `round`, `random`, or empty (defaults to
    `round`).
- Node:
  - `name` required, no `/`, unique.
  - `provider` required and must exist.
  - `model` required after trimming.
  - `weight >= 0`.
- Provider validation is unchanged.
- An empty entrypoint list is valid; the gateway then routes nothing, exactly as
  an empty alias list does today.

### Matching semantics

- Matching is against the JSON request body field `model` (the x `BodyJSON`
  matcher), exactly as today.
- Each entrypoint becomes one hop entry. Its matcher is the union of its match
  globs, compiled to one anchored regex: `^(g1|g2|...)$`.
- Glob semantics are unchanged: case-insensitive, `*` is the only wildcard.
- Hop entries are emitted in `entrypoints` order. The first entrypoint with any
  matching glob wins; later entrypoints are not considered, even if they point
  at the same route.
- An unmatched request has no matching hop entry and follows the existing
  "unrouted" behavior.
- `match: ["*"]` is the explicit catch-all; there is no implicit fallback.

## Data-plane compilation

`buildHopGroup` changes shape:

1. Register rewriters once per `(route, node)`, as today.
2. Compile each referenced route's hop once, cached by route name:
   - For each node reference, look up the node and provider.
   - Skip disabled nodes and disabled providers, as today.
   - Resolve the target model directly from `node.Model`; there is no route
     model and no `"*"` fallback.
   - If the provider disables that model, log and skip the node.
   - Build the x nodes from the provider's keys and target API exactly as today.
3. Emit one hop entry per entrypoint, in order:
   - Matcher built from the entrypoint's match globs.
   - Hop is the cached hop for its route (shared pointer, no duplicate compile).
   - Group selector stays FIFO.

`compileRouteNode`/`buildNodes` lose the route argument and the model-map
lookup. `EffectiveModel` is deleted. x node names remain
`<route>/<provider>/<node>[/<keyIndex>]`, so `stats.parseNodeName` is unchanged.

The rewriter registry key remains `r/<route>/<node>`. The engine's
`ResolveModel` returns the fixed target model and protocol, and an empty third
value; the engine's `LogicalModel` result is ignored. This means a route shared
by several entrypoints still has one engine per node. That is correct because
every node now has exactly one target model.

## Admin API

### Endpoints

- `GET /api/entrypoints` → ordered `[]Entrypoint`.
- `PUT /api/entrypoints` → replace the whole ordered list; `204`. The body order
  is the precedence order. Validation failure leaves the previous config
  untouched.
- `GET/PUT /api/aliases` are removed.

Routes and nodes keep CRUD:

- `POST /api/routes`, `PUT /api/routes/{name}`, `DELETE /api/routes/{name}`.
  Bodies are `{name, nodes, strategy}`. `PUT` keeps the existing rule that the
  path name wins and a body name must match when present.
- `DELETE /api/routes/{name}` is rejected while any entrypoint references the
  route; the error lists the entrypoint names.
- `PUT /api/routes/order` is removed. Route order no longer has any routing
  meaning; only entrypoint order does. The Routes page keeps a stable list
  order but has no ↑/↓ controls.
- Node CRUD bodies are `{name, provider, model, backup, weight, enabled}`.
  Deleting a node still fails while any route references it.

`GET /api/config` returns `entrypoints` instead of `aliases`, and the new node
and route shapes. `PUT /api/config` accepts the new shape. Old fields are
unknown and are not migrated.

### Removed logic

- Alias CRUD and bulk PUT.
- Route order endpoint, client helper, tests and page controls.
- Logical-model rename/migration logic in route update/delete.
- `LogicalModels`, `HasLogicalModel`, `AliasMatchesForModel`, `ResolveAlias`,
  `ResolveAliasMatch`.
- `Match` normalization/expansion and matcher construction from route match.

Add:

- `RouteByName(name) (*Route, bool)` (or equivalent) for validation and admin.
- `ResolveEntrypoint(clientModel string) (*Entrypoint, bool)` in entrypoint
  order, using the same glob matcher as routing.
- `ResolveEntrypointName(clientModel string) string` for stats.

## Stats and topology

- `Request.LogicalModel` is removed from the Go struct, JSON views, inserts,
  selects and scans.
- The `logical_model` SQLite column is no longer written or read. New databases
  do not create it; existing databases keep the inert column. No destructive
  migration is required.
- `stats.Fact.LogicalModel` and the recorder assignment are removed.
- `AliasResolver` becomes `EntrypointResolver`, returning the entrypoint name.
  `GraphWithAliases`/`GraphWithFilter` are renamed accordingly.
- Graph nodes use `entrypoint:<name>` instead of `alias:<glob>`. The edge
  targets the **request row's recorded route**:
  `entrypoint:<name>->route:<route>`, or `entrypoint:<name>->unrouted` when the
  row has no route. It does not use the entrypoint's current configured route,
  so old rows keep the route that actually served them; only the entrypoint
  name is resolved from the current config.
- Attribution is computed at query time by resolving each row's
  `client_model` through the current entrypoint list. Renaming an entrypoint,
  editing its match list, or reordering entrypoints changes the attribution of
  historical rows. This is accepted and matches today's alias behavior.
- `TopologyIncludes` is otherwise unchanged. Requests with no matched
  entrypoint still land in `unrouted`.
- `GET /api/stats/graph` keeps its payload shape; only the node key prefix and
  labels change.

## Web UI

- Navigation: `Aliases` becomes `Entrypoints` (Chinese: 入口).
- Entrypoints page:
  - Ordered list; ↑/↓ reorders and saves the whole list.
  - Each row shows the entrypoint name, its match globs and its route.
  - Add/edit drawer: name, match list (one glob per line or tag input), route
    select. At least one glob is required.
  - Delete removes the row from the ordered list.
- Route form: name, strategy and node multi-select only. Remove match model,
  path and host fields and all "logical model" explanation text.
- Routes page: remove the ↑/↓ order controls and the "order = FIFO priority"
  copy. Route creation no longer reorders the list; new routes append.
- Node form: provider select, one model select/input, backup, weight, enabled.
  Remove the mapping table and `routeLogicalModels`.
- Topology:
  - `AliasNode` becomes `EntrypointNode`, keyed by entrypoint name.
  - Label is the entrypoint name; sub-label shows its match globs and route.
  - Edge goes to the entrypoint's route; a missing route falls back to
    `unrouted` defensively.
  - On the editable canvas, dragging an entrypoint onto a route replaces that
    entrypoint's `route` through the bulk entrypoint PUT. Dragging a route onto
    a node still edits the route's node list. The edge inspector for
    entrypoint→route is read-only text pointing to the Entrypoints panel,
    mirroring today's alias edge inspector.
  - Route label becomes the route name; route sub-label keeps node count and
    strategy. Node sub-label shows provider and its single model.
- Requests page: remove the `client → logical` line. Client model and target
  model remain. No entrypoint column is added.
- Types: `Alias` → `Entrypoint` (`name`, `match: string[]`, `route`), `Route`
  loses `match`, `Node` uses `model: string`, `ConfigResponse` uses
  `entrypoints`.
- Presets: delete `routeLogicalModels`; model pickers use the provider catalog
  directly.

## Compatibility and migration

- Config files are loaded with `KnownFields(true)`. An old file fails on the
  first unknown field (`aliases`, `match`, or `models`). There is no auto
  migration.
- Admin JSON decoding stays permissive for unknown fields, but the UI and docs
  only emit the new shape.
- Existing SQLite stats databases continue to open; the unused
  `logical_model` column is ignored. Historical rows can still be attributed to
  an entrypoint through `client_model`.
- `example.yaml` and any docs snippets are rewritten to the new model.

## Testing

- Config:
  - Load/validate new schema; env expansion; trims/defaults.
  - Entrypoint validation: missing name/match/route, unknown route, duplicate
    name, empty match entries.
  - Node validation: missing model, unknown provider, duplicate node, bad
    weight, duplicate route refs.
  - `ResolveEntrypoint`: order, multiple globs, case-insensitivity, catch-all,
    no match.
  - Old `aliases` / route `match` / node `models` fail to load.
- Gateway:
  - Entrypoint order determines which route wins when globs overlap.
  - Several entrypoints sharing a route reuse the compiled hop.
  - Disabled provider/node and provider-disabled model skip the node.
  - Node model is used verbatim; no logical mapping or `"*"` fallback remains.
  - Existing API/protocol conversion and rewriter tests still pass.
- Admin:
  - Entrypoint GET/PUT round-trip and order preservation.
  - Route create/update/delete with no match field; delete blocked by an
    entrypoint reference and allowed otherwise.
  - Node CRUD with a single model; delete blocked by route reference.
  - Config response uses `entrypoints`.
- Stats:
  - Request insert/select without `logical_model`.
  - Graph keys `entrypoint:<name>`; edge to route; order-based resolution.
  - Existing time-window/topology filters still pass.
- Web:
  - `buildGraph` entrypoint nodes/edges and defensive unrouted edge.
  - Entrypoint selection from graph node id.
  - Presets/types compile and tests pass; `npm run build` and TypeScript checks.
- End to end:
  - Run the gateway with the rewritten `example.yaml`; send one request per
    entrypoint and assert each reaches the expected provider model.
  - Send an unmatched model and assert the existing unrouted failure.
  - Reload config through the admin API and verify entrypoint order changes the
    selected route.

## Impacted files

Approximate, to guide the implementation plan:

- `internal/config/config.go`, `internal/config/config_test.go`
- `internal/gateway/build.go`, `upstream.go`, gateway/build tests
- `internal/admin/server.go`, `handlers.go`, `handlers_stats.go`, admin tests
- `internal/stats/facts.go`, `recorder.go`, `graph.go`, `store.go`, stats tests
- `web/src/api/types.ts`, `client.ts`
- `web/src/pages/Aliases.tsx` (renamed), `Edit.tsx`, `Routes.tsx`,
  `presets.ts`, `Requests.tsx`
- `web/src/graph/build.ts`, `layout.ts`, `nodes.tsx`
- `web/src/pages/selection.ts` and their tests
- `example.yaml`, `DOCKER.md` if it contains config examples

## Decisions left to the builder

- Exact Ant Design control for the match list (tags input vs one-per-line
  textarea) inside the existing drawer patterns.
- Whether the Requests detail resolves and displays the entrypoint client-side.
  It is not required and must not add a stored column.
- Test file names and whether to rename existing alias-named test files or
  replace them in place.

## Acceptance criteria

- The only routing chain in config and code is
  `Entrypoint -> Route -> Node -> Provider model`; no logical-model concept
  remains in config, admin API, web or stats code.
- Entrypoint order is the single precedence rule.
- `node.Model` is the only target-model source; `Node.Models` and `"*"` fallback
  are gone.
- Old config fails loudly instead of running partially.
- All Go tests, web tests, TypeScript checks and the web build pass.
- `example.yaml` works end to end with one request per entrypoint.
