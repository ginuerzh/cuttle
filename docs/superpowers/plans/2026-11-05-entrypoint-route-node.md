# Entrypoint → Route → Node → Provider Model Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace aliases and the logical-model layer with named entrypoints, so every request follows exactly `Entrypoint -> Route -> Node -> provider model`.

**Architecture:** Config gains `entrypoints` (name, ordered model globs, route) and loses `aliases`, `Route.Match`, and `Node.Models`. The data plane compiles one hop entry per entrypoint in declaration order, each pointing at a cached route hop; node model resolution becomes `node.model`. The admin API, stats graph and web UI are renamed and reshaped to match.

**Tech Stack:** Go 1.26, x hop group/FIFO selector, SQLite stats store, React + Ant Design + React Flow + Vitest.

**Spec:** `docs/superpowers/specs/2026-11-05-entrypoint-route-node-design.md`

## Global Constraints

- The only routing chain is `Entrypoint -> Route -> Node -> provider model`; no logical-model concept may remain in config, admin API, web or stats code.
- Entrypoint declaration order is the only precedence rule. Route order no longer has routing meaning.
- An entrypoint has a `name`, a non-empty `match` list of client model globs, and exactly one `route`.
- A node has exactly one `model`; `Node.Models`, `EffectiveModel`, `"*"` fallback and client-model pass-through no longer exist.
- Only the client model name matches. There is no path or host matching.
- Old config (`aliases`, route `match`, node `models`) must fail loudly at load; no migration layer.
- Stats entrypoint attribution stays query-time. Graph edges use the request row's recorded route, not the entrypoint's current route.
- x node names stay `<route>/<provider>/<node>[/<keyIndex>]`.
- Work in `/root/code/go-gost/llm-gw`; run `GOWORK=off` for Go commands if the workspace interferes.
- Commit after each task, including the filled Implementation record in the same commit. Never push.

## Review Focus

- A model matching several entrypoints must go to the first entrypoint in declaration order, even when a later route is declared earlier. Pinned by `TestEntrypointOrderWins` in Task 1.
- An old config using `aliases` / `match` / `models` must fail at load rather than start with no routes. Pinned by `TestLoadRejectsLegacyAliasConfig` in Task 1.
- A route whose nodes are all disabled/provider-disabled/model-disabled must fail the request, never silently pass the client model through. Pinned by `TestRouteWithOnlyDisabledNodesIsUnrouted` in Task 1.
- Deleting a route still referenced by an entrypoint must be rejected and leave config untouched. Pinned by `TestRouteDeleteRejectedWhileEntrypointReferences` in Task 1.
- Historical stats rows keep the route that served them even if the entrypoint now points elsewhere; the graph must not use the current configured route. Pinned by `TestGraphEntrypointsUseRowRoute` in Task 1.

---

### Task 1: Backend config, routing, admin, stats

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`
- Modify: `internal/gateway/build.go`
- Modify: `internal/gateway/upstream.go`
- Modify: `internal/gateway/gateway_test.go`
- Rename/replace: `internal/gateway/alias_route_test.go` → `internal/gateway/entrypoint_route_test.go`
- Modify: `internal/gateway/protocol_test.go`, `routing_extra_test.go`, `provider_capabilities_test.go`, `model_disabled_test.go`, `stats_test.go`, `reload_test.go`, `admin_e2e_test.go`
- Modify: `internal/admin/server.go`, `handlers.go`, `handlers_stats.go`
- Delete/replace: `internal/admin/logical_model_test.go` → `internal/admin/entrypoint_test.go`
- Modify: `internal/admin/server_test.go`, `p6a_test.go`
- Modify: `internal/stats/facts.go`, `recorder.go`, `graph.go`, `store.go`
- Modify: `internal/stats/graph_test.go`, `store_test.go`
- Modify: `example.yaml`

**Interfaces:**
- Produces:
  ```go
  type Entrypoint struct {
      Name  string   `yaml:"name" json:"name"`
      Match []string `yaml:"match" json:"match"`
      Route string   `yaml:"route" json:"route"`
  }

  type Node struct {
      Name     string
      Provider string
      Model    string
      Backup   bool
      Weight   int
      Enabled  *bool
  }

  type Route struct {
      Name     string
      Nodes    []string
      Strategy string
  }

  func (c *Config) ResolveEntrypoint(clientModel string) (*Entrypoint, bool)
  func (c *Config) ResolveEntrypointName(clientModel string) string
  ```
- Produces admin endpoints: `GET /api/entrypoints`, `PUT /api/entrypoints`.
- Produces stats API: `type EntrypointResolver func(string) string` and
  `func (s *Store) GraphWithEntrypoints(ctx context.Context, from, to time.Time, resolve EntrypointResolver) (GraphMetrics, error)`.
- Removes: `Alias`, `Match`, `Node.Models`, `EffectiveModel`, `LogicalModels`,
  `HasLogicalModel`, `AliasMatchesForModel`, `ResolveAlias`, `ResolveAliasMatch`,
  `GraphWithAliases`, `AliasResolver`, `Request.LogicalModel`, route order API.

- [x] **Step 1: Write the failing config tests**

Update every existing config fixture to add `model: <id>` to each node and an
`entrypoints` list. Add these tests:

```go
func TestResolveEntrypointOrder(t *testing.T) {
    c := &Config{Entrypoints: []Entrypoint{
        {Name: "specific", Match: []string{"gpt-5*"}, Route: "a"},
        {Name: "fallback", Match: []string{"*"}, Route: "b"},
    }}
    // ResolveEntrypoint("GPT-5.5") -> specific/a
    // ResolveEntrypoint("other")   -> fallback/b
}

func TestResolveEntrypointMultipleGlobsAndCase(t *testing.T) {
    c := &Config{Entrypoints: []Entrypoint{
        {Name: "claude", Match: []string{"claude-*", "claude*"}, Route: "r"},
    }}
    // ResolveEntrypoint("CLAUDE-OPUS-4-5") -> claude/r
}

func TestValidateEntrypoints(t *testing.T) {
    // table: empty name; name with "/"; duplicate name; empty match list;
    // empty-only match entries; unknown route. Each must error.
}

func TestValidateNodeRequiresModel(t *testing.T) {
    // Node{Name:"n", Provider:"p"} with a valid provider -> error mentioning model.
}

func TestValidateRejectsDuplicateRouteNode(t *testing.T) {
    // Route{Nodes:["n","n"]} -> error.
}

func TestLoadRejectsLegacyAliasConfig(t *testing.T) {
    // YAML containing `aliases:` must fail and the error must contain "aliases".
}

func TestNodeModelExpandsEnv(t *testing.T) {
    t.Setenv("MODEL", "deepseek-v4-flash")
    // node.model: ${MODEL} loads as deepseek-v4-flash.
}
```

- [x] **Step 2: Run the config tests to verify they fail**

Run: `GOWORK=off go test ./internal/config -run 'TestResolveEntrypoint|TestValidateEntrypoints|TestValidateNodeRequiresModel|TestValidateRejectsDuplicateRouteNode|TestLoadRejectsLegacyAliasConfig|TestNodeModelExpandsEnv' -count=1`

Expected: compile errors for missing types/functions, or failures because the old schema is still present.

- [x] **Step 3: Implement the config model**

In `internal/config/config.go`:

- Replace `Aliases []Alias` with `Entrypoints []Entrypoint`.
- Delete `Alias`, `Match`, `Node.Models`, `LogicalModels`, `HasLogicalModel`,
  `AliasMatchesForModel`, `ResolveAlias`, `ResolveAliasMatch`, `EffectiveModel`.
- Remove route `Match` expansion/defaulting/validation.
- `expandEnv`: process entrypoint name, each match entry, route reference, node model.
- `applyDefaults`: for each entrypoint trim name/route, trim and dedupe match
  entries, drop empties; for each route trim name/node refs and default strategy
  `round`; for each node trim model and default weight `1`.
- `Validate`, in this order:
  1. Providers (unchanged).
  2. Nodes: name required/no `/`/unique; provider exists; `Model` non-empty
     after trim; weight >= 0.
  3. Routes: name required/no `/`/unique; `Nodes` non-empty and all exist;
     duplicate refs rejected; strategy `fifo|round|random|""`.
  4. Entrypoints: name required/no `/`/unique; `Match` has at least one
     non-empty glob after trim; `Route` exists.
- `ResolveEntrypoint` iterates `c.Entrypoints` in order and returns the first
  entrypoint with any `globMatch(match, clientModel)` hit. Return a pointer to
  the slice element.
- `ResolveEntrypointName` returns its `Name`, or `""`.

- [x] **Step 4: Run the config package**

Run: `GOWORK=off go test ./internal/config -count=1`

Expected: PASS.

- [x] **Step 5: Rewrite gateway fixtures and add the failing routing tests**

Update `testConfig`, `matrixConfig`, and every gateway fixture to the new
schema. `testConfig` becomes:

```go
func testConfig(upstreamURL string) *gwconfig.Config {
    return &gwconfig.Config{
        Gateway: gwconfig.Gateway{Listen: "127.0.0.1:0"},
        Entrypoints: []gwconfig.Entrypoint{{
            Name: "default", Match: []string{"*"}, Route: "default",
        }},
        Providers: []gwconfig.Provider{{
            Name: "up", Type: "generic", Chat: upstreamURL, Keys: []string{"sk-test"},
        }},
        Nodes: []gwconfig.Node{{Name: "up", Provider: "up", Model: "deepseek-chat"}},
        Routes: []gwconfig.Route{{Name: "default", Nodes: []string{"up"}}},
    }
}
```

`matrixConfig` should keep one catch-all route with a single node model
(`deepseek-v4-flash`) and entrypoints `strong` (`claude-opus*`, `gpt-5.5*`) and
`fast` (`gpt-4.5*`, `*`), both pointing at that route. Delete the
"provider without models keeps the client model" subtest; replace it with a test
that an empty node model makes `New` fail.

Add `internal/gateway/entrypoint_route_test.go`:

```go
func TestEntrypointOrderWins(t *testing.T) {
    // Routes declared catch-all first, specific second.
    // Entrypoints: specific (gpt-5.5 -> strong) first, catch-all (* -> fast).
    // Request gpt-5.5 must hit strong; an unknown model must hit fast.
}

func TestEntrypointMultipleGlobs(t *testing.T) {
    // One entrypoint matches claude-* and gpt-5*; both route to the same node.
    // Both requests must reach that node and carry its model.
}

func TestUnmatchedModelIsUnrouted(t *testing.T) {
    // No catch-all entrypoint; request an unknown model.
    // Status must be >= 500 and neither upstream may be hit.
}

func TestEmptyEntrypointsServesNothing(t *testing.T) {
    // Routes and nodes exist, Entrypoints is nil.
    // New must succeed; every request is >= 500.
}

func TestNodeModelRequired(t *testing.T) {
    // Node.Model is empty; New must return an error mentioning the node/model.
}

func TestRouteWithOnlyDisabledNodesIsUnrouted(t *testing.T) {
    // One route, node provider enabled=false (or node enabled=false, or the
    // provider disables the node model). Request must be >= 500; no fallback
    // model is sent upstream.
}
```

Rewrite `TestRoutePrioritySpecificBeforeCatchAll` as
`TestEntrypointOrderBeatsRouteOrder` using the scenario above. Remove
`TestRouteMatchesAliasUnion` and `TestRouteFallsBackToLiteralGlobWithoutAlias`.
Update the remaining fixtures in `protocol_test.go`,
`provider_capabilities_test.go`, `routing_extra_test.go`, `model_disabled_test.go`
and `stats_test.go` mechanically: `Aliases` → `Entrypoints`, route `Match` →
entrypoint `Match`, node `Models` → one `Model`.

- [x] **Step 6: Run the gateway tests to verify they fail**

Run: `GOWORK=off go test ./internal/gateway -count=1`

Expected: failures/compile errors for the old routing fields.

- [x] **Step 7: Implement the data plane**

In `internal/gateway/upstream.go`:

- `compileRouteNode(p gwconfig.Provider, n gwconfig.Node)` uses
  `strings.TrimSpace(n.Model)`; return an error if it is empty.
- Delete route-model logic and `EffectiveModel` use.

In `internal/gateway/build.go`:

- `buildHopGroup` compiles each referenced route once into a
  `map[string]hop.Hop`, then emits one `xhop.NewHopEntry(routeHop, matcher, nil)`
  per entrypoint, in entrypoint order. Keep the FIFO group selector.
- `buildEntrypointMatcher(match []string) (corerouting.Matcher, error)`: build
  `^(glob1|glob2|...)$` with `globToRegexp` inner expressions; error on no
  non-empty globs.
- Delete `buildMatcher` and the alias-union logic.
- `buildRouteHop` passes no route model to `compileRouteNode`.
- `registerRewriters` and `newNodeEngine` no longer read a route model. The
  engine's `ResolveModel` returns `targetModel, engineProto, ""`.

- [x] **Step 8: Run the gateway package**

Run: `GOWORK=off go test ./internal/gateway -count=1`

Expected: PASS.

- [x] **Step 9: Write the failing admin tests**

Delete `internal/admin/logical_model_test.go`; add `internal/admin/entrypoint_test.go`:

```go
func TestEntrypointListAndPut(t *testing.T) {
    // GET /api/entrypoints -> []
    // PUT [{"name":"fast","match":["gpt*"],"route":"fast"},
    //      {"name":"default","match":["*"],"route":"fast"}] -> 204
    // GET preserves order; GET /api/config has the same entrypoints.
}

func TestEntrypointPutRejectsUnknownRoute(t *testing.T) {
    // PUT an entrypoint whose route does not exist -> 400; config unchanged.
}

func TestRouteDeleteRejectedWhileEntrypointReferences(t *testing.T) {
    // Route "fast" exists; entrypoint "fast" -> route "fast".
    // DELETE /api/routes/fast -> 400 containing "fast"; route still present.
}

func TestRouteDeleteAfterEntrypointRemoved(t *testing.T) {
    // PUT [] then DELETE route -> 204.
}

func TestRouteCreateHasNoMatchField(t *testing.T) {
    // POST {"name":"r","nodes":["n"],"strategy":"round"} -> 201.
}

func TestRouteOrderEndpointRemoved(t *testing.T) {
    // PUT /api/routes/order -> 404 or 405.
}
```

Update `TestGraphAliasAPI` to `TestGraphEntrypointAPI` using a config entrypoint
and expect `entrypoint:fast -> route:r`. Remove `TestRouteOrder`. Update
`TestEmptyListsReturnArrays` to require `entrypoints`.

Update config PUT / route / node fixtures throughout `server_test.go` and
`p6a_test.go` to the new schema.

- [x] **Step 10: Run the admin tests to verify they fail**

Run: `GOWORK=off go test ./internal/admin -run 'TestEntrypoint|TestRouteDelete|TestRouteCreate|TestRouteOrder|TestGraphEntrypoint' -count=1`

Expected: compile errors or 404/400 mismatches.

- [x] **Step 11: Implement the admin API**

In `internal/admin/handlers.go`:

- `configView.Entrypoints []config.Entrypoint` with JSON `entrypoints`; drop aliases.
- Replace `handleGetAliases`/`handlePutAliases` with
  `handleGetEntrypoints`/`handlePutEntrypoints`. PUT replaces the whole ordered
  list; `mutate` supplies validation/reload/rollback.
- Route create/update: remove match trimming and `migrateRouteNodeModels`.
  `handleUpdateRoute` only replaces the route body (path name wins).
- Route delete: after removing, reject if any `c.Entrypoints[i].Route` equals
  the deleted name; the error lists the entrypoint names.
- Delete `handleRouteOrder` and its route registration.
- `handleGetConfig` returns entrypoints.

In `internal/admin/handlers_stats.go`, snapshot `Entrypoints` (not aliases),
call `cfg.ResolveEntrypointName` and `GraphWithFilter`.

In `internal/admin/server.go`, register `GET/PUT /api/entrypoints`; remove
`/api/aliases` and `PUT /api/routes/order`.

- [x] **Step 12: Run the admin package**

Run: `GOWORK=off go test ./internal/admin -count=1`

Expected: PASS.

- [x] **Step 13: Write the failing stats tests**

In `internal/stats/store_test.go` and `graph_test.go`:

- Remove `LogicalModel` assertions/fields; add a round-trip test that inserts a
  request with `ClientModel` and reads it back.
- Add `TestGraphEntrypointsUseRowRoute`: insert two rows with the same client
  model but different recorded routes; `ResolveEntrypoint` maps the model to
  `fast`. The graph must contain `entrypoint:fast->route:r1` and
  `entrypoint:fast->route:r2`, never the entrypoint's current configured route
  when it differs, and no `alias:` keys.
- Update every `GraphWithAliases`/`AliasResolver` reference.

- [x] **Step 14: Run the stats tests to verify they fail**

Run: `GOWORK=off go test ./internal/stats -count=1`

Expected: compile errors for removed/renamed symbols.

- [x] **Step 15: Implement stats changes**

- `facts.go`: remove `LogicalModel` and its merge.
- `recorder.go`: stop assigning `Request.LogicalModel`.
- `store.go`: remove `LogicalModel` from `Request`, INSERT, both SELECTs, scan
  args, and the `ensureColumns` map. Leave old databases untouched; their
  `logical_model` column is simply never read or written.
- `graph.go`: rename `AliasResolver` → `EntrypointResolver`,
  `GraphWithAliases` → `GraphWithEntrypoints`, keep `GraphWithFilter` with the
  new resolver type. Emit `entrypoint:<name>` nodes and
  `entrypoint:<name>->route:<row route>` (or `->unrouted`).

- [x] **Step 16: Run the stats package**

Run: `GOWORK=off go test ./internal/stats -count=1`

Expected: PASS.

- [x] **Step 17: Update `example.yaml`**

Rewrite the example to the new schema: entrypoints fast/strong/default, nodes
with one `model` each, routes with `nodes`/`strategy` only, no aliases, no
route `match`, no node `models` mapping. Keep the provider section.

- [x] **Step 18: Run the full Go gate**

```bash
gofmt -w $(find internal -name '*.go')
GOWORK=off go build ./...
GOWORK=off go vet ./...
GOWORK=off go test ./... -count=1
```

Expected: all PASS.

- [x] **Step 19: Fill the Implementation record and commit**

Fill the record below, then:

```bash
git add internal example.yaml docs/superpowers/specs/2026-11-05-entrypoint-route-node-design.md docs/superpowers/plans/2026-11-05-entrypoint-route-node.md
git commit -m "refactor(backend)!: route clients through named entrypoints"
```

**Implementation record**
- Date/status: 2026-11-05 / complete
- Repo/commit: llm-gw / 641cafd3 (backend code and docs); this record commit is the follow-up
- Files changed: `internal/config/config.go`, `internal/config/config_test.go`, `internal/gateway/build.go`, `internal/gateway/upstream.go`, `internal/gateway/gateway_test.go`, `internal/gateway/entrypoint_route_test.go` (replaced `alias_route_test.go`), `internal/gateway/protocol_test.go`, `internal/gateway/routing_extra_test.go`, `internal/gateway/provider_capabilities_test.go`, `internal/gateway/model_disabled_test.go`, `internal/gateway/stats_test.go`, `internal/gateway/reload_test.go`, `internal/gateway/admin_e2e_test.go`, `internal/admin/server.go`, `internal/admin/handlers.go`, `internal/admin/handlers_stats.go`, `internal/admin/entrypoint_test.go` (replaced `logical_model_test.go`), `internal/admin/server_test.go`, `internal/admin/p6a_test.go`, `internal/stats/facts.go`, `internal/stats/recorder.go`, `internal/stats/graph.go`, `internal/stats/store.go`, `internal/stats/graph_test.go`, `example.yaml`
- Tests: `GOWORK=off go build ./...` PASS; `GOWORK=off go vet ./...` PASS; `GOWORK=off go test ./... -count=1` PASS (root, admin, config, gateway, provider, stats). Targeted tests cover entrypoint order, multiple globs, empty entrypoints, missing node model, all-disabled nodes, legacy alias rejection, admin entrypoint PUT/delete guard, and stats row-route attribution.
- Review findings/fixes: one node can no longer serve two models, so the provider-capability fixture was split into one node per route. Requests without a model body no longer match any entrypoint; the pass-through and reload tests now send a model JSON body. Route-order removal is asserted by a 400/404 plus unchanged order because `/api/routes/{name}` captures the literal `order` path. `model_disabled` fixtures were updated when routes are renamed, because entrypoints reference routes by name. `logical_model` is no longer written or read; existing SQLite databases keep the inert column.
- Deferred/limitations: old YAML with `aliases`/`match`/`models` fails at load by design; no migration. `PUT /api/routes/order` can still be interpreted as an update of a route literally named `order`; it no longer reorders anything. Web/API consumers are updated in Task 2. The end-to-end example load test is Task 3.

---

### Task 2: Web entrypoints, forms, and topology

**Files:**
- Modify: `web/src/api/types.ts`, `web/src/api/client.ts`
- Modify: `web/src/App.tsx`
- Rename: `web/src/pages/Aliases.tsx` → `web/src/pages/Entrypoints.tsx`
- Modify: `web/src/pages/Edit.tsx`, `Routes.tsx`, `Nodes.tsx`, `Requests.tsx`, `presets.ts`
- Modify: `web/src/graph/build.ts`, `layout.ts`, `nodes.tsx`
- Modify: `web/src/pages/selection.ts`
- Modify: `web/src/locales/en-US.ts`
- Modify: `web/src/graph/build.test.ts`, `web/src/pages/selection.test.ts`, `web/src/pages/presets.test.ts`
- Modify: `web/src/pages/Topology.tsx` only if the node type map requires it

**Interfaces:**
- Produces:
  ```ts
  export interface Entrypoint { name: string; match: string[]; route: string; }
  export interface Node { name: string; provider: string; model: string; backup?: boolean; weight?: number; enabled?: boolean; }
  export interface Route { name: string; nodes: string[]; strategy?: string; }
  export interface ConfigResponse { entrypoints: Entrypoint[]; /* ... */ }
  ```
- Produces client calls:
  ```ts
  export function putEntrypoints(entrypoints: Entrypoint[]): Promise<void>
  ```
- Produces components: `EntrypointsPage`, `EntrypointForm`, `AddEntrypointModal`.
- Produces graph helpers: `entrypointFromNodeId(entrypoints, id)` and node kind `entrypoint`.

- [x] **Step 1: Write the failing web tests**

Update `web/src/graph/build.test.ts` to a config with `entrypoints` and expect:

```ts
expect(nodes.some((n) => n.id === 'entrypoint:fast')).toBe(true);
expect(edges.some((e) => e.id === 'entrypoint:fast->route:fast')).toBe(true);
expect(edges.some((e) => e.id.startsWith('alias:'))).toBe(false);
```

Update `selection.test.ts` to `entrypointFromNodeId`; unknown and non-entrypoint
ids return null. In `presets.test.ts`, delete the `routeLogicalModels` describe
block. Keep the `unknownModels` / `disabledTargets` signatures and their tests;
they are called with a single-element rows array for a node's one model.

- [x] **Step 2: Run the web tests to verify they fail**

Run: `cd web && npm test`

Expected: failures for missing `entrypoints` / `entrypointFromNodeId`.

- [x] **Step 3: Update types and client**

In `types.ts`: add `Entrypoint`, change `Node.models` to `model`, remove
`Route.match`, replace `aliases` with `entrypoints` in `ConfigResponse`, remove
`logicalModel` from the request row type. In `client.ts`: normalize
`entrypoints: cfg.entrypoints ?? []`; replace `putAliases` with
`putEntrypoints` calling `PUT /api/entrypoints`; remove `reorderRoutes`.

- [x] **Step 4: Implement the Entrypoints page and forms**

Rename `Aliases.tsx` to `Entrypoints.tsx`:

- `EntrypointsPage` shows `name`, `match.join(', ')`, and `route`.
- ↑/↓ calls `putEntrypoints` with the reordered list.
- Add/edit drawer uses `EntrypointForm`; add uses `AddEntrypointModal`.
- Delete filters the row and calls `putEntrypoints`.

In `Edit.tsx`:

- Replace `AliasForm` with `EntrypointForm`: name `Input`, match `Select
  mode="tags"` (values are the glob array), route `Select` over `config.routes`.
- Replace `AddAliasModal` with `AddEntrypointModal` using the same fields; append
  to the list.
- Route form: remove the model field and all logical-model copy; keep name,
  strategy, nodes. In `AddRouteModal`, create the route directly; remove the
  `reorderRoutes`/catch-all insertion logic.
- Node form: replace `ModelMapField` with one required `model` field backed by
  the existing model picker; keep provider, backup, weight. Change
  `DerivedTargetAPIHint` and `ModelCatalogHint` to take `model: string` and pass
  `model ? [{ value: model }] : []` to the existing `unknownModels` /
  `disabledTargets` helpers. Remove `routeLogicalModels` from `presets.ts` and
  its uses.
- Delete `mapToRows`/`rowsToMap` if no longer used.

- [x] **Step 5: Update navigation and Routes page**

- `App.tsx`: import `EntrypointsPage`, rename the nav key/label
  `aliases/别名` to `entrypoints/入口`, and route the page.
- `Routes.tsx`: remove ↑/↓ controls, `reorderRoutes`, and the FIFO-order copy;
  subtitles show `strategy · nodes`.
- `Nodes.tsx`: replace `n.models` payload/subtitle with `model: n.model`; show
  `${provider} · ${model}`.
- `locales/en-US.ts`: translate `入口` as `Entrypoints`; remove or rewrite the
  alias and logical-model strings so no stale translation is reachable.

- [x] **Step 6: Update the topology graph**

- `graph/build.ts`: `NodeKind` uses `entrypoint`; `GraphNodeData.entrypoint`;
  accept `entrypoints`; draw one node per entrypoint keyed
  `entrypoint:<name>` with label `name` and sub `matches → route`; edge to
  `route:<route>` (or `unrouted` if missing). Route label is `route.name`; node
  sub-label is `provider · model`.
- `graph/layout.ts`: rename the `alias` size entry to `entrypoint`.
- `graph/nodes.tsx`: rename `AliasNode` to `EntrypointNode` and register it.
- `pages/selection.ts`: replace `aliasFromNodeId` with
  `entrypointFromNodeId(entrypoints, id)`, keyed by entrypoint name.
- `Edit.tsx`: update selection and `connect`:
  - entrypoint → route replaces that entrypoint's `route` via `putEntrypoints`;
  - route → node unchanged;
  - `EdgeForm` for `entrypoint|route` is read-only text pointing to the
    Entrypoints panel.
- `App.tsx` `NodePanel`: treat `entrypoint` like other kinds for request/error
  metrics.

- [x] **Step 7: Update the Requests page**

Remove the `client → logical` Descriptions item; keep client model and target
model. Remove any `logicalModel` type references.

- [x] **Step 8: Run the web gate**

```bash
cd web
npm test
npm run build
```

Expected: all tests PASS; TypeScript and Vite build PASS.

- [x] **Step 9: Fill the Implementation record and commit**

Fill the record below, then:

```bash
git add web
git commit -m "refactor(web)!: author named entrypoints and the new topology"
```

**Implementation record**
- Date/status: 2026-11-05 / complete
- Repo/commit: llm-gw / c101eb21 (web code and docs); this record commit is the follow-up
- Files changed: `web/src/api/types.ts`, `web/src/api/client.ts`, `web/src/App.tsx`, `web/src/pages/Entrypoints.tsx` (replaced `Aliases.tsx`), `web/src/pages/Edit.tsx`, `Routes.tsx`, `Nodes.tsx`, `Requests.tsx`, `presets.ts`, `web/src/graph/build.ts`, `layout.ts`, `nodes.tsx`, `web/src/pages/selection.ts`, `web/src/locales/en-US.ts`, `web/src/styles.css`, and their tests. Removed the now-unused `web/src/components/MapFields.tsx` and its test.
- Tests: `npx tsc --noEmit` PASS; `npm test` PASS (9 files, 75 tests); `npm run build` PASS (Vite chunk-size warning unchanged); `GOWORK=off go test ./... -count=1` PASS after the web build.
- Review findings/fixes: route order is no longer editable (no ↑/↓, no `reorderRoutes`); new routes append. The node form now has one required model field with the existing catalog picker and unknown/disabled warnings. The entrypoint form uses a tags control for `match`, a route select, and the list order is edited via ↑/↓/bulk PUT. Topology draws `entrypoint:<name>` nodes and dragging an entrypoint onto a route replaces its route. Requests no longer show a logical model. Stale alias/logical translations were removed.
- Deferred/limitations: `PUT /api/routes/order` is still interpreted as an update of a route literally named `order` (backend ruling from Task 1); no UI path uses it. Requests do not display the resolved entrypoint. The Vite large-chunk warning is pre-existing.

---

### Task 3: End-to-end verification and documentation

**Files:**
- Create: `internal/gateway/entrypoint_e2e_test.go`
- Modify: `internal/config/config_test.go` (example config load test)
- Modify: `DOCKER.md` only if it contains old config snippets
- Modify: plan record only otherwise

**Interfaces:**
- Consumes Task 1's `config.Load`, `gateway.New`, `gateway.Start`.
- Consumes Task 2's web build; no new production interfaces.

- [x] **Step 1: Add the end-to-end test**

In `internal/gateway/entrypoint_e2e_test.go` (package `gateway_test`):

```go
func TestEntrypointConfigEndToEnd(t *testing.T) {
    // 1. httptest upstream captures path and body model.
    // 2. Write a YAML file: one generic provider pointing at the upstream,
    //    nodes fast (model fast-model) and strong (model strong-model),
    //    routes fast/strong, entrypoints:
    //      fast:    ["gpt-4.5*"] -> fast
    //      strong:  ["claude-opus*", "gpt-5.5*"] -> strong
    //      default: ["*"] -> fast
    // 3. config.Load the file, gateway.New/Start.
    // 4. POST each model and assert the upstream body model:
    //    gpt-4.5 -> fast-model, claude-opus-4-5 -> strong-model,
    //    gpt-5.5 -> strong-model, unknown -> fast-model.
    // 5. Rewrite the YAML with strong after default, config.Load it, and call
    //    gw.Reload(reloaded); assert claude-opus now goes to fast-model
    //    (entrypoint order is the only precedence rule).
}
```

Also add `TestExampleConfigLoads`:

```go
func TestExampleConfigLoads(t *testing.T) {
    t.Setenv("DEEPSEEK_API_KEY", "test")
    // Load ../../example.yaml; assert at least one entrypoint and every
    // route reference resolves.
}
```

- [x] **Step 2: Run the end-to-end tests to verify they pass**

Run: `GOWORK=off go test ./internal/gateway -run 'TestEntrypointConfigEndToEnd' -count=1` and `GOWORK=off go test ./internal/config -run 'TestExampleConfigLoads' -count=1`.

Expected: PASS. If they fail, fix the backend, not the assertions.

- [x] **Step 3: Run the full regression gate**

```bash
GOWORK=off go build ./...
GOWORK=off go vet ./...
GOWORK=off go test ./... -count=1
cd web && npm test && npm run build
```

Expected: all PASS.

- [x] **Step 4: Search for leftovers**

```bash
rg -n 'Alias|aliases|logical|LogicalModel|logical_model|match\.model|\.models\b' \
  internal web/src example.yaml docs/superpowers/specs/2026-11-05-entrypoint-route-node-design.md
```

Expected: only historical `logical_model` compatibility notes in the design doc
and the inert column comment/tests; no live logical-model routing code.

- [x] **Step 5: Fill the Implementation record and commit**

Fill the record below, then:

```bash
git add internal web example.yaml docs/superpowers/plans/2026-11-05-entrypoint-route-node.md
git commit -m "test: verify entrypoint routing end to end"
```

**Implementation record**
- Date/status: 2026-11-05 / complete
- Repo/commit: llm-gw / b94718ba (e2e code and docs); this record commit is the follow-up
- Files changed: `internal/gateway/entrypoint_e2e_test.go` (new), `internal/config/config_test.go`, plan record.
- Tests: `GOWORK=off go test ./internal/gateway -run TestEntrypointConfigEndToEnd -count=1` PASS; `GOWORK=off go test ./internal/config -run TestExampleConfigLoads -count=1` PASS; `GOWORK=off go build ./...`, `go vet ./...`, `go test ./... -count=1` PASS; `cd web && npm test` PASS (9 files, 75 tests); `npm run build` PASS.
- Review findings/fixes: the end-to-end test loads a real YAML file, routes four client models through the loaded config, then rewrites the entrypoint order and calls `gw.Reload` to prove precedence changes without a restart. `example.yaml` loads with `DEEPSEEK_API_KEY` set and every entrypoint route resolves. The leftover scan found only historical design-doc mentions and the deliberate legacy-config rejection test; the built `internal/admin/dist` bundle was excluded from the scan.
- Deferred/limitations: the upstream in the e2e test is a local httptest server, not a live DeepSeek/OpenAI endpoint. The inert `logical_model` SQLite column is still present in pre-existing databases. The route-order path edge from Task 1 remains.

## Final review fix pass (2026-11-05)

- Fixed the data-plane glob matcher to be case-insensitive (`(?i)`), matching the config/stats resolver.
- An entrypoint whose match list contains `*` now uses an unconditional hop matcher, so the catch-all also covers bodyless pass-through requests.
- `TestReloadFailureKeepsFileAndMemory` now creates a valid provider/node/route, asserts the reload was actually attempted, and compares the config file before/after instead of asserting it does not exist.
- `stats.ErrorRow` now carries the resolved entrypoint name, and the web node panel filters entrypoint errors through `errorMatchesNodeId`.
- Added `TestEntrypointGlobIsCaseInsensitive`, `TestCatchAllMatchesBodylessRequest`, and an entrypoint-error assertion in `TestGraphEntrypointMetrics`.
- Verification: `GOWORK=off go build ./...`, `go vet ./...`, `go test ./... -count=1` PASS; `npx tsc --noEmit`, `npm test` (76 tests), `npm run build` PASS.
