# Entity IDs Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give every cuttle entity an immutable generated id, move every association and every persisted statistic onto it, and turn `name` into an editable display label.

**Architecture:** A new `internal/identity` package owns id generation, derivation and validation. Config structs gain `ID` next to `Name`; `config.Load` migrates legacy configs (derive ids, rewrite name references) and writes the result back before env expansion. The gateway compiles by id and stamps ids into x node names and stats rows. The stats store rewrites v1 rows to ids once (`user_version = 2`) and keeps a never-pruned `entity_labels` table for display names. The admin API addresses entities by id; the console sends ids and edits names.

**Tech Stack:** Go 1.27 (cuttle module only, `GOWORK=off`), SQLite (`modernc.org/sqlite` via `database/sql`), React 19 + TypeScript + Vite + Ant Design + React Flow, Vitest.

**Spec:** `docs/superpowers/specs/2026-11-10-entity-ids-design.md`

## Global Constraints

- Run every Go command with `GOWORK=off`; never add a `replace` to `go.mod`; never edit the `../core`, `../x`, `../llm-api-converter` siblings.
- id format: `^[a-z]{1,2}_[0-9a-z]{10}$`, prefixes `p` provider, `pk` provider key, `n` node, `r` route, `e` entrypoint, `gk` gateway key; globally unique; immutable.
- `name`: trimmed for comparison, non-empty, at most 64 runes, no control characters, no `$`, unique per kind (provider keys unique per provider). No other restrictions.
- `config.Load` order: decode → migrate + write-back → `expandEnv` → `applyDefaults` → `Validate`. `config.Write` stays atomic with `0600`.
- Write-back happens before env expansion; it copies the old file to `<path>.bak-<unix>` and never aborts the load on failure (warn only).
- Every association (node→provider, node→key, route→nodes, entrypoint→route) is an id after migration. Stats columns, caches, x node names, graph keys and topology positions use ids.
- Code, comments, docs and commit messages are English. Console source strings stay Chinese and get `web/src/locales/en-US.ts` entries.
- Commit after each task, including that task's Implementation record in the same commit. Never push.
- Go gate before a task is called done: `GOWORK=off go build ./...`, `GOWORK=off go vet ./...`, `GOWORK=off go test ./... -count=1`. Web gate for web tasks: `cd web && npm test && npx tsc --noEmit && npm run build`.
- Between Task 2 and Task 7 a temporary name fallback exists in `config.Provider/Node/Route/Entrypoint` lookups so untouched packages keep passing; Task 7 removes it and adds the test that name references are rejected.
- Subagents (if this plan is executed with subagent-driven development): never pass a `model`, never pass a `sessionID`.

## Review Focus

- Upgrading a read-only config mount (write-back fails): two restarts must derive the same ids, and a hand-edit rename that drops the id line must be the documented identity split, not a silent corruption.
- A v1 stats database whose config has renamed or deleted providers/nodes/routes before the upgrade: historical rows must not collide, lose their labels, or be reattributed to a new entity.
- Same provider-key name on two providers, and the same display name reused across kinds: derivation and uniqueness stay scoped correctly.
- Names at the edges (`$`, control characters, >64 runes, leading/trailing spaces, Chinese, `/`): rejected or compared consistently, and never able to break YAML, x node names or the console.
- A rename racing a SIGHUP and an in-flight request: rows record ids, labels refresh, rollups stay one series, and gateway-key rotation keeps its history.

---

### Task 1: Identity primitives and id-bearing config schema

**Files:**
- Create: `internal/identity/identity.go`
- Create: `internal/identity/identity_test.go`
- Modify: `internal/config/config.go` (structs `Entrypoint`, `Provider`, `ProviderKey`, `Node`, `Route`, `GatewayKey`; `expandEnv`; `Validate`)
- Modify: `internal/config/config_test.go`
- Modify: `internal/config/config.go` doc comments for `ProviderKey`/`Node`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `identity.Kind` with constants `Provider`, `ProviderKey`, `Node`, `Route`, `Entrypoint`, `GatewayKey` (values `"provider"`, `"provider_key"`, `"node"`, `"route"`, `"entrypoint"`, `"gateway_key"`).
  - `func New(k Kind) (string, error)` — random id.
  - `func Derive(k Kind, seed string) string` — deterministic id from `sha256(seed)[:6]`.
  - `func Valid(k Kind, id string) bool`, `func Shape(id string) bool`.
  - `ID string \`yaml:"id" json:"id"\`` on all six structs; name validation stays inline in `Validate` (no new exported helper).

- [ ] **Step 1: Write the failing identity tests**

```go
func TestNewShapeAndPrefix(t *testing.T) {
    id, err := identity.New(identity.Provider)
    if err != nil { t.Fatal(err) }
    if !identity.Valid(identity.Provider, id) { t.Fatalf("invalid id %q", id) }
    if strings.HasPrefix(id, "pk_") { t.Fatalf("wrong prefix %q", id) }
}

func TestDeriveStableAndKindScoped(t *testing.T) {
    a := identity.Derive(identity.Provider, "DeepSeek 官方")
    b := identity.Derive(identity.Provider, "DeepSeek 官方")
    if a != b { t.Fatal("derive is not deterministic") }
    if a == identity.Derive(identity.Node, "DeepSeek 官方") { t.Fatal("prefix does not scope derivation") }
    if !identity.Valid(identity.Provider, a) { t.Fatalf("invalid derived id %q", a) }
}

func TestValidRejectsWrongPrefixAndShape(t *testing.T) {
    for _, id := range []string{"", "p_", "p_short", "x_3k9x2m4q7a", "p_3K9X2M4Q7A", "p_3k9x2m4q7a_"} {
        if identity.Valid(identity.Provider, id) { t.Fatalf("accepted %q", id) }
    }
}
```

- [ ] **Step 2: Run the identity test to verify it fails**

Run: `GOWORK=off go test ./internal/identity -run TestNewShape -count=1`
Expected: build failure (`no required module provides package .../internal/identity`).

- [ ] **Step 3: Implement `internal/identity/identity.go`**

10 base32 chars from 6 random bytes (`crypto/rand`, lowercase the standard no-padding encoding), `Derive` from `sha256(seed)[:6]`, `Shape` rejecting anything but `^[a-z]{1,2}_[0-9a-z]{10}$`, `Valid` additionally matching the kind's prefix. `New` returns an error from `rand.Read`.

- [ ] **Step 4: Run the identity tests**

Run: `GOWORK=off go test ./internal/identity -count=1`
Expected: PASS.

- [ ] **Step 5: Add `ID` to the six config structs and cover it in `expandEnv`**

Add `ID string \`yaml:"id" json:"id"\`` to `Entrypoint`, `Provider`, `ProviderKey`, `Node`, `Route`, `GatewayKey`; expand it in `expandEnv` next to each `Name`. Update the `ProviderKey` comment: the name is now a display label, the id is the stable handle.

- [ ] **Step 6: Write the failing validation tests**

```go
func TestValidateNameRules(t *testing.T) {
    for _, name := range []string{"", "  ", "a$b", "ctl\x01", strings.Repeat("x", 65)} {
        // provider name must fail validation
    }
    for _, name := range []string{"DeepSeek 官方", "a/b", "x y", strings.Repeat("x", 64)} {
        // provider name must pass
    }
}

func TestValidateDuplicateNamesAcrossKinds(t *testing.T) {
    // a provider named "shared" and a node named "shared" both validate
}
```

Assertions: name failures return an error naming the offending entity; `/`, spaces and non-ASCII pass; `$` and control characters fail; 65 runes fail, 64 pass; duplicate provider names still fail; duplicate names across kinds pass.

- [ ] **Step 7: Run the validation tests to verify they fail**

Run: `GOWORK=off go test ./internal/config -run 'TestValidateNameRules|TestValidateDuplicateNames' -count=1`
Expected: FAIL (`$`/control-character names currently accepted; 64-rune rule missing).

- [ ] **Step 8: Implement name validation and optional id validation**

In `Validate`, next to each per-kind loop call a shared helper that enforces: trimmed non-empty, ≤64 runes, no control characters, no `$`, duplicate check (existing `seen` maps stay). Add `if id := strings.TrimSpace(p.ID); id != "" && !identity.Valid(identity.Provider, id)` → error for every kind; ids stay optional in this task. Provider-key names keep their per-provider duplicate rule and drop the `/` restriction, keeping `$`/control rejection.

- [ ] **Step 9: Run the config package tests**

Run: `GOWORK=off go test ./internal/config -count=1`
Expected: PASS (fix any fixture that used `/` in names or relied on the old message text).

- [ ] **Step 10: Commit**

```bash
git add internal/identity internal/config
git commit -m "feat(config): add entity ids and display-name validation"
```

**Implementation record**
- Date/status:
- Repository + commit:
- Files changed:
- Tests/commands and results:
- Review findings and fixes:
- Deferred items/limitations:

---

### Task 2: Legacy config migration, write-back and lookups

**Files:**
- Create: `internal/config/migrate.go`
- Create: `internal/config/migrate_test.go`
- Modify: `internal/config/config.go` (`Load` options, new lookups `Provider/Node/Route/Entrypoint/GatewayKey`, `NameToIDs`, `LabelsByID`, `ResolveEntrypointID`, `TopologyIncludes`)
- Modify: `internal/config/config_test.go` (replace `ResolveEntrypointName` uses)
- Modify: `internal/admin/handlers_stats.go`, `internal/admin/handlers_events.go` (call `ResolveEntrypointID`; no behavior change yet)
- Modify: `main.go` (`config.Load(res.ConfigPath, config.WithLogger(log))` on reload)

**Interfaces:**
- Consumes: `identity.New/Derive/Valid` (Task 1), all six structs' `ID`.
- Produces:
  - `func MigrateIdentities(c *Config) (changed bool, err error)` — assigns derived ids to every entity missing one (providers first; provider keys derive from `providerID + "\x00" + name`), then rewrites `node.provider`, `node.key` (scoped to the node's provider), `route.nodes`, `entrypoint.route` from name to id; returns an error naming the field when a reference is neither a known id nor a unique name.
  - `func Load(path string, opts ...LoadOption) (*Config, error)` and `func WithLogger(l logger.Logger) LoadOption`.
  - `func (c *Config) NameToIDs() map[string]map[string]string` — kind → name → id.
  - `func (c *Config) LabelsByID() map[string]map[string]string` — kind → id → name.
  - `func (c *Config) Provider(ref string) (*Provider, bool)`, `Node(ref)`, `Route(ref)`, `Entrypoint(ref)`, `GatewayKey(ref)` — id first, legacy name second (removed in Task 7).
  - `func (c *Config) ResolveEntrypointID(clientModel string) string`.
  - `func (c *Config) TopologyIncludes(routeID, nodeID, providerID string) bool`.
  - `config.Load` writes the migrated raw config back with a `<path>.bak-<unix>` copy and logs the outcome through the optional logger.

- [ ] **Step 1: Write the failing migration tests**

```go
func TestMigrateIdentitiesDerivesAndTracksChanges(t *testing.T) { /* id-less YAML with name refs -> ids everywhere, changed=true, second run changed=false */ }

func TestMigrateProviderKeyIDScopedToProvider(t *testing.T) {
    // two providers each with a key named "default" -> two different pk_ ids
}

func TestMigrateRejectsDanglingAndAmbiguousReferences(t *testing.T) { /* node.provider "nope" -> error; two same-named providers + name ref -> error mentioning ids */ }

func TestLoadReadOnlyConfigDerivesStableIDs(t *testing.T) {
    // config in a read-only dir: two Load calls yield the same ids, no write-back
}

func TestLoadWritesBackMigratedConfigAndBackup(t *testing.T) {
    // writable dir: ids appear in the file, a .bak-* file exists, ${ENV} placeholders stay unexpanded
}

func TestLoadRenameWithoutIDChangesIdentity(t *testing.T) { /* documents the split: id differs after a name-only edit */ }
```

- [ ] **Step 2: Run the migration tests to verify they fail**

Run: `GOWORK=off go test ./internal/config -run 'TestMigrate|TestLoadReadOnly|TestLoadWritesBack|TestLoadRenameWithoutID' -count=1`
Expected: FAIL (`MigrateIdentities` undefined).

- [ ] **Step 3: Implement `MigrateIdentities` and the new lookups**

`MigrateIdentities` mutates in place: assign ids, then resolve references in declaration order with a clear error (`node "x": provider reference "y" is neither an id nor a unique provider name`). `Node(ref)` uses the node's own provider for key resolution before rewriting. Lookups compare `ID` first, then `Name` (case-sensitive `==` for ids, existing `EqualFold` for legacy names only if it was that way before). Change `TopologyIncludes` to compare ids; add `ResolveEntrypointID`.

- [ ] **Step 4: Run the migration tests until green**

Run: `GOWORK=off go test ./internal/config -run 'TestMigrate|TestLoadReadOnly|TestLoadWritesBack|TestLoadRenameWithoutID' -count=1`
Expected: PASS.

- [ ] **Step 5: Change `Load` to migrate and write back before expansion**

New order: decode (strict) → `MigrateIdentities` → when `changed`, copy to `<path>.bak-<unix>` then `Write(path, &c)`, logging success at info and failure at warn through `LoadOption`'s logger (no logger: stay silent, never abort) → `expandEnv` → `applyDefaults` → `Validate`. Callers stay source-compatible via the variadic option.

- [ ] **Step 6: Write the failing required-id test**

```go
func TestValidateRequiresIDs(t *testing.T) {
    // after MigrateIdentities every entity has an id; a hand-built Config with a missing id fails
}

func TestValidateNameReferencesRejected(t *testing.T) {
    // node.provider = provider name (not id) fails with a message that names the id
}
```

Implement ids as required in `Validate`: `identity.Valid` per kind, non-empty, globally unique across kinds. Reference checks accept an existing id **or, until Task 7, a unique name** (the transition that keeps other packages green); `TestValidateNameReferencesRejected` is written now and stays red until Task 7 turns strictness on — mark it `t.Skip("enabled in Task 7")` with that exact reason.

- [ ] **Step 7: Update the config package tests and call sites**

Add `ID` to every `Config` literal in `internal/config/*_test.go` or call `MigrateIdentities` in the fixture helper; replace `ResolveEntrypointName` with `ResolveEntrypointID` in `internal/config/config_test.go` and in the two admin call sites; pass `config.WithLogger(log)` in `main.go`'s reload.

- [ ] **Step 8: Run the config package, the build and the admin package**

Run: `GOWORK=off go test ./internal/config -count=1`
Run: `GOWORK=off go build ./... && GOWORK=off go test ./internal/admin -count=1`
Expected: PASS (any admin failure caused by name-based fixtures is fixed by adding ids, not by weakening validation).

- [ ] **Step 9: Commit**

```bash
git add internal/config
git commit -m "feat(config): migrate legacy configs to entity ids"
```

**Implementation record**
- Date/status:
- Repository + commit:
- Files changed:
- Tests/commands and results:
- Review findings and fixes:
- Deferred items/limitations:

---

### Task 3: Gateway compiles and records by id

**Files:**
- Modify: `internal/gateway/build.go` (provider/route/node/key maps, x node names, metadata, `registerRewriter`)
- Modify: `internal/gateway/gateway.go` (`RouteHealth` marker keys, `configNodeID`/`configRouteID`)
- Modify: `internal/gateway/auth.go` (`authEntry.id`, `authenticate` returns the id)
- Modify: `internal/stats/gatewaykey.go` (rename to id helpers)
- Modify: `internal/health/health.go` (`Route.ID`, `Node.ID`, `Node.ProviderID`)
- Modify: `internal/gateway/*_test.go` fixtures (ids on providers/nodes/routes/entrypoints/keys, refs by id)
- Modify: `internal/gateway/gateway_test.go:24` `testConfig` helper

**Interfaces:**
- Consumes: `config.Provider/Node/Route` lookups, `identity`, all struct ids.
- Produces: `health.Route{ID, Name string; MaxFails int; FailTimeout string; Nodes []health.Node}`, `health.Node{ID, Name, ProviderID string; ...}`; x node names `<routeID>/<providerID>/<nodeID>[/<keyID>]`; node metadata `configNode`/`configRoute` (ids); `stats.WithGatewayKeyID(ctx, id)` / `stats.GatewayKeyIDFromContext(ctx)`.

- [ ] **Step 1: Write the failing gateway id tests**

```go
func TestBuildUsesEntityIDs(t *testing.T) { /* build a config with ids and a rename-able name; assert the x node name contains ids and not names */ }

func TestRouteHealthReportsIDs(t *testing.T) { /* health.Route.ID == route id, health.Node.ID == node id, ProviderID == provider id */ }

func TestAuthAttributesKeyID(t *testing.T) { /* authenticated request records the gateway key id in the recorder row */ }
```

- [ ] **Step 2: Run them to verify they fail**

Run: `GOWORK=off go test ./internal/gateway -run 'TestBuildUsesEntityIDs|TestRouteHealthReportsIDs|TestAuthAttributesKeyID' -count=1`
Expected: FAIL (names in x node names / health payload / context).

- [ ] **Step 3: Implement id-based compilation and health**

`buildHopGroup` maps routes by `r.ID` and entrypoints by `e.Route`; `buildRoutePlan` maps providers by `p.ID`, nodes by `n.ID`, keys by `k.ID`; `resolveNodeKeys` compares `n.Key` to `k.ID`; `buildNodes` names x nodes with ids and sets `metadata["configNode"] = n.ID`, `metadata["configRoute"] = routeID`; `registerRewriter` receives `p.ID`, `n.ID`, `routeID` for facts. `RouteHealth` keys markers by id and fills ids. `authEntry` stores `id`; `WithGatewayKeyID` carries it.

- [ ] **Step 4: Update gateway and health tests**

Every config literal gains ids and id references (or the file's fixture calls `config.MigrateIdentities` once); assertions on x node names and health payloads switch to ids; `stats.WithGatewayKey` call sites become `WithGatewayKeyID`.

- [ ] **Step 5: Run the gateway, health and stats package tests**

Run: `GOWORK=off go test ./internal/gateway ./internal/health ./internal/stats -count=1`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/gateway internal/health internal/stats
git commit -m "feat(gateway): compile and record by entity id"
```

**Implementation record**
- Date/status:
- Repository + commit:
- Files changed:
- Tests/commands and results:
- Review findings and fixes:
- Deferred items/limitations:

---

### Task 4: Stats v2 — entity labels and one-time id migration

**Files:**
- Modify: `internal/stats/store.go` (schema, `path` field, fresh-db detection, `user_version = 2`, `MigrateIdentities`, `SyncLabels`, `Labels`)
- Create: `internal/stats/labels_test.go`
- Create: `internal/stats/migrate_ids_test.go`
- Modify: `internal/stats/store_test.go` (schema assertions)
- Modify: `internal/gateway/gateway.go` (`apply`: migrate + sync labels after a successful hop swap)

**Interfaces:**
- Consumes: `identity.Derive`, `stats.Store`, config lookups.
- Produces:
  - `func (s *Store) MigrateIdentities(ctx context.Context, lookup func(kind, name string) string) error` — backs up to `<path>.v1.bak-<unix>` via `VACUUM INTO` when `user_version < 2` and `path != ":memory:"`, then rewrites `requests.provider/route/node/gateway_key`, `rollups.provider`, `key_rollups.gateway_key`, `probe_results.provider` and `topology_positions.node_id` prefixes; seeds `entity_labels`; sets `user_version = 2`.
  - `func (s *Store) SyncLabels(ctx context.Context, labels map[string]map[string]string) error` (kind → id → name, upsert, never delete).
  - `func (s *Store) Labels(ctx context.Context) (map[string]map[string]string, error)`.
  - `entity_labels(kind TEXT, id TEXT, name TEXT, PRIMARY KEY(kind, id))`.

- [ ] **Step 1: Write the failing label and migration tests**

```go
func TestSyncLabelsUpsertsAndKeepsDeleted(t *testing.T) { /* rename updates, removal keeps the last name */ }

func TestMigrateIdentitiesRewritesEveryColumn(t *testing.T) { /* seed v1 rows with names, run migration, read ids; rollups keyed by id; positions rewritten */ }

func TestMigrateIdentitiesUnknownNames(t *testing.T) { /* name absent from config -> derived id + label, no collision with a second unknown */ }

func TestFreshDatabaseIsVersion2(t *testing.T) { /* Open(":memory:") -> user_version == 2 and entity_labels exists */ }
```

- [ ] **Step 2: Run them to verify they fail**

Run: `GOWORK=off go test ./internal/stats -run 'TestSyncLabels|TestMigrateIdentities|TestFreshDatabase' -count=1`
Expected: FAIL (methods and table missing).

- [ ] **Step 3: Implement schema detection, labels and migration**

`Open` records `path`, checks for an existing `requests` table before creating anything, and sets `user_version = 2` only for a fresh database. `MigrateIdentities` reads `user_version`, runs the backup, then the rewrite inside one transaction, deriving unknown ids via `identity.Derive(kind, name)` and seeding labels. Empty values and `unrouted` stay as they are.

- [ ] **Step 4: Run the stats tests**

Run: `GOWORK=off go test ./internal/stats -count=1`
Expected: PASS.

- [ ] **Step 5: Wire migration and label sync in the gateway**

In `Gateway.apply` (which `New` and `Reload` both call), after the new hop group is built and swapped successfully:

```go
if g.store != nil {
    if err := g.store.MigrateIdentities(ctx, func(kind, name string) string {
        if id, ok := cfg.NameToIDs()[kind][name]; ok {
            return id
        }
        return identity.Derive(identity.Kind(kind), name)
    }); err != nil {
        log warn
    }
    if err := g.store.SyncLabels(ctx, cfg.LabelsByID()); err != nil {
        log warn
    }
}
```

`NameToIDs`/`LabelsByID` are keyed by the `identity.Kind` string values (`"provider"`, …), so `identity.Kind(kind)` is a direct conversion.

Migration is idempotent, so calling it on reload is safe; label sync is what makes a rename visible to history.

- [ ] **Step 6: Write the failing rename-continuity test**

```go
func TestRenameKeepsRollupsAndLabelsFollow(t *testing.T) {
    // insert rows, migrate, rename the provider in the config, SyncLabels, then:
    // ProviderUsage returns one series keyed by the same id and Labels returns the new name
}
```

Run it, then make it pass (it should once Steps 3 and 5 exist; if it exposes the gateway wiring, drive it through `gateway.apply`).

- [ ] **Step 7: Run the gateway and stats packages plus the full build**

Run: `GOWORK=off go test ./internal/stats ./internal/gateway -count=1`
Run: `GOWORK=off go build ./...`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/stats internal/gateway
git commit -m "feat(stats): migrate persisted ids and track entity labels"
```

**Implementation record**
- Date/status:
- Repository + commit:
- Files changed:
- Tests/commands and results:
- Review findings and fixes:
- Deferred items/limitations:

---

### Task 5: Admin — providers and provider keys by id

**Files:**
- Modify: `internal/admin/handlers.go` (`providerView`, `handleCreateProvider`, `handleGetProvider`, `handleUpdateProvider`, `handleDeleteProvider`, `handlePutConfig`, `validateProviderImmutability`, `handleTestProvider`, `handleProviderModels`)
- Modify: `internal/admin/models.go` (`providerModels`, `freshModelsLocked`, `providerLocked`, `invalidateModelsLocked`, `sameProviderFetch` keyed by id)
- Modify: `internal/admin/probe.go` (`probes` map keyed by provider id)
- Modify: `internal/admin/handlers_usage.go` (`usageCache` keyed by provider id, `providerUsageView.ID`)
- Modify: `internal/admin/server.go` (routes `/api/providers/{id}`; cache map comments)
- Modify: `internal/admin/server_test.go`, `internal/admin/handlers_provider_keys_test.go`, `internal/admin/p06_models_test.go`, `internal/admin/usage_test.go`, `internal/admin/probe_test.go`, `internal/admin/probe_tls_test.go`, `internal/admin/entrypoint_test.go`
- Modify: `internal/admin/presets.go` (comment only: presets carry no id)

**Interfaces:**
- Consumes: `identity`, `config.Provider(ref)`, `config.Node(ref)`, id-carrying structs.
- Produces:
  - `providerView.ID`, `providerKeyView.ID`.
  - `PUT /api/providers/{id}`, `POST /api/providers/{id}/test`, `GET /api/providers/{id}/models`; creates assign `identity.New(identity.Provider)` and ignore any body id.
  - `providerKeyUpdate{ID, Name, Key string; Enabled *bool; Delete bool}`; `keyUpdates` addressed by key id, new keys (empty id) appended with a generated `pk_` id.
  - `providerModels(ctx, providerID string, refresh bool)`, `providerLocked(providerID)`, `invalidateModelsLocked` keyed by id.

- [ ] **Step 1: Write the failing admin provider tests**

```go
func TestProviderCRUDByID(t *testing.T) { /* create -> list has id+name -> GET/PUT by id -> rename keeps id, DELETE by id */ }

func TestProviderUpdateRejectsMismatchedID(t *testing.T) { /* PUT /api/providers/<id> with another body id -> 400 */ }

func TestProviderKeyUpdateByID(t *testing.T) { /* rename, enable, rotate value, delete by key id; dangling key id -> 400; delete pinned -> 400 */ }

func TestProviderImmutabilityByID(t *testing.T) { /* type/base-URL changes rejected after a rename (same id) */ }

func TestProviderModelsAndProbeUseID(t *testing.T) { /* catalog and probe endpoints work after rename, keyed by id */ }
```

- [ ] **Step 2: Run them to verify they fail**

Run: `GOWORK=off go test ./internal/admin -run 'TestProvider' -count=1`
Expected: FAIL (path value read as name).

- [ ] **Step 3: Implement id addressing and id-assigned creates**

Path value is the id everywhere; body `id` must match the path or be empty; creates generate ids; `handlePutConfig` preserves provider keys by provider id; immutability compares by id; `viewProvider` emits `id`; caches and `probes` key by id; `buildProviderUsage` copies the id into the view.

- [ ] **Step 4: Update the admin fixtures**

Seed ids in the config literals (or `config.MigrateIdentities` in `newTestAdmin`); paths switch from names to ids; assertions on rename verify the id is unchanged and the name updated.

- [ ] **Step 5: Run the admin provider tests and the package**

Run: `GOWORK=off go test ./internal/admin -run 'TestProvider' -count=1`
Run: `GOWORK=off go test ./internal/admin -count=1`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/admin
git commit -m "feat(admin): address providers and provider keys by id"
```

**Implementation record**
- Date/status:
- Repository + commit:
- Files changed:
- Tests/commands and results:
- Review findings and fixes:
- Deferred items/limitations:

---

### Task 6: Admin — nodes, routes, entrypoints and route health by id

**Files:**
- Modify: `internal/admin/handlers_nodes.go` (CRUD by id, delete checks by node id)
- Modify: `internal/admin/handlers.go` (route CRUD by id, entrypoint PUT assigning ids, delete checks, `handleRouteHealth`)
- Modify: `internal/admin/handlers_capture.go` (route rule lookup by id)
- Modify: `internal/admin/handlers_events.go` (health payload passthrough)
- Modify: `internal/admin/server.go` (paths `/api/nodes/{id}`, `/api/routes/{id}`)
- Modify: `internal/admin/server_test.go`, `entrypoint_test.go`, `capture_test.go`, `p6a_test.go`, `health_test.go`

**Interfaces:**
- Consumes: `identity`, `config.Node/Route/Entrypoint` lookups, `health.Route.ID`/`Node.ID`/`Node.ProviderID`.
- Produces: node/route CRUD by id; `PUT /api/entrypoints` keeps existing ids and assigns `identity.New(identity.Entrypoint)` for entries without one; in-use errors list ids and names; `GET /api/routes/health` returns the id-bearing payload from Task 3.

- [ ] **Step 1: Write the failing tests**

```go
func TestNodeCRUDByIDAndRename(t *testing.T) { /* rename keeps id; provider is an id; delete referenced node -> 400 naming the route */ }

func TestRouteCRUDByIDAndRename(t *testing.T) { /* route.nodes are node ids; delete referenced route -> 400 naming the entrypoint */ }

func TestPutEntrypointsAssignsIDsAndPreservesThem(t *testing.T) { /* existing id kept, new entry gets e_ id, order preserved */ }

func TestRouteHealthIDsExposed(t *testing.T) { /* payload carries route/node/provider ids and names */ }

func TestCaptureRuleUsesRouteID(t *testing.T) { /* unknown route id -> 400; configured id accepted */ }
```

- [ ] **Step 2: Run them to verify they fail**

Run: `GOWORK=off go test ./internal/admin -run 'TestNode|TestRoute|TestPutEntrypoints|TestCaptureRule' -count=1`
Expected: FAIL.

- [ ] **Step 3: Implement id addressing for nodes, routes and entrypoints**

Same shape as Task 5: path value is the id, body id must match or be empty, creates generate ids, `PUT /api/entrypoints` assigns ids to entries without one, delete/in-use checks compare ids, capture rules resolve the route by id.

- [ ] **Step 4: Update the admin fixtures and run the package**

Run: `GOWORK=off go test ./internal/admin -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/admin
git commit -m "feat(admin): address nodes, routes and entrypoints by id"
```

**Implementation record**
- Date/status:
- Repository + commit:
- Files changed:
- Tests/commands and results:
- Review findings and fixes:
- Deferred items/limitations:

---

### Task 7: Admin — gateway keys, id-only lookups and label-enriched rows

**Files:**
- Modify: `internal/admin/handlers_keys.go` (`gatewayKeyView.ID`, `gatewayKeySecret.ID`, CRUD/rotate/usage by id, `key_rollups` join)
- Modify: `internal/admin/server.go` (paths `/api/gateway-keys/{id}`)
- Modify: `internal/admin/handlers_stats.go` (`requestView` with labels, filters by id)
- Create: `internal/admin/labels.go`, `internal/admin/labels_test.go`
- Modify: `internal/config/config.go` (remove the legacy name fallback from `Provider/Node/Route/Entrypoint/GatewayKey`)
- Modify: `internal/config/config_test.go` (unskip `TestValidateNameReferencesRejected`)
- Modify: `internal/admin/*_test.go`, `internal/gateway/*_test.go` (name references gone)
- Modify: `internal/stats/store.go` (`Filter` doc: ids)

**Interfaces:**
- Consumes: `stats.Labels`, `config.LabelsByID`, `config.NameToIDs`.
- Produces:
  - `gatewayKeyUsageRow{ID, Name string; Configured, Enabled bool; ...}`; `POST /api/gateway-keys` and rotate return `{id, name, key}`.
  - `type requestView struct { stats.Request; ProviderName, NodeName, RouteName, GatewayKeyName string }`; `GET /api/requests` and `/api/requests/{id}` return it; `?provider=&route=&node=&key=` accept ids.
  - `type labelSet struct { Provider, Node, Route, Entrypoint, GatewayKey map[string]string }`; `func (s *Server) newLabelSet(ctx context.Context) labelSet` merges `stats.Labels` with `config.LabelsByID` (current config wins); `func (l labelSet) provider(id) string` etc. fall back to the raw id.
  - `config.Provider/Node/Route/Entrypoint/GatewayKey(ref)` accept ids only.

- [ ] **Step 1: Write the failing tests**

```go
func TestGatewayKeyCRUDByID(t *testing.T) { /* create returns id+name+secret, rename keeps id, rotate by id, delete keeps usage history */ }

func TestGatewayKeyUsageJoinsByID(t *testing.T) { /* deleted key rows show the last label from entity_labels; configured rows keep order */ }

func TestRequestViewsCarryLabels(t *testing.T) { /* rows store ids; response adds providerName/nodeName/routeName/gatewayKeyName, current config wins over stored labels */ }

func TestAdminNodeRejectsNameReference(t *testing.T) { /* PUT /api/nodes/<id> with provider = provider name -> 400 mentioning the id */ }
```

- [ ] **Step 2: Run them to verify they fail**

Run: `GOWORK=off go test ./internal/admin -run 'TestGatewayKey|TestRequestViews|TestAdminNodeRejects' -count=1`
Expected: FAIL.

- [ ] **Step 3: Implement gateway-key ids, the labeler and id-only lookups**

Gateway key create assigns `identity.New(identity.GatewayKey)`; update/rotate/delete take the id; usage rows join `KeyUsage` by id and take names from config then labels. Add `labelSet` in `internal/admin/labels.go` and use it in the request handlers. Delete the legacy name branch from the config lookups and remove the `t.Skip` from `TestValidateNameReferencesRejected`; it must pass unchanged.

- [ ] **Step 4: Update remaining fixtures that still reference names**

Run `GOWORK=off go test ./... -count=1` and fix every failure by adding ids/replacing name references; do not weaken assertions.

- [ ] **Step 5: Run the full Go gate**

Run: `GOWORK=off go build ./... && GOWORK=off go vet ./... && GOWORK=off go test ./... -count=1`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal
git commit -m "feat(admin): address gateway keys by id and label stats rows"
```

**Implementation record**
- Date/status:
- Repository + commit:
- Files changed:
- Tests/commands and results:
- Review findings and fixes:
- Deferred items/limitations:

---

### Task 8: Console types, API client and graph ids

**Files:**
- Modify: `web/src/api/types.ts`
- Modify: `web/src/api/client.ts`
- Modify: `web/src/graph/build.ts`, `web/src/graph/build.test.ts`
- Modify: `web/src/pages/routeHealth.ts` (key by node id, expose id-based edges)
- Modify: `web/src/pages/GatewayKeys.tsx` (id-based calls only; layout work stays in Task 9)
- Modify: `web/src/App.test.tsx`

**Interfaces:**
- Consumes: the id-bearing HTTP payloads from Tasks 5–7.
- Produces: `id` on `Provider`, `Node`, `Route`, `Entrypoint`, `GatewayKey`, `ProviderKeyView`; `requestView` fields `providerName`, `nodeName`, `routeName`, `gatewayKeyName`; `client.ts` functions take ids (`updateProvider(id, …)`, `deleteProvider(id)`, `testProvider(id)`, `getProviderModels(id)`, `updateNode(id, …)`, `updateRoute(id, …)`, gateway-key functions); graph ids `provider:<id>`, `node:<id>`, `route:<id>`, `entrypoint:<id>`; `nodeRouteHealth` keyed by node id with edges `route:<id>|node:<id>`.

- [ ] **Step 1: Write the failing graph test**

```ts
it('keys graph nodes and edges by entity id', () => {
  const graph = buildGraph({ ...cfg, providers: [{ id: 'p_3k9x2m4q7a', name: 'DeepSeek', type: 'deepseek', keys: [] }] });
  expect(graph.nodes.some((n) => n.id === 'provider:p_3k9x2m4q7a')).toBe(true);
});
```

- [ ] **Step 2: Run it to verify it fails**

Run: `cd web && npx vitest run src/graph/build.test.ts`
Expected: FAIL (graph ids use names).

- [ ] **Step 3: Update types and the client signatures**

Add ids to the interfaces; rename the id-taking parameters; keep `ProviderInput` as the write payload; `ProviderKeyInput` gains optional `id`; `RouteNodeHealth` gains `id` and `providerId`; `RouteHealth` gains `id`.

- [ ] **Step 4: Switch graph and route-health ids to entity ids**

`buildGraph` indexes by id (provider lookup for a node uses `n.provider` id), edges and entrypoint/route references use ids; `routeHealth` folds `node.id`/`route.id` and builds `route:<id>|node:<id>` edges.

- [ ] **Step 5: Update the mechanical call sites and run the web gate**

Update every page/component the type change touches (id arguments, `id` as React key, `*.id` lookups) so the console keeps building; the rename UI itself stays in Task 9.

Run: `cd web && npm test && npx tsc --noEmit`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add web
git commit -m "feat(web): key console data and graph by entity id"
```

**Implementation record**
- Date/status:
- Repository + commit:
- Files changed:
- Tests/commands and results:
- Review findings and fixes:
- Deferred items/limitations:

---

### Task 9: Console rename UI and id-based forms

**Files:**
- Modify: `web/src/pages/Edit.tsx` (provider/node/route/entrypoint forms: editable name; selects carry ids; key pins by key id; gateway-key page)
- Modify: `web/src/pages/Providers.tsx`, `web/src/pages/Nodes.tsx`, `web/src/pages/Usage.tsx`, `web/src/pages/Requests.tsx`, `web/src/pages/Settings.tsx`, `web/src/pages/GatewayKeys.tsx`
- Modify: `web/src/App.tsx` (selection/query params by id, row labels from `*Name`)
- Modify: `web/src/locales/en-US.ts`
- Modify: `web/src/pages/Edit.dom.test.tsx`, `web/src/pages/Edit.test.tsx`, `web/src/pages/Nodes.test.tsx`, `web/src/pages/Settings.dom.test.tsx`, `web/src/pages/GatewayKeys.test.tsx`, `web/src/App.test.tsx`

**Interfaces:**
- Consumes: Task 8 types/client.
- Produces: editable `name` fields; ids in select values; filters and detail links by id; display labels from the backend names with a raw-id fallback.

- [ ] **Step 1: Write the failing form tests**

```tsx
it('renames a provider without changing its id', async () => { /* open edit, change name, save -> updateProvider called with provider.id and payload.name */ });
it('pins a provider key by id', async () => { /* node form submits key: 'pk_...' */ });
it('renders request row labels from the backend', async () => { /* row shows providerName even when the config has no such id */ });
```

- [ ] **Step 2: Run them to verify they fail**

Run: `cd web && npx vitest run src/pages/Edit.dom.test.tsx src/pages/Edit.test.tsx src/App.test.tsx`
Expected: FAIL.

- [ ] **Step 3: Implement the rename and id plumbing**

Provider/node/route/entrypoint/gateway-key/provider-key forms get a `name` input (hint: display only, editable any time); selects list `id` values labelled by name; provider key rows carry ids in payloads; delete/test/models calls pass ids; lists and topology use ids for keys and selection; request and usage surfaces show `*Name` with an id fallback; summary/error/alert cards resolve ids through the loaded config and fall back to the raw id.

- [ ] **Step 4: Add the Chinese strings and their English translations**

Every new label/hint goes into the source as Chinese and into `web/src/locales/en-US.ts`.

- [ ] **Step 5: Run the full web gate**

Run: `cd web && npm test && npx tsc --noEmit && npm run build`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add web
git commit -m "feat(web): edit entity names and submit ids"
```

**Implementation record**
- Date/status:
- Repository + commit:
- Files changed:
- Tests/commands and results:
- Review findings and fixes:
- Deferred items/limitations:

---

### Task 10: Docs, example config and version

**Files:**
- Modify: `README.md` (ids, migration, rename semantics, read-only caveat)
- Modify: `example.yaml` (ids on every entity, id references)
- Modify: `AGENTS.md` (configuration, admin API, stats and web invariants listed in the spec §8)
- Modify: `internal/config/config_test.go` (assert `example.yaml` loads and validates)

**Interfaces:**
- Consumes: everything above.
- Produces: docs and fixtures consistent with the shipped schema.

- [ ] **Step 1: Write the failing example-config test**

```go
func TestExampleConfigLoads(t *testing.T) {
    // 1. copy ../../example.yaml to t.TempDir() so Load's migration cannot rewrite the repo file
    // 2. decode the raw copy with yaml.KnownFields(true) into config.Config (no migration):
    //    assert every entity has a non-empty ID and every reference is a valid id
    // 3. config.Load(copy) succeeds and the copy is unchanged byte for byte
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `GOWORK=off go test ./internal/config -run TestExampleConfigLoads -count=1`
Expected: FAIL (the example still lists name references and no ids at step 2 of the assertion).

- [ ] **Step 3: Update `example.yaml`, README and AGENTS.md**

Add ids and id references to the example; document that ids are immutable, names are display labels, the loader migrates old files with a `.bak` copy, a read-only/hand-maintained config must keep its `id` lines when renaming, and stats migrate once with a `.bak`. Amend the AGENTS.md invariants exactly as listed in the spec §8 (load order, name rules, `node.key` by id, id-based admin paths and key updates, stats graph ids and `?key=`, web ids). Record that this is a breaking change shipped as `v0.2.0`; the version string itself comes from release ldflags, so no code change is needed.

- [ ] **Step 4: Run the example test and the whole gate**

Run: `GOWORK=off go test ./internal/config -run TestExampleConfigLoads -count=1`
Run: `GOWORK=off go build ./... && GOWORK=off go vet ./... && GOWORK=off go test ./... -count=1`
Expected: PASS.

- [ ] **Step 5: Run the web gate**

Run: `cd web && npm test && npx tsc --noEmit && npm run build`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add README.md example.yaml AGENTS.md internal/config
git commit -m "docs: document entity ids and the v2 migration"
```

**Implementation record**
- Date/status:
- Repository + commit:
- Files changed:
- Tests/commands and results:
- Review findings and fixes:
- Deferred items/limitations:
