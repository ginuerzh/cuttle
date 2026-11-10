# Entity IDs: stable identity, editable display names

## Intent

Every cuttle entity uses its `name` as both identity and label: `providers[].name`
is the YAML key, node references, admin URL path, stats column, probe-history key,
model-catalog cache key, graph node id (`provider:<name>`) and topology position
key. The same is true for nodes, routes and entrypoints, and for provider keys
and gateway keys. That makes renaming impossible, forces display labels to be
URL-safe and unique, and couples every persisted statistic to a mutable string.

Operators want readable names (Chinese, spaces, brand casing) and to rename a
provider or route without breaking its references, splitting its usage history
or losing its topology position. The fix is one identity model: every entity
carries an immutable, generated `id`; every association, cache, stats column and
API path uses the id; `name` becomes an editable display label.

## Goals

- Every entity (`provider`, `provider key`, `node`, `route`, `entrypoint`,
  `gateway key`) has an `id` and a `name`.
- `id` is generated once, never changes, and is never derived from user input
  once assigned.
- All associations use ids: `node.provider`, `node.key`, `route.nodes`,
  `entrypoint.route`.
- Everything persistent uses ids: stats columns, rollups, probe history,
  topology positions, in-memory caches, graph node ids, x node names.
- `name` is editable at any time through the console/API with zero cascading
  writes; renaming keeps usage history continuous and the topology layout.
- Existing `cuttle.yaml`, an existing v1 `stats.sqlite` and hand-written configs
  migrate automatically and idempotently on load.
- Names stay human-friendly: required, non-empty, unique per entity kind, no
  slug/URL constraints.

## Non-goals

- User-supplied or custom ids, import/export of ids, or syncing identity with an
  external system.
- Renaming an entity's `type` or (for non-generic providers) its API base URLs;
  today's immutability rules stay, keyed by id instead of name.
- Any change to routing semantics, model matching, protocol selection, retry
  policy or the `entrypoint -> route -> node -> provider` chain.
- Human-readable URL slugs for entities; console URLs and API paths use ids.
- Multi-tenant or per-entity ownership.

## Anti-goals

- Two identities. A half-migrated model where some joins use ids and others use
  names is worse than today.
- Treating a name as the join key anywhere in stats, caches or graph ids.
- Silently creating a new identity when an operator renames by hand-editing YAML.
- Acquiring a display name in the proxy hot path (no config lookup per request
  beyond what exists today).

## Constraints

- `config.Load` uses `yaml.KnownFields(true)`; adding `id` is a schema change,
  and every existing config must still load (migration runs before `Validate`).
- Names (and therefore ids and references) are environment-expanded on load, so
  a name containing `$` would change between the console write and the next
  reload. Names reject `$`.
- Migration write-back must happen before env expansion, or `${VAR}` secret
  placeholders would be persisted as resolved values.
- The gateway can read a config that the admin console cannot write (read-only
  mount, or `admin.passwordHash` unset). Identity must remain stable across
  restarts even when write-back fails; renames in such deployments keep the
  existing `id` line by hand.
- `SIGHUP` reload re-reads the file; migration must be idempotent and must not
  renumber anything.
- x node names keep the shape `<route>/<provider>/<node>[/<key>]` and
  `stats.parseNodeName` still splits them; their parts become ids.
- `metadata["configName"]`/`configRoute` feed route health and must become ids.
- Capture rules are in memory and match by route; they move to route ids.
- `go.mod` has no `replace`; run the gate with `GOWORK=off`. Do not edit sibling
  repositories.

## Approaches

Three ways were on the table. The third was chosen.

- **A. Rename-time cascade (no ids).** Keep names as keys; on rename, rewrite
  every reference and migrate the stats rows (`requests`, `rollups`, probe
  history, topology positions) in one transaction. Cheapest schema change, keeps
  YAML human-friendly, but every rename becomes an irreversible data migration
  with a large failure surface (rollup primary-key merges, reload races), and
  display names stay constrained by the identity rules. Rejected: it encodes the
  problem rather than fixing it.
- **B. Add a display name beside the immutable name.** `name` stays the key;
  add `title`/`displayName` for the console. Tiny change, zero migration, but
  leaves two names with different semantics in every API, YAML file and select
  box, and the technical name still leaks into URLs and stats. Rejected: it only
  half-answers "the name is just a label".
- **C. Generated ids for every entity (chosen).** Ids are the only association
  key; names are editable labels. One cut of config schema, admin API, console
  and stats schema, but afterwards renames are free, usage stays continuous and
  topology positions survive. The migration is a one-time cost paid by the
  loader (config write-back + SQLite v2), not by every rename.

The rest of this document specifies C.


### 1. ID shape and generation

- Shape: `<prefix>_<10 lowercase base32 chars>`, e.g. `p_3k9x2m4q7a`,
  `n_...`, `r_...`, `e_...`, `pk_...` (provider key), `gk_...` (gateway key).
- Prefixes: `p` provider, `pk` provider key, `n` node, `r` route, `e`
  entrypoint, `gk` gateway key.
- Generated from `crypto/rand` (6 bytes -> 10 base32 chars, unpadded) at entity
  creation; retried on collision.
- Validation: matches `^[a-z]{1,2}_[0-9a-z]{10}$`, required, **globally unique
  across the whole config** (not just per kind), so an id is unambiguous wherever
  it appears in a log line or a stats column.
- Immutable after creation: the admin API rejects a `PUT` whose body id differs
  from the path, and rejects a body id on create.
- `name` rules: trimmed, non-empty, at most 64 runes, no control characters, no
  `$` (names are env-expanded on load, see Constraints), unique per entity kind
  (case-sensitive, matching today's duplicate rule). Names are otherwise
  unconstrained: spaces, `/` and non-ASCII are fine. Provider key names stay
  unique only inside their provider, as today.
- Provider key ids are derived from `provider id + key name` during migration
  (never from the key name alone), because the same key name on two providers is
  legal and must not collide.

### 2. Config schema

```yaml
entrypoints:
  - id: e_q7a4m2xk9d
    name: Claude Code
    match: ["claude-*"]
    route: r_f3k9x2m4qa
providers:
  - id: p_3k9x2m4q7a
    name: DeepSeek 官方
    type: deepseek
    chat: https://api.deepseek.com/v1
    keys:
      - id: pk_8m2xq7a4k9
        name: dadax
        key: sk-...
nodes:
  - id: n_4q7a9x2m3k
    name: deepseek-v3
    provider: p_3k9x2m4q7a
    key: pk_8m2xq7a4k9      # optional pin, now a provider key id
    model: deepseek-chat
routes:
  - id: r_f3k9x2m4qa
    name: main
    nodes: [n_4q7a9x2m3k]
gateway:
  keys:
    - id: gk_9x2m4q7a3k
      name: laptop
      key: gwk-...
```

`id` is a normal YAML/JSON field; every struct keeps `Name` but gains `ID`.
`ExpandEnv` covers both fields, exactly as it covers names today; ids must
therefore be literal in practice (the generator never emits `$`).

### 3. Load-time migration (`config.Load`)

Order: strict decode -> **migrateIdentity (on the raw document)** -> write back
when something changed -> `expandEnv` -> `applyDefaults` -> `Validate`.

- Write-back runs before environment expansion so `${VAR}` placeholders (API
  keys, base URLs, names) are never baked into the file as resolved values.
- Missing id: derive a stable id from the entity identity,
  `sha256(input)[:6]` base32 with the kind's prefix, where `input` is the
  trimmed name for providers/nodes/routes/entrypoints/gateway keys and
  `providerID + "\x00" + trim(keyName)` for provider keys. Derivation makes
  identity stable across restarts even if write-back fails.
- References (`node.provider`, `node.key`, `route.nodes[]`, `entrypoint.route`)
  resolve in this order: exact id match, then unique name match (legacy configs
  and hand-written edits), else a loud error naming the field and value.
  `node.key` resolves against keys of the node's own provider only, matching
  today's scoping.
- Duplicate names inside one kind make a legacy reference ambiguous: fail with an
  error that tells the operator to add ids.
- After a successful migration, write the raw migrated config back atomically
  (existing `config.Write`, `0600`), after copying the previous file to
  `cuttle.yaml.bak-<unix>`. Write-back is best-effort: log at info on success,
  warn on failure, never abort the load. Once ids are explicit, later loads do
  nothing.
- Read-only or hand-maintained configs: ids may be authored by hand there, and a
  rename must keep the existing `id` line. Dropping the id line makes the name
  the derivation input again, so the entity gets a new identity and its history
  splits. This is the one documented exception to "never edit an id by hand".
  Read-only configs cannot have identities renamed through the console; there is
  no second identity store.

### 4. Runtime: gateway, health, caches

- `internal/gateway/build.go`: providers indexed by id; `node.provider` and
  `node.key` resolve by id; x node name becomes
  `<routeID>/<providerID>/<nodeID>[/<keyID>]`; `metadata["configName"]` /
  `configRoute` carry ids; `registerRewriter` passes provider/node/route ids into
  `stats.Facts`.
- `internal/stats/recorder.go`: `parseNodeName` keeps parsing the same shape but
  now yields ids, so `Request.Provider/Route/Node` are ids. Gateway key
  attribution stores the gateway key id.
- `internal/gateway/gateway.go` route health: keys its marker map by
  `(routeID, nodeID)` and returns `health.Route{ID,Name,...}` /
  `health.Node{ID,Name,ProviderID,...}`.
- Admin caches keyed by id: `modelsCache`, `modelsInflight`, `usageCache`,
  `probes`. Rename therefore invalidates nothing.
- `config.ResolveEntrypointID`, `config.TopologyIncludes(routeID, nodeID,
  providerID)`, capture rules by route id.
- Live aggregator (`stats/graph.go`) emits `provider:<id>`, `node:<id>`,
  `route:<id>`, `entrypoint:<id>`; edges use ids. Topology positions are keyed by
  those ids, so renames keep the layout.

### 5. Stats schema (v2)

The existing text columns change meaning from name to id; no parallel name/id
columns. Display names come from a new `entity_labels` table maintained from the
config.

- `entity_labels(kind TEXT, id TEXT, name TEXT, PRIMARY KEY(kind, id))`. Kinds:
  `provider`, `node`, `route`, `entrypoint`, `gateway_key` (the kinds that appear
  in stats rows or labels). On every config load/reload, upsert one row per
  entity. Rows are never deleted: they are the last-known label for deleted
  entities. Provider keys are not labelled here; they appear only inside their
  provider in the console.
- `PRAGMA user_version = 2` migration, in one transaction after a best-effort
  copy of the database to `stats.sqlite.v1.bak-<unix>`:
  - Build `name -> id` maps from the loaded config. Unknown historical names get
    a derived id with `sha256(name)`, and an `entity_labels` row so they keep
    their label.
  - Rewrite `requests.provider/route/node/gateway_key`, `rollups.provider`,
    `key_rollups.gateway_key`, `probe_results.provider`.
  - Rewrite `topology_positions.node_id` prefixes (`provider:`, `node:`,
    `route:`, `entrypoint:`); leave non-matching keys untouched.
  - `unrouted` rows and empty values stay empty.
  - Because every conversion is name -> unique id, the `rollups` and
    `key_rollups` primary keys stay valid and no series merge.
- A fresh database creates every table in its v2 shape (including
  `entity_labels`) and sets `user_version = 2` at creation, so it never runs the
  rewrite; existing v1 databases run it exactly once.
- `requests` keeps ids only. Historical display uses `entity_labels`, so a rename
  relabels past rows (desired: one continuous series per provider).

### 6. Admin API

- Paths use ids: `/api/providers/{id}`, `/api/nodes/{id}`, `/api/routes/{id}`,
  `/api/gateway-keys/{id}`, `/api/providers/{id}/test`,
  `/api/providers/{id}/models`, `/api/gateway-keys/{id}/rotate`.
- Views return `id` and `name` for every entity; provider key views return
  `{id, name, keyPreview, enabled}`; `keyUpdates` are addressed by key id.
- Creates ignore/require-empty `id` (server assigns); updates reject an id that
  differs from the path; the name is free to change.
- Provider immutability (`type`, non-generic base URLs) and "in use" checks
  (provider <- node, node <- route, route <- entrypoint, gateway key in use)
  compare ids.
- `GET /api/routes/health` returns `{id, name, nodes: [{id, name, providerId,
  ...}]}`.
- Stats endpoints return ids and add resolved labels (`providerId`,
  `providerName`, ...). Request/capture filters take ids.
- `PUT /api/entrypoints` accepts ids; an entrypoint without id is created.
- Breaking change: no compatibility aliases for name-based paths or payloads.

### 7. Console

- Edit drawers for provider, node, route, entrypoint, gateway key and provider
  key gain an editable `name` field with the hint that it is display-only; the
  provider type/base-URL lock hint stays.
- Graph ids and selection use entity ids; labels still come from config, so the
  topology looks the same.
- Selects store ids and render names (`provider` in the node form, `key` in the
  node form, node picker in routes, route picker in entrypoints).
- Requests, errors, usage, capture and alert surfaces prefer the backend-provided
  names, and their filters send ids.
- Presets still fill type + base URLs; the server assigns the id and the name
  defaults to the preset name, editable afterwards.

### 8. Docs, versioning, rollout

- README and `example.yaml` show ids and state: ids are normally generated by the
  console and are immutable; names are free-form labels; hand-written configs may
  omit ids and will be migrated on load, but a hand-maintained (read-only) config
  must keep its `id` lines when renaming.
- `AGENTS.md` invariants to amend in the same change:
  - Configuration: `Load` order gains the migration step before `expandEnv`
    (decode -> migrate/write-back -> `expandEnv` -> `applyDefaults` -> `Validate`);
    new identity rules; provider-key and gateway-key name rules relax to
    "no `$/control characters`" since ids, not names, feed x node names;
    `node.key` names a key id; `example.yaml` loads with ids.
  - Admin API: key-update addressing and write-only key preservation move from
    name to id; gateway-key routes use ids; in-use errors name entities by label.
  - Stats: graph keys become `entrypoint:<id>`, `route:<id>`, `node:<id>`,
    `provider:<id>`; `?key=` filters by gateway key id; the v2 data rewrite is an
    explicit, backed-up exception to the old "no destructive migration" note.
  - Web: `types.ts` gains ids, filters and graph ids use them, selects store ids.
- Breaking config + API + stats change -> `v0.2.0`.
- First start after upgrade rewrites `cuttle.yaml` and migrates `stats.sqlite`
  (with `.bak` copies). Rollback means restoring both backups and the previous
  image. The production k3s deployment (`llm-gw`) must back up the PVC first.

## Decisions (made while writing this)

- **Hard cut, no dual fields.** Keeping both name-derived and id-based columns
  would leave two sources of truth and negative long-term value. The project is
  at v0.1.x.
- **Derived ids for legacy configs.** Deterministic `sha256(name)` derivation
  keeps identity stable when write-back is impossible, and makes the migration
  reproducible. Explicit ids take precedence forever after.
- **Global id uniqueness** across kinds simplifies logs, stats and the console.
- **Names unique per kind** (case-sensitive): duplicates are legal in an id world
  but make operator-facing dropdowns ambiguous.
- **Stats store ids in the existing columns** rather than adding `*_id` columns,
  plus an `entity_labels` table for display; this keeps one meaning per column
  and avoids dual-write code.
- **Provider keys and gateway keys are included.** Both are referenced
  (`node.key`, key rollups) and both are currently name-keyed; leaving them out
  would keep the old problem in place.
- **Names reject `$`.** Keeping env expansion on names (existing behavior, used
  by templated configs) and allowing `$` in names would make a console rename
  change on the next reload and could even collapse two names into a duplicate.
  Names keep expansion; `$` is rejected.
- **Provider key ids derive from provider id + key name.** Key names are unique
  only inside a provider, so name-only derivation would collide in configs that
  reuse a key name on two providers and abort startup.
- **Hand-authored ids are allowed in hand-maintained configs** (the read-only
  deployment case), where a rename keeps the id line. The console remains the
  normal authoring path.
- **Write-back happens before env expansion**, so migration never persists
  secrets or resolved URLs.
- **Labels are never pruned.** The table is tiny; keeping last-known labels is
  what makes deleted entities legible in history.
- **Names are display-only in the data plane.** No request-path lookup of names.

## Left to the builder

- Exact SQL for the v2 migration, index changes and the backup mechanics.
- Whether to reuse a small `identity` helper package or keep generation in
  `internal/config`; either is fine as long as admin and config share one
  generator.
- Test layout and fixture strategy (a committed v1 config and a v1 SQLite fixture
  are expected).
- UI copy and layout of the new name field; the existing antd patterns apply.
- Whether `entity_labels` refresh runs on every reload or only when a name
  actually changed (both acceptable; the former is simpler).
