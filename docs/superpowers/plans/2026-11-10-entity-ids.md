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

- [x] **Step 1: Write the failing identity tests**

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

- [x] **Step 2: Run the identity test to verify it fails**

Run: `GOWORK=off go test ./internal/identity -run TestNewShape -count=1`
Expected: build failure (`no required module provides package .../internal/identity`).

- [x] **Step 3: Implement `internal/identity/identity.go`**

10 base32 chars from 6 random bytes (`crypto/rand`, lowercase the standard no-padding encoding), `Derive` from `sha256(seed)[:6]`, `Shape` rejecting anything but `^[a-z]{1,2}_[0-9a-z]{10}$`, `Valid` additionally matching the kind's prefix. `New` returns an error from `rand.Read`.

- [x] **Step 4: Run the identity tests**

Run: `GOWORK=off go test ./internal/identity -count=1`
Expected: PASS.

- [x] **Step 5: Add `ID` to the six config structs and cover it in `expandEnv`**

Add `ID string \`yaml:"id" json:"id"\`` to `Entrypoint`, `Provider`, `ProviderKey`, `Node`, `Route`, `GatewayKey`; expand it in `expandEnv` next to each `Name`. Update the `ProviderKey` comment: the name is now a display label, the id is the stable handle.

- [x] **Step 6: Write the failing validation tests**

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

- [x] **Step 7: Run the validation tests to verify they fail**

Run: `GOWORK=off go test ./internal/config -run 'TestValidateNameRules|TestValidateDuplicateNames' -count=1`
Expected: FAIL (`$`/control-character names currently accepted; 64-rune rule missing).

- [x] **Step 8: Implement name validation and optional id validation**

In `Validate`, next to each per-kind loop call a shared helper that enforces: trimmed non-empty, ≤64 runes, no control characters, no `$`, duplicate check (existing `seen` maps stay). Add `if id := strings.TrimSpace(p.ID); id != "" && !identity.Valid(identity.Provider, id)` → error for every kind; ids stay optional in this task. Provider-key names keep their per-provider duplicate rule and drop the `/` restriction, keeping `$`/control rejection.

- [x] **Step 9: Run the config package tests**

Run: `GOWORK=off go test ./internal/config -count=1`
Expected: PASS (fix any fixture that used `/` in names or relied on the old message text).

- [x] **Step 10: Commit**

```bash
git add internal/identity internal/config
git commit -m "feat(config): add entity ids and display-name validation"
```

**Implementation record**
- Date/status: 2026-11-10 — DONE (code in `517f40c1`).
- Repository + commit: `github.com/ginuerzh/cuttle` @ `517f40c1` (`feat(config): add entity ids and display-name validation`); this record is added by the follow-up plan commit.
- Files changed: `internal/identity/identity.go` (new), `internal/identity/identity_test.go` (new), `internal/config/config.go`, `internal/config/config_test.go`.
- Tests/commands and results: RED `GOWORK=off go test ./internal/identity -run TestNewShape -count=1` (no non-test Go files) and the focused config run (`TestValidateNameRules*`, `TestValidateOptionalIDs`, …: `$`/control/64-rune/id cases failed as expected). GREEN `GOWORK=off go test ./internal/config ./internal/identity -count=1`. Extra verification: `GOWORK=off go build ./...`, `GOWORK=off go vet ./internal/config ./internal/identity`, `GOWORK=off go test ./... -count=1` (all packages ok). Full report: `.superpowers/sdd/2026-11-10-entity-ids/task-1-report.md`.
- Review findings and fixes: corrected an invalid 9-character test id (`pk_8m2xq7a4k9`); centralised all five entity kinds on `validateEntityName` and removed the duplicated `gatewayKeyNameMax`/`providerKeyNameMax` constants; name errors now quote the offending name via `%q` while key values stay unlogged; removed a dead assignment and an unreachable fixture branch in the new tests.
- Deferred items/limitations: ids stay optional and are not checked for global uniqueness here (Task 2); `Node.Key` is still name-based (Tasks 2–3); `config.Write` emits `id: ""` for id-less entities until Task 2 migrates before write-back; gateway-key and provider-key name rules relaxed to "no `$`/control characters" — AGENTS.md still documents the old `/` rule and is updated in Task 10; `identity.New` does not retry on collision (admin create paths own that).

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

- [x] **Step 1: Write the failing migration tests**

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

- [x] **Step 2: Run the migration tests to verify they fail**

Run: `GOWORK=off go test ./internal/config -run 'TestMigrate|TestLoadReadOnly|TestLoadWritesBack|TestLoadRenameWithoutID' -count=1`
Expected: FAIL (`MigrateIdentities` undefined).

- [x] **Step 3: Implement `MigrateIdentities` and the new lookups**

`MigrateIdentities` mutates in place: assign ids, then resolve references in declaration order with a clear error (`node "x": provider reference "y" is neither an id nor a unique provider name`). `Node(ref)` uses the node's own provider for key resolution before rewriting. Lookups compare `ID` first, then `Name` (case-sensitive `==` for ids, existing `EqualFold` for legacy names only if it was that way before). Change `TopologyIncludes` to compare ids; add `ResolveEntrypointID`.

- [x] **Step 4: Run the migration tests until green**

Run: `GOWORK=off go test ./internal/config -run 'TestMigrate|TestLoadReadOnly|TestLoadWritesBack|TestLoadRenameWithoutID' -count=1`
Expected: PASS.

- [x] **Step 5: Change `Load` to migrate and write back before expansion**

New order: decode (strict) → `MigrateIdentities` → when `changed`, copy to `<path>.bak-<unix>` then `Write(path, &c)`, logging success at info and failure at warn through `LoadOption`'s logger (no logger: stay silent, never abort) → `expandEnv` → `applyDefaults` → `Validate`. Callers stay source-compatible via the variadic option.

- [x] **Step 6: Write the failing required-id test**

```go
func TestValidateRequiresIDs(t *testing.T) {
    // after MigrateIdentities every entity has an id; a hand-built Config with a missing id fails
}

func TestValidateNameReferencesRejected(t *testing.T) {
    // node.provider = provider name (not id) fails with a message that names the id
}
```

Implement ids as required in `Validate`: `identity.Valid` per kind, non-empty, globally unique across kinds. Reference checks accept an existing id **or, until Task 7, a unique name** (the transition that keeps other packages green); `TestValidateNameReferencesRejected` is written now and stays red until Task 7 turns strictness on — mark it `t.Skip("enabled in Task 7")` with that exact reason.

- [x] **Step 7: Update the config package tests and call sites**

Add `ID` to every `Config` literal in `internal/config/*_test.go` or call `MigrateIdentities` in the fixture helper; replace `ResolveEntrypointName` with `ResolveEntrypointID` in `internal/config/config_test.go` and in the two admin call sites; pass `config.WithLogger(log)` in `main.go`'s reload.

- [x] **Step 8: Run the config package, the build and the admin package**

Run: `GOWORK=off go test ./internal/config -count=1`
Run: `GOWORK=off go build ./... && GOWORK=off go test ./internal/admin -count=1`
Expected: PASS (any admin failure caused by name-based fixtures is fixed by adding ids, not by weakening validation).

- [x] **Step 9: Commit**

```bash
git add internal/config
git commit -m "feat(config): migrate legacy configs to entity ids"
```

**Implementation record**
- Date/status: 2026-11-10 — DONE (code in `3ca7064f`, review fixes in `89e134f5`); this record is added by the follow-up plan commit.
- Repository + commit: `github.com/ginuerzh/cuttle` @ `3ca7064f` (`feat(config): migrate legacy configs to entity ids`) + `89e134f5` (`fix(config): enforce unique entity ids and keep the graph filter id-safe`).
- Files changed: `internal/config/migrate.go` (new), `internal/config/migrate_test.go` (new), `internal/config/config.go`, `internal/config/config_test.go`, `internal/admin/handlers_stats.go`, `internal/admin/handlers_events.go`, `internal/admin/p6a_test.go` (id in the entrypoint fixture), `internal/bootstrap/bootstrap.go` (test seam wrapper for the new `Load` signature), `main.go`.
- Tests/commands and results: RED `GOWORK=off go test ./internal/config -run 'TestMigrate|TestLoadReadOnly|TestLoadWritesBack|TestLoadRenameWithoutID|TestValidateNameReferencesRejected' -count=1` → `undefined: MigrateIdentities` (build failure). GREEN the same command, then `GOWORK=off go test ./internal/config -count=1` and `GOWORK=off go test ./internal/admin -count=1`. Gate: `GOWORK=off go build ./...`, `GOWORK=off go vet ./...` and `GOWORK=off go test ./internal/config ./internal/admin -count=1` all pass. `GOWORK=off go test ./... -count=1` passes everywhere except `internal/gateway` `TestEntrypointConfigEndToEnd` and `TestZeroConfigOnboarding`, which fail with `entrypoint "…": unknown route "r_…"` because `internal/gateway/build.go` still keys routes, providers, nodes and keys by name — Task 3 (Step 3) moves those maps to ids and owns those two e2e tests. Full report: `.superpowers/sdd/2026-11-10-entity-ids/task-2-report.md`. **Post-review fix run:** RED `GOWORK=off go test ./internal/config -run 'TestValidateRejectsDuplicateIDs|TestTopologyIncludesNameKeyedRowsAfterMigration' -count=1` (five of six duplicate-id subtests accepted the config; the name-keyed row was dropped), GREEN the same command, then `GOWORK=off go test ./internal/config -count=1`, `GOWORK=off go build ./...`, `GOWORK=off go vet ./...`, `gofmt -l .` and `GOWORK=off go test ./... -count=1` (only the two Task 3 gateway e2e failures above).
- Review findings and fixes: `TestExampleConfigLoads` loaded `../../example.yaml` in place, so the new write-back rewrote the repository file during the first green run (restored; the test now copies the file into `t.TempDir()`); `bootstrap.go`'s `var loadConfig = config.Load` no longer matched the `func(string)` test seam after the variadic option, so it became an explicit wrapper; the write-back refuses to rewrite the file when the `.bak-<unix>` copy cannot be created; `validateEntityID` now rejects ids with surrounding whitespace instead of trimming them (ruling 2) with a new `provider padded id` case; `TestGraphEntrypointAPI` gained a real entrypoint id instead of weakening its assertions; `TestTopologyIncludes` and `TestResolveEntrypointOrder` were moved to ids. **Post-review fixes (`fix(config): enforce unique entity ids and keep the graph filter id-safe`):** (1) `Validate` now enforces global id uniqueness through an `idRegistry` (id → entity scope) claimed for all six kinds — gateway keys, providers, provider keys, nodes, routes, entrypoints — and reports `…: duplicate id "p_…", already used by …` naming both entities, with `TestValidateRejectsDuplicateIDs` covering every kind; ids are claimed after the duplicate-name check so a shared name (which also shares a derived id) still reports as a duplicate name (`TestValidateDuplicateNameBeatsDuplicateID`). (2) `TopologyIncludes` resolves the recorded node and provider references through `Node`/`Provider` and compares identities (id first, name for id-less configs), so name-keyed stats rows from between Task 2 and Task 3 are kept again, with `TestTopologyIncludesNameKeyedRowsAfterMigration` covering name/id/mixed rows plus unknown, off-route and provider-mismatch drops.
- Deferred items/limitations: ids stay optional in `Validate` and reference checks still accept a unique name (`TestValidateNameReferencesRejected` is skipped with `enabled in Task 7`), per the controller ruling; the legacy name fallback in `Provider/Node/Route/Entrypoint/GatewayKey` and `TopologyIncludes` disappears in Task 7 (the gateway still records name-keyed stats rows until Task 3). The read-only write-back subtest skips when the suite runs as root (root bypasses the `0555` directory); a second subtest still pins deterministic derivation, and the failure path is `warn`-only by construction. Migration writes raw YAML, so comments and formatting in a migrated file are lost (Task 10 documents the `.bak` copy). `example.yaml` stays id-less until Task 10. `NameToIDs` omits names that repeat within a kind (provider key names shared by two providers), while `LabelsByID` stays id-keyed and unambiguous. `node.key` is still name-based in the gateway until Task 3.

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

- [x] **Step 1: Write the failing gateway id tests**

```go
func TestBuildUsesEntityIDs(t *testing.T) { /* build a config with ids and a rename-able name; assert the x node name contains ids and not names */ }

func TestRouteHealthReportsIDs(t *testing.T) { /* health.Route.ID == route id, health.Node.ID == node id, ProviderID == provider id */ }

func TestAuthAttributesKeyID(t *testing.T) { /* authenticated request records the gateway key id in the recorder row */ }
```

- [x] **Step 2: Run them to verify they fail**

Run: `GOWORK=off go test ./internal/gateway -run 'TestBuildUsesEntityIDs|TestRouteHealthReportsIDs|TestAuthAttributesKeyID' -count=1`
Expected: FAIL (names in x node names / health payload / context).

- [x] **Step 3: Implement id-based compilation and health**

`buildHopGroup` maps routes by `r.ID` and entrypoints by `e.Route`; `buildRoutePlan` maps providers by `p.ID`, nodes by `n.ID`, keys by `k.ID`; `resolveNodeKeys` compares `n.Key` to `k.ID`; `buildNodes` names x nodes with ids and sets `metadata["configNode"] = n.ID`, `metadata["configRoute"] = routeID`; `registerRewriter` receives `p.ID`, `n.ID`, `routeID` for facts. `RouteHealth` keys markers by id and fills ids. `authEntry` stores `id`; `WithGatewayKeyID` carries it.

- [x] **Step 4: Update gateway and health tests**

Every config literal gains ids and id references (or the file's fixture calls `config.MigrateIdentities` once); assertions on x node names and health payloads switch to ids; `stats.WithGatewayKey` call sites become `WithGatewayKeyID`.

- [x] **Step 5: Run the gateway, health and stats package tests**

Run: `GOWORK=off go test ./internal/gateway ./internal/health ./internal/stats -count=1`
Expected: PASS.

- [x] **Step 6: Commit**

```bash
git add internal/gateway internal/health internal/stats
git commit -m "feat(gateway): compile and record by entity id"
```

**Implementation record**
- Date/status: 2026-11-10 — DONE (code in `c9d26dfc`); this record is added by the follow-up plan commit.
- Repository + commit: `github.com/ginuerzh/cuttle` @ `c9d26dfc` (`feat(gateway): compile and record by entity id`).
- Files changed: `internal/gateway/build.go`, `internal/gateway/gateway.go`, `internal/gateway/auth.go`, `internal/gateway/count_tokens_policy.go`, `internal/health/health.go`, `internal/stats/gatewaykey.go`, `internal/stats/recorder.go`; tests `internal/gateway/gateway_test.go`, `internal/gateway/identity_fixture_test.go` (new, `withIDs` fixture helper for the `gateway_test` package), `internal/gateway/auth_test.go`, `internal/gateway/count_tokens_policy_test.go`, `internal/gateway/node_keys_test.go`, `internal/gateway/protocol_auto_test.go`, `internal/gateway/retry_e2e_test.go`, `internal/gateway/stats_test.go`, `internal/gateway/sticky_test.go`, `internal/stats/gatewaykey_test.go`, `internal/stats/recorder_test.go`; compile-only fixture renames forced by the `health.Node` field change in `internal/admin/server_test.go` and `internal/admin/events_test.go`.
- Tests/commands and results: RED `GOWORK=off go test ./internal/gateway -run 'TestBuildUsesEntityIDs|TestRouteHealthReportsIDs|TestAuthAttributesKeyID' -count=1` → build failure (`undefined: configNodeID`, `health.Route has no field ID`, `health.Node has no field ID/ProviderID`); after the build/health change and before the auth change the auth test failed at runtime with `gateway key = "alpha", want the key id gk_0000000001` while the recorder row already showed id-valued `Provider`/`Route`/`Node`. GREEN the same command (all three pass), then `GOWORK=off go test ./internal/gateway ./internal/health ./internal/stats -count=1` → PASS, and the Task 2 carry-over `TestEntrypointConfigEndToEnd` and `TestZeroConfigOnboarding` now pass. Gate: `GOWORK=off go build ./...`, `GOWORK=off go vet ./...`, `GOWORK=off go test ./internal/gateway ./internal/health ./internal/stats ./internal/config -count=1` and `GOWORK=off go test ./... -count=1` all pass; `gofmt -l internal` is clean. Full report: `.superpowers/sdd/2026-11-10-entity-ids/task-3-report.md`.
- Review findings and fixes: the first draft left the old name-keyed `routes` map in `buildHopGroup` unused after switching to `cfg.Route`, so it was deleted; the x hop `Name` was moved from `route/<name>/<variant>` to `route/<routeID>/<variant>` so diagnostics match the id-based node names; `buildRoutePlan` now copies the resolved provider before applying the `gateway.userAgent` default, because the lookups return config pointers and the default must not mutate the loaded config; `TestRouteHealthReportsIDs` marks a node with the fixture's default `maxFails` first, which made it dead after one failure, so the fixture pins `MaxFails: 2`; `TestBuildNodesSuffixUsesKeyName` became `TestBuildNodesSuffixUsesKeyID` and `TestGatewayAuthRecordsGatewayKey` became `TestAuthAttributesKeyID` (same scenario, id assertion); the plan key falls back to `"name:" + r.Name` instead of the bare name so a transient id-less route can never collide with another route's id; `RouteHealth` now resolves each route node through `cfg.Node` and skips references that no longer exist (validation already rejects them) instead of emitting a synthetic entry. **Post-review fixes (`fix(gateway): keep id-less entities distinct during the id migration`):** an id-less config (an admin-written config until Tasks 4-6) collapsed every node of a route onto one rewriter engine — `buildRewriterName` produced the same `r//<variant>/` key for every node, `registerRewriter` unregisters before it registers, so the last node's engine won and every node of that route converted to the last node's target model; `buildNodes` likewise emitted identical `//` x node names, and `authEntry.id` was empty for every key so `requests.gateway_key` could not tell an authenticated request from an unauthenticated one. Per the controller ruling the transition now uses one idiom — `id, else name` — through a single helper, `entityRef(id, name)`: `buildVariantHop` names rewriters with the route and node refs (and documents why the name must stay per node), `buildNodes` builds the x node name from the route/provider/node/key refs while the node metadata keeps the ids, and `newAuthPolicy` stores `entityRef(key.ID, key.Name)`. `buildNodes` now takes the whole `gwconfig.Route` so it can use the route name in the name and the route id in the metadata. Tests: `TestIdLessNodesKeepDistinctEngines` builds a fully id-less two-node, two-provider route, asserts the x node names and the registered rewriter names are exactly one per node (no collapse), then runs the gateway and asserts each upstream receives its own target model over a round-robin pair; `TestAuthAttributesKeyNameWithoutID` asserts an id-less gateway key still records a non-empty `requests.gateway_key` (the key name), sharing a new `authenticatedRow` helper with `TestAuthAttributesKeyID`.
- Deferred items/limitations: a config written by the admin API is still id-less until Task 4 migrates in `apply` and Tasks 5/6 mint ids on create, so the gateway resolves such references by name and its id-valued node metadata stays empty for those deployments in this intermediate commit (no route-health entries, no capture route attribution); `routePlanKey`'s name fallback disappears once ids are mandatory. Capture rules filter by route name in `internal/capture`, while the rewriter now reports the route id, so name-filtered rules stop matching until Task 6 resolves capture rules by id. `requests.gateway_key`, `key_rollups` and `Filter.GatewayKey` now hold ids, so the console's exact-name key filter and the usage page show nothing until Task 7 joins labels by id. `health.Node.Provider` was replaced by `ProviderID` (no console consumer read it; Task 8 adds `id`/`providerId` to the TypeScript type). `AGENTS.md`/`README.md` still describe name-keyed lookups and stats rows; Task 10 updates them. **Transition fallbacks that Task 7 removes together with `routePlanKey`:** `entityRef` (x node names and rewriter registration names) and the `authEntry.id` name fallback. Until then an id-less route's node metadata (`configNode`/`configRoute`) and the rewriter facts still carry empty ids, so `RouteHealth` skips those nodes and capture rules see an empty route; that is the pre-existing Task 3 limitation above, not the collapse this fix removes. Refs that concatenate a name can also mix an id and a name in one compiled name when a route/provider/node is id-less and its neighbour is not; names may contain `/`, so an id-less name can in principle make two component layouts look alike — both disappear with mandatory ids in Task 7.

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

- [x] **Step 1: Write the failing label and migration tests**

```go
func TestSyncLabelsUpsertsAndKeepsDeleted(t *testing.T) { /* rename updates, removal keeps the last name */ }

func TestMigrateIdentitiesRewritesEveryColumn(t *testing.T) { /* seed v1 rows with names, run migration, read ids; rollups keyed by id; positions rewritten */ }

func TestMigrateIdentitiesUnknownNames(t *testing.T) { /* name absent from config -> derived id + label, no collision with a second unknown */ }

func TestFreshDatabaseIsVersion2(t *testing.T) { /* Open(":memory:") -> user_version == 2 and entity_labels exists */ }
```

- [x] **Step 2: Run them to verify they fail**

Run: `GOWORK=off go test ./internal/stats -run 'TestSyncLabels|TestMigrateIdentities|TestFreshDatabase' -count=1`
Expected: FAIL (methods and table missing).

- [x] **Step 3: Implement schema detection, labels and migration**

`Open` records `path`, checks for an existing `requests` table before creating anything, and sets `user_version = 2` only for a fresh database. `MigrateIdentities` reads `user_version`, runs the backup, then the rewrite inside one transaction, deriving unknown ids via `identity.Derive(kind, name)` and seeding labels. Empty values and `unrouted` stay as they are.

- [x] **Step 4: Run the stats tests**

Run: `GOWORK=off go test ./internal/stats -count=1`
Expected: PASS.

- [x] **Step 5: Wire migration and label sync in the gateway**

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

- [x] **Step 6: Write the failing rename-continuity test**

```go
func TestRenameKeepsRollupsAndLabelsFollow(t *testing.T) {
    // insert rows, migrate, rename the provider in the config, SyncLabels, then:
    // ProviderUsage returns one series keyed by the same id and Labels returns the new name
}
```

Run it, then make it pass (it should once Steps 3 and 5 exist; if it exposes the gateway wiring, drive it through `gateway.apply`).

- [x] **Step 7: Run the gateway and stats packages plus the full build**

Run: `GOWORK=off go test ./internal/stats ./internal/gateway -count=1`
Run: `GOWORK=off go build ./...`
Expected: PASS.

- [x] **Step 8: Commit**

```bash
git add internal/stats internal/gateway
git commit -m "feat(stats): migrate persisted ids and track entity labels"
```

**Implementation record**
- Date/status: 2026-11-10, done.
- Repository + commit: `github.com/ginuerzh/cuttle` — code + tests in `a8908067 feat(stats): migrate persisted ids and track entity labels`, this record in the following `docs(plan): record entity ids task 4 implementation` commit.
- Files changed: `internal/stats/store.go` (schema v2: `entity_labels`, `path` field, `isFresh` detection, `statsVersion`, `MigrateIdentities`, `backupV1`, `SyncLabels`, `Labels`), `internal/stats/gatewaykey.go` (comment: the value is the key identifier, id or display-name fallback until Task 7), `internal/stats/store_test.go` (`TestFreshDatabaseIsVersion2` + `entity_labels` schema assertion), new `internal/stats/labels_test.go`, `internal/stats/migrate_ids_test.go`, `internal/stats/open_version_test.go`, `internal/gateway/gateway.go` (`apply`: migrate then sync labels after a successful hop swap, warnings only), new `internal/gateway/stats_identity_test.go`.
- Tests/commands and results: RED `GOWORK=off go test ./internal/stats -run 'TestSyncLabels|TestLabels|TestMigrateIdentities|TestFreshDatabase|TestRename|TestOpenLeaves' -count=1` → build failure (`SyncLabels`, `Labels`, `MigrateIdentities`, `statsVersion` undefined). An intermediate RED (`sql: no rows in result set`) exposed that the fresh path skipped the `request_seq` seed; fixed in `migrate(fresh)`. GREEN the same command, then `GOWORK=off go test ./internal/stats -count=1` (`ok`, 14.3s), `GOWORK=off go test ./internal/gateway -run TestApplyMigratesStatsAndSyncsLabels -count=1` (`ok`). Gate: `GOWORK=off go build ./...`, `GOWORK=off go vet ./...`, `GOWORK=off go test ./internal/stats ./internal/gateway ./internal/config -count=1` and the full `GOWORK=off go test ./... -count=1` all pass; `gofmt -l` clean. Full report: `.superpowers/sdd/2026-11-10-entity-ids/task-4-report.md`.
- Review findings and fixes: fresh databases skipped the `request_seq` seed (caught by the existing suite, fixed before commit); the design's "no series merge" assumption does not hold for Task 3-era id-keyed rows, so `rollups`/`key_rollups` are summed into the id row instead of overwriting it; a legacy and an id-keyed topology position can coexist, so the id-keyed one wins; `SyncLabels` filters to the five stats kinds so `config.LabelsByID()`'s `provider_key` entries are not stored; seeded labels use `ON CONFLICT DO NOTHING` so a synced rename wins; the test opens the backup and asserts it still holds the v1 name.
- Deferred items/limitations: `probe_results` migrates to ids while `internal/admin/probe.go` still writes provider names until Task 5, so the table holds both kinds in the interim (`LatestProbes` has no production consumer); name-keyed consumers (`Filter.GatewayKey` exact-name filter, provider/key usage pages) show nothing for migrated history until Tasks 5/7 join `entity_labels`; backup file permissions follow the umask (nothing specifies `0600`); a retried migration within the same second is refused by `VACUUM INTO` and warns until the next apply (a failed migration never advances `user_version`); Task 10 documents schema v2, the backup and the "no destructive migration" exception.

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

- [x] **Step 1: Write the failing admin provider tests**

```go
func TestProviderCRUDByID(t *testing.T) { /* create -> list has id+name -> GET/PUT by id -> rename keeps id, DELETE by id */ }

func TestProviderUpdateRejectsMismatchedID(t *testing.T) { /* PUT /api/providers/<id> with another body id -> 400 */ }

func TestProviderKeyUpdateByID(t *testing.T) { /* rename, enable, rotate value, delete by key id; dangling key id -> 400; delete pinned -> 400 */ }

func TestProviderImmutabilityByID(t *testing.T) { /* type/base-URL changes rejected after a rename (same id) */ }

func TestProviderModelsAndProbeUseID(t *testing.T) { /* catalog and probe endpoints work after rename, keyed by id */ }
```

- [x] **Step 2: Run them to verify they fail**

Run: `GOWORK=off go test ./internal/admin -run 'TestProvider' -count=1`
Expected: FAIL (path value read as name).

- [x] **Step 3: Implement id addressing and id-assigned creates**

Path value is the id everywhere; body `id` must match the path or be empty; creates generate ids; `handlePutConfig` adopts the stored provider and key ids for an id-less payload matched by name (minting `identity.New` only for a genuinely new entity) and preserves provider keys by provider id; immutability compares by id; `viewProvider` emits `id`; caches and `probes` key by id; `buildProviderUsage` copies the id into the view.

- [x] **Step 4: Update the admin fixtures**

Seed ids in the config literals (or `config.MigrateIdentities` in `newTestAdmin`); paths switch from names to ids; assertions on rename verify the id is unchanged and the name updated.

- [x] **Step 5: Run the admin provider tests and the package**

Run: `GOWORK=off go test ./internal/admin -run 'TestProvider' -count=1`
Run: `GOWORK=off go test ./internal/admin -count=1`
Expected: PASS.

- [x] **Step 6: Commit**

```bash
git add internal/admin
git commit -m "feat(admin): address providers and provider keys by id"
```

**Implementation record**
- Date/status: 2026-11-10 — DONE (code in `68177f51`, review fix in `2b4b7b2c`).
- Repository + commit: `github.com/ginuerzh/cuttle` @ `68177f51` (`feat(admin): address providers and provider keys by id`), review fix @ `2b4b7b2c` (`fix(admin): adopt stored ids for id-less config replacements`); this record is added by the follow-up plan commit.
- Files changed: `internal/admin/handlers.go` (`providerView.ID`, `providerKeyView.ID`, `providerIndexByID`, `validateProviderImmutability`, `handleGetProvider`, `handleCreateProvider`, `providerKeyUpdate{ID,…}`, `applyProviderKeyUpdates`, `mergeProviderKeys`, `sameProviderRef`, `sameKeyRef`, `ensureKeysNotPinned`, `handleUpdateProvider`, `handleDeleteProvider`, `handleTestProvider`, `handleProviderModels`, `handlePutConfig`), `internal/admin/models.go` (id-keyed catalog cache and flights, `providerLocked`, `invalidateModelsLocked`, `sameProviderFetch` ignores the name), `internal/admin/probe.go` (`probes[p.ID]`, `stats.ProbeResult{Provider: p.ID}`), `internal/admin/handlers_usage.go` (`providerUsageView.ID`, id-keyed usage cache, local rollup joined by provider id), `internal/admin/server.go` (`/api/providers/{id}` paths, cache-map comments), `internal/admin/presets.go` (comment only); tests: new `internal/admin/providers_id_test.go` (the five Step-1 tests plus `createProviderTest`/`providerUpdateBody` helpers), `internal/admin/server_test.go` (`providerJSON`/`providerKeyJSON` wire types, `listProviders`/`getProvider`/`providerID`, id paths, `TestProviderKeyUpdates` rewritten around key ids), `internal/admin/handlers_provider_keys_test.go` (helpers return ids, key-update and merge tests rewritten for ids, id assertions in the config round-trip, usage view and full-key replacement), `internal/admin/p06_models_test.go` (`createProviderP`/`providerURL`, id paths, cache assertions keyed by id, ids in both `PUT /api/config` payloads), `internal/admin/p6a_test.go` (probe keyed by id), `internal/admin/usage_test.go` (`setUsageProviders` migrates ids, rollup row inserted under the provider id).
- Tests/commands and results: RED `GOWORK=off go test ./internal/admin -run 'TestProvider' -count=1` → all five Step-1 tests failed with the path value read as a name: `TestProviderCRUDByID: id = "", want a generated provider id`, `TestProviderUpdateRejectsMismatchedID: mismatched id = 404`, `TestProviderKeyUpdateByID: get provider "" = 404`, `TestProviderImmutabilityByID: rename = 404`, `TestProviderModelsAndProbeUseID: GET .../api/providers//models = 404`. GREEN the same five (`-v`: 5/5 PASS), then `GOWORK=off go test ./internal/admin -count=1` (ok). Gate: `GOWORK=off go build ./...`, `GOWORK=off go vet ./...`, `GOWORK=off go test ./internal/admin ./internal/config ./internal/gateway ./internal/stats -count=1` and the full `GOWORK=off go test ./... -count=1` all pass; `gofmt -l internal/admin` clean. `CGO_ENABLED=1 GOWORK=off go test -race ./internal/admin -run 'TestProvider|TestProbe|TestUsage|TestKeyUpdates|TestMergeProviderKeys|TestPutConfig|TestNodeAPIRoundTrip' -count=1` passes; the package-wide `-race` run still flags the pre-existing `TestEventsClosesWhenSessionExceedsMaxAge` race (the test writes `s.wsMaxAge` without the mutex), reproduced unchanged on `HEAD` in a temporary worktree. Full report: `.superpowers/sdd/2026-11-10-entity-ids/task-5-report.md`. Review fix `2b4b7b2c`: the three new `TestPutConfig*` tests fail on the pre-fix handler (`providers ... want the stored id`, `id = "", want a minted provider id`, keys `[]` for the omitted-keys payload) and pass afterwards; `GOWORK=off go test ./internal/admin -count=1` (ok) and `GOWORK=off go build ./... && GOWORK=off go vet ./...` (clean).
- Review findings and fixes: (1) the pinned-key check must match a pin against the stored *and* the incoming provider snapshot — matching only the renamed provider let a rename silently drop a key a node still pins — so `sameProviderRef`/`ensureKeysNotPinned` take every snapshot of the provider. (2) `handleCreateProvider` routes the body's `keys` through `mergeProviderKeys(nil, …)`, so a created provider's keys carry `pk_` ids; without it key updates by id were unusable on anything the API created. (3) `validateProviderImmutability` matches by id and keeps a name fallback for id-less entries, so an id-less `PUT /api/config` still cannot change a type or base URL (`TestProviderTypeAndURLsImmutable` unchanged). (4) `sameProviderFetch` now ignores `Name`, so a rename keeps the cached catalog (asserted by `TestProviderModelsAndProbeUseID`); the fixture `TestProviderModelsKeepCacheOnModelOverrideChange` sends the key id because a key list without ids is a new key identity by design. (5) `TestProviderFullKeysReplacementPinned` now asserts the pinned key id survives a value replacement instead of only the value. (6) `TestKeyUpdatesDeleteByName`/`TestKeyUpdatesMissingNameRejected` became `TestKeyUpdatesDeleteByID`/`TestKeyUpdatesMissingIDRejected`, and `TestProbeOnce`/`TestProbeProviderAnthropicAPI` assert the probe record under the provider id. (7) **Review finding — an id-less `PUT /api/config` stored a provider with an empty id**, which was unaddressable for writes (`providerIndexByID` returns -1 for `""`, so `PUT`/`DELETE /api/providers/{id}` could only 400 it), dropped the stored key values through the `continue` at the old `handlePutConfig`, and shared the `""` cache/probe entry across two id-less providers. Fixed in `2b4b7b2c`: an id-less incoming provider adopts the stored id matched by trimmed name and otherwise mints `identity.New(identity.Provider)`; each id-less incoming key adopts the stored key id matched by name inside that provider and otherwise gets a minted `pk_` id from `mergeProviderKeys` (`adoptKeyIDs`); a known provider with an absent `keys` field still preserves the stored keys. Covered by `TestPutConfigAdoptsStoredProviderID`, `TestPutConfigMintsIDForNewProvider` and `TestPutConfigKeepsStoredKeysWithoutIDOrKeys`.
- Deferred items/limitations: `PUT /api/config` adopts the stored identity for an id-less provider or provider key matched by name and mints `identity.New` only for a genuinely new entity (`mint` on create, `p_`/`pk_` on a new config entry), so an id-less payload can no longer store an empty id, drop stored key values or share the empty cache/probe key; a by-name rename through `PUT /api/config` is still not inferred, so a renamed provider needs its id (or a name change would create a new entity). The `web/` console still calls name paths (`updateProvider(name)`, `testProvider(name)`, models by name) until Task 8, so editing an existing provider from the console fails with 400 in the interim, while name-based *reads* still resolve. `AGENTS.md`'s admin-API invariants still describe name-addressed `keyUpdates` and name-keyed `PUT /api/config` preservation; Task 10 amends them (README/`example.yaml` untouched). The id-or-name read fallback (`GET /api/providers/{ref}`, `POST …/test`, `GET …/models`) and the node/key reference fallbacks in `sameProviderRef`/`sameKeyRef` are removed in Task 7. Nodes, routes, entrypoints and gateway keys still get a new identity from an id-less `PUT /api/config` until Tasks 6/7 apply the same adoption rule. `internal/admin/dist` was not rebuilt (no console change in this task).

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

- [x] **Step 1: Write the failing tests**

```go
func TestNodeCRUDByIDAndRename(t *testing.T) { /* rename keeps id; provider is an id; delete referenced node -> 400 naming the route */ }

func TestRouteCRUDByIDAndRename(t *testing.T) { /* route.nodes are node ids; delete referenced route -> 400 naming the entrypoint */ }

func TestPutEntrypointsAssignsIDsAndPreservesThem(t *testing.T) { /* existing id kept, new entry gets e_ id, order preserved */ }

func TestRouteHealthIDsExposed(t *testing.T) { /* payload carries route/node/provider ids and names */ }

func TestCaptureRuleUsesRouteID(t *testing.T) { /* unknown route id -> 400; configured id accepted */ }
```

- [x] **Step 2: Run them to verify they fail**

Run: `GOWORK=off go test ./internal/admin -run 'TestNode|TestRoute|TestPutEntrypoints|TestCaptureRule' -count=1`
Expected: FAIL.

- [x] **Step 3: Implement id addressing for nodes, routes and entrypoints**

Same shape as Task 5: path value is the id, body id must match or be empty, creates generate ids, `PUT /api/entrypoints` assigns ids to entries without one, delete/in-use checks compare ids, capture rules resolve the route by id.

- [x] **Step 4: Update the admin fixtures and run the package**

Run: `GOWORK=off go test ./internal/admin -count=1`
Expected: PASS.

- [x] **Step 5: Commit**

```bash
git add internal/admin
git commit -m "feat(admin): address nodes, routes and entrypoints by id"
```

**Implementation record**
- Date/status: 2026-11-10, done.
- Repository + commit: `github.com/ginuerzh/cuttle` — code + tests in `cd2f7cdb feat(admin): address nodes, routes and entrypoints by id`, this record in the following `docs(plan): record entity ids task 6 implementation` commit.
- Files changed: `internal/admin/handlers_nodes.go` (`nodeIndexByID`, `sameNodeRef`, create mints `identity.New(identity.Node)`, update/delete by id with id-vs-path check, delete blocks a node referenced by a route naming both ids and labels, reads still id-then-name); `internal/admin/handlers.go` (`entityLabel`, `adoptEntityID`, `routeIndexByID`, `sameRouteRef`, route create/update/delete by id, delete lists the referencing entrypoints as `"name" (id)`, `PUT /api/entrypoints` keeps/adopts/mints ids without reordering, `PUT /api/config` adopts stored ids for nodes, routes and entrypoints, comment on the id-bearing route-health passthrough); `internal/admin/handlers_capture.go` (route rule validated as a route id); `internal/admin/server.go` (`/api/nodes/{id}`, `/api/routes/{id}`); new `internal/admin/entities_id_test.go`; fixtures in `internal/admin/entrypoint_test.go`, `server_test.go`, `capture_test.go`. `internal/admin/handlers_events.go` needed no edit: both `/api/events` snapshots already emit `currentRouteHealth()`.
- Tests/commands and results: RED `GOWORK=off go test ./internal/admin -run 'TestNode|TestRoute|TestPutEntrypoints|TestCaptureRule' -count=1` → four failures (a supplied node id was stored verbatim; routes/nodes had no ids so id-based references were empty). `TestRouteHealthIDsExposed` passed at RED because Task 3 already made the payload id-bearing (`handleRouteHealth` always passed it through); it pins that contract. GREEN the same command plus `TestPutConfigAdoptsStoredEntityIDs`. Step 4 fixture run failed on five name-addressed tests (`TestCaptureAPI`, `TestRouteDeleteAfterEntrypointRemoved`, `TestRouteRetryFieldsRoundTrip`, `TestProviderCRUDMasksKeys`, `TestNodeAPIRoundTripThroughAdmin`); fixed by attaching ids and using id paths, no weakened assertions. Gate: `gofmt -l` clean, `GOWORK=off go build ./...`, `GOWORK=off go vet ./...`, `GOWORK=off go test ./internal/admin ./internal/config ./internal/gateway ./internal/stats -count=1` and the full `GOWORK=off go test ./... -count=1` all pass. Full report: `.superpowers/sdd/2026-11-10-entity-ids/task-6-report.md`.
- Review findings and fixes: route delete now checks the entrypoint references before removing the route (previously it removed first and relied on the discarded clone); an omitted update name keeps the stored label instead of being forced to the path value; `PUT /api/config` adoption runs inside `mutate` before `*dst = c`, so a minting error changes neither memory nor file, and the adoption maps come from the stored snapshot; in-use errors name every referencing entity with both its id and its label.
- Deferred items/limitations: the in-use checks and reads keep the name fallback (`sameNodeRef`, `sameRouteRef`, `config.Node`) until Task 7 makes ids mandatory; `PUT /api/config` adopts entity ids but does not rewrite name references (`node.provider`, `route.nodes`, `entrypoint.route`), which stay valid for validation until Task 7; a node rename of a legacy config whose route references the node by name is rejected by validation (dangling name), so such a config must be converted with the console first; the web console still addresses nodes/routes by name and sends entrypoint route names until Task 8, so node/route mutations from the console fail in the interim (same gap providers already have); route-scoped capture rules require a route id, which every file loaded through `config.Load` has.

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

- [x] **Step 1: Write the failing tests**

```go
func TestGatewayKeyCRUDByID(t *testing.T) { /* create returns id+name+secret, rename keeps id, rotate by id, delete keeps usage history */ }

func TestGatewayKeyUsageJoinsByID(t *testing.T) { /* deleted key rows show the last label from entity_labels; configured rows keep order */ }

func TestRequestViewsCarryLabels(t *testing.T) { /* rows store ids; response adds providerName/nodeName/routeName/gatewayKeyName, current config wins over stored labels */ }

func TestAdminNodeRejectsNameReference(t *testing.T) { /* PUT /api/nodes/<id> with provider = provider name -> 400 mentioning the id */ }
```

- [x] **Step 2: Run them to verify they fail**

Run: `GOWORK=off go test ./internal/admin -run 'TestGatewayKey|TestRequestViews|TestAdminNodeRejects' -count=1`
Expected: FAIL.

- [x] **Step 3: Implement gateway-key ids, the labeler and id-only lookups**

Gateway key create assigns `identity.New(identity.GatewayKey)`; update/rotate/delete take the id; usage rows join `KeyUsage` by id and take names from config then labels. Add `labelSet` in `internal/admin/labels.go` and use it in the request handlers. Delete the legacy name branch from the config lookups and remove the `t.Skip` from `TestValidateNameReferencesRejected`; it must pass unchanged.

- [x] **Step 4: Update remaining fixtures that still reference names**

Run `GOWORK=off go test ./... -count=1` and fix every failure by adding ids/replacing name references; do not weaken assertions.

- [x] **Step 5: Run the full Go gate**

Run: `GOWORK=off go build ./... && GOWORK=off go vet ./... && GOWORK=off go test ./... -count=1`
Expected: PASS.

- [x] **Step 6: Commit**

```bash
git add internal
git commit -m "feat(admin): address gateway keys by id and label stats rows"
```

**Implementation record**
- Date/status: 2026-11-10 — DONE (code in `f60c1dd8`); this record is added by the follow-up plan commit.
- Repository + commit: `github.com/ginuerzh/cuttle` @ `f60c1dd8` (`feat(admin): address gateway keys by id and label stats rows`). No sibling repository changed.
- Files changed: `internal/admin/handlers_keys.go` (create mints `identity.New(identity.GatewayKey)`, update/rotate/delete by path id, `id` on the view/secret/usage rows, `key_rollups` joined by id, deleted keys ordered by last label), `internal/admin/labels.go` (new: `labelSet`, `newLabelSet`, `requestView`), `internal/admin/labels_test.go` (new: `TestLabelSetPrefersConfigThenStoredLabels`, `TestLabelSetWithoutStatsStore`, `TestRequestViewsCarryLabels`), `internal/admin/handlers_stats.go` (`requestView` for both request endpoints, entrypoint filter by id), `internal/admin/server.go` (`/api/gateway-keys/{id}` paths), `internal/admin/handlers.go` (`PUT /api/config` adopts/mints gateway-key ids; id-only `sameProviderRef`/`sameKeyRef`/`sameRouteRef`; provider immutability by id only), `internal/admin/handlers_nodes.go` (id-only `sameNodeRef`), `internal/admin/models.go` (comments, id-keyed cache guards), `internal/admin/handlers_keys_test.go` (`TestGatewayKeyCRUDByID`, `TestGatewayKeyUsageJoinsByID`, `TestPutConfigAdoptsStoredGatewayKeyID`, ids everywhere else), `internal/admin/entities_id_test.go` (`TestAdminNodeRejectsNameReference`, id-carrying `PUT /api/config` fixture), `internal/admin/server_test.go`, `internal/admin/p6a_test.go`, `internal/admin/handlers_provider_keys_test.go` (id fixtures), `internal/config/config.go` (mandatory ids, id-only references and lookups, `nameRefError`, id-only `TopologyIncludes`, `sameNodeReference` removed), `internal/config/config_test.go` (`topologyFixture`, `fixtureID`, `withGatewayKeyIDs`, `TestValidateRequiresIDs`, `TestValidateEntrypoints` missing-id case, name-reference rejection cases, `TestTopologyIncludesIDKeyedRows`), `internal/config/migrate_test.go` (`TestValidateNameReferencesRejected` unskipped, `TestEntityLookupsUseIDsOnly`), `internal/gateway/build.go` (`entityRef` and `routePlanKey` removed), `internal/gateway/auth.go`, `internal/gateway/count_tokens_policy.go`, `internal/gateway/*_test.go` (id fixtures; `idless_transition_test.go` and `identity_fixture_test.go` deleted; `TestAuthAttributesKeyNameWithoutID` removed), `internal/stats/store.go` (`Filter`/`KeyUsage` id docs, migration lookup trim), `internal/stats/gatewaykey.go` (doc), `internal/stats/migrate_ids_test.go` (`TestMigratePaddedRefUsesTrimmedLookup`).
- Tests/commands and results: RED `GOWORK=off go test ./internal/admin -run 'TestGatewayKey|TestRequestViews|TestAdminNodeRejects|TestLabelSet' -count=1` → build failure (`s.newLabelSet undefined`, `requestView` missing), then, after the labeler landed, `TestAdminNodeRejectsNameReference` (the error named the offending value but not the expected id) and `TestRequestViewsCarryLabels` (one row instead of three) failed on real assertions. GREEN the same command. Gate: `GOWORK=off go build ./...`, `GOWORK=off go vet ./...`, `gofmt -l .` clean, `GOWORK=off go test ./internal/admin ./internal/config ./internal/gateway ./internal/stats -count=1` and `GOWORK=off go test ./... -count=1` (14 packages ok, no failures). Full report: `.superpowers/sdd/2026-11-10-entity-ids/task-7-report.md`.
- Review findings and fixes: (1) a name reference is rejected with an actionable error: `nameRefError` matches the value against the kind's display names and reports `… is a display name; use the provider id "p_…"` (or `unknown provider id "…"`), which is what `TestValidateNameReferencesRejected` and the new admin test require; (2) `PUT /api/gateway-keys/{id}` rejects a body id that differs from the path, like providers, nodes and routes, and accepts an optional `name` so a label can be renamed without losing the id (the console's rename UI in Task 9 needs it) — `AGENTS.md`'s "PUT changes only enabled" line is therefore stale and is amended in Task 10; (3) every gateway-key and config fixture was converted to ids (`topologyFixture`, `fixtureID`, `keyFixtureID`, id literals), and the two `withIDs`/`fixtureIDs` migration helpers were removed, so no test reaches a lookup with a display name; (4) the request-list entrypoint filter now takes the entrypoint id (it is a query-time client-model match, so no column changed) and unknown ids still return `[]`; (5) `TestTopologyIncludesNameKeyedRowsAfterMigration` became `TestTopologyIncludesIDKeyedRows`: name-keyed rows are now dropped instead of reattributed, since the fallback that kept them is gone.
- Deferred items/limitations: the console still calls the name-based gateway-key paths (`/api/gateway-keys/{name}`) and name-keyed `?entrypoint=`, so the gateway-key page and the entrypoint filter are broken until Task 8 updates `web/src`; the data plane is unaffected. `labelSet.entrypoint` is produced for the request views but nothing surfaces an entrypoint label yet (rows store `client_model`, resolved at query time). Gateway-key usage joins by id, so a rollup written by a pre-Task-3 release is migrated once by `stats.MigrateIdentities` and keeps its label; a key deleted before that migration has no stored label and shows its raw id. A literal `$` in a renamed gateway-key name is still rejected on the next `Validate` (names are env-expanded on load), as before. `internal/admin/dist` was not rebuilt (no console change in this task), and `README.md`/`example.yaml`/`AGENTS.md` remain Task 10.

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

- [x] **Step 1: Write the failing graph test**

```ts
it('keys graph nodes and edges by entity id', () => {
  const graph = buildGraph({ ...cfg, providers: [{ id: 'p_3k9x2m4q7a', name: 'DeepSeek', type: 'deepseek', keys: [] }] });
  expect(graph.nodes.some((n) => n.id === 'provider:p_3k9x2m4q7a')).toBe(true);
});
```

- [x] **Step 2: Run it to verify it fails**

Run: `cd web && npx vitest run src/graph/build.test.ts`
Expected: FAIL (graph ids use names).

- [x] **Step 3: Update types and the client signatures**

Add ids to the interfaces; rename the id-taking parameters; keep `ProviderInput` as the write payload; `ProviderKeyInput` gains optional `id`; `RouteNodeHealth` gains `id` and `providerId`; `RouteHealth` gains `id`.

- [x] **Step 4: Switch graph and route-health ids to entity ids**

`buildGraph` indexes by id (provider lookup for a node uses `n.provider` id), edges and entrypoint/route references use ids; `routeHealth` folds `node.id`/`route.id` and builds `route:<id>|node:<id>` edges.

- [x] **Step 5: Update the mechanical call sites and run the web gate**

Update every page/component the type change touches (id arguments, `id` as React key, `*.id` lookups) so the console keeps building; the rename UI itself stays in Task 9.

Run: `cd web && npm test && npx tsc --noEmit`
Expected: PASS.

- [x] **Step 6: Commit**

```bash
git add web
git commit -m "feat(web): key console data and graph by entity id"
```

**Implementation record**
- Date/status: 2026-11-10 — DONE (code in `acaadeb0`); this record is added by the follow-up plan commit.
- Repository + commit: `github.com/ginuerzh/cuttle` @ `acaadeb0` (`feat(web): key console data and graph by entity id`). No sibling repository changed.
- Files changed: `web/src/api/types.ts` (`id` on `Provider`/`Node`/`Route`/`Entrypoint`/`GatewayKey`/`ProviderKeyView`, optional `id` on `ProviderKeyInput`, `RouteNodeHealth.id`/`providerId` replacing `provider`, `RouteHealth.id`, four `*Name` labels on `RequestRow`, id/label comments on every association field), `web/src/api/client.ts` (every mutation and lookup takes `id`; `GatewayKeySecret` with `id` for create/rotate; `EntrypointInput` so a new entry may omit its id; `RequestQuery` documented as id-valued), `web/src/graph/build.ts` + `build.test.ts` (graph and edge ids from entity ids, names stay the labels, node `sub`/entrypoint `sub` resolve the referenced entity's name as before), `web/src/pages/routeHealth.ts` + `routeHealth.test.ts` (fold by node id, `route:<routeId>|node:<nodeId>` edges, new `folds by node id, not by the display name` case), `web/src/pages/selection.ts` + `selection.test.ts` (`entrypointFromNodeId` resolves by id), `web/src/pages/Edit.tsx` (client calls by id, panel/connection/edge-form lookups by id, `useProviderModels(providerId)`, `RouteNodeList`/provider/route selects valued by id with the existing name labels, `EntrypointForm` keeps the entrypoint id on save), `web/src/pages/Providers.tsx` (test/models/toggle/delete by id; the model modal tracks `{id, name}`), `web/src/pages/Nodes.tsx` (toggle/delete/key/busy by id, provider and pinned-key names resolved from config), `web/src/pages/Routes.tsx` (health map by route id, delete by id, node names resolved from config), `web/src/pages/Entrypoints.tsx` (`key={e.id}`, route name in the subtitle), `web/src/pages/GatewayKeys.tsx` (toggle/rotate/delete and the `?key=` session filter by id, secret state typed `GatewayKeySecret`), `web/src/pages/presets.ts` (`nodeKeyOptions` values are key ids, labels unchanged), `web/src/pages/Topology.tsx` (comment only), `web/src/App.tsx` (`sessionQuery` reads the id from the graph id), plus fixtures in `web/src/App.test.tsx`, `web/src/api/client.test.ts`, `web/src/api/events.test.ts`, `web/src/components/RequestDetail.test.tsx`, `web/src/pages/Edit.test.tsx`, `web/src/pages/Edit.dom.test.tsx`, `web/src/pages/Nodes.test.tsx`, `web/src/pages/GatewayKeys.test.tsx`, `web/src/pages/Requests.test.ts`, `web/src/pages/presets.test.ts`.
- Tests/commands and results: RED `cd web && npx vitest run src/graph/build.test.ts` → 5 failed / 5 (`keeps the display names as labels` returned `undefined`, every id-keyed edge assertion false), because the graph still keyed by name. GREEN the same command after `build.ts` (5 passed), then `npx vitest run src/graph/build.test.ts src/pages/routeHealth.test.ts src/pages/selection.test.ts` → 14 passed. `npx tsc --noEmit` enumerated every remaining fixture/call site (`TS2739`/`TS2741` on the missing ids and request labels, `TS2322` on the id-less new entrypoint). Gate: `npm test` → 38 files / 263 tests passed; `npx tsc --noEmit` clean; `npm run build` → 3446 modules, `internal/admin/dist` rebuilt. Two new client tests pin the contract: every id-addressed mutation/lookup path, and the five id-valued request filters. Full report: `.superpowers/sdd/2026-11-10-entity-ids/task-8-report.md`.
- Review findings and fixes: (1) the id change reaches beyond the compile errors: `Node.provider`, `Entrypoint.route`, `Route.nodes`, `node.key` and `routeHealth` members hold ids, so every select that carries an association was switched to id values with the previous labels (`config.providers`/`config.routes`/`nodeKeyOptions`, plus `RouteNodeList`'s row map, keys and add-select). Leaving them name-valued would have submitted display names into id fields on save; this is the one deliberate step past "mechanical", and it is covered by the updated `Edit.dom.test.tsx` (`updateNode` called with `n_…` and `key: 'pk_…'`) and the `nodeKeyOptions` case in `presets.test.ts`. (2) `Provider.keyUpdates` stays name-based because that is still the admin payload shape; only `ProviderKeyView.id` and `ProviderKeyInput.id` were added. (3) `putEntrypoints` now takes `EntrypointInput`, so a newly added entry stays id-less in the request and the admin API mints one, while an edit spreads the existing entry and therefore keeps its id. (4) Where the old console printed a name it now resolves the id through the loaded config (graph node/entrypoint subtitles, node card provider and pinned-key tags, route node list, route card, entrypoint card), so no display regressed in those places. (5) `ProviderForm` resets its key rows on `provider.id`, not on the name, so a rename cannot reset the editor. (6) `gatewayKeyState` keeps its `Pick<GatewayKey, 'enabled'>` contract; only the card fixture gained an id.
- Deferred items/limitations: request surfaces still render raw ids — `SessionCard`/`Requests.tsx` (`route · provider/node`), `RequestDetail` (`route`, `provider`), `requestLabels.gatewayKeyLabel` (`key:<id>`) and `NodePanel`'s `dead 路由` list — because showing `providerName`/`nodeName`/`routeName`/`gatewayKeyName` is Task 9's "labels from the backend" work with its own test. Editable `name` fields (provider/node/route) stay absent, and `Routes.tsx`/`Nodes.tsx`/`Entrypoints.tsx`/`GatewayKeys.tsx` still use the name only as a label. Provider usage cards keep the name-based `ProviderUsage`/`GatewayKeyUsage` shapes, which the admin API still returns name-labelled. `internal/admin/dist` is git-ignored, so the rebuilt bundle is not part of the commit; the Go gate was not rerun for this web-only change. `README.md`/`example.yaml`/`AGENTS.md` are still Task 10.

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

- [x] **Step 1: Write the failing form tests**

```tsx
it('renames a provider without changing its id', async () => { /* open edit, change name, save -> updateProvider called with provider.id and payload.name */ });
it('pins a provider key by id', async () => { /* node form submits key: 'pk_...' */ });
it('renders request row labels from the backend', async () => { /* row shows providerName even when the config has no such id */ });
```

- [x] **Step 2: Run them to verify they fail**

Run: `cd web && npx vitest run src/pages/Edit.dom.test.tsx src/pages/Edit.test.tsx src/App.test.tsx`
Expected: FAIL.

- [x] **Step 3: Implement the rename and id plumbing**

Provider/node/route/entrypoint/gateway-key/provider-key forms get a `name` input (hint: display only, editable any time); selects list `id` values labelled by name; provider key rows carry ids in payloads; delete/test/models calls pass ids; lists and topology use ids for keys and selection; request and usage surfaces show `*Name` with an id fallback; summary/error/alert cards resolve ids through the loaded config and fall back to the raw id.

- [x] **Step 4: Add the Chinese strings and their English translations**

Every new label/hint goes into the source as Chinese and into `web/src/locales/en-US.ts`.

- [x] **Step 5: Run the full web gate**

Run: `cd web && npm test && npx tsc --noEmit && npm run build`
Expected: PASS.

- [x] **Step 6: Commit**

```bash
git add web
git commit -m "feat(web): edit entity names and submit ids"
```

**Implementation record**
- Date/status: 2026-11-10 — DONE (code in `93362b6c`); this record is added by the follow-up plan commit.
- Repository + commit: `github.com/ginuerzh/cuttle` @ `93362b6c` (`feat(web): edit entity names and submit ids`). No sibling repository changed.
- Files changed: new `web/src/labels.ts` (`labelMap`, `entityLabel`, `configLabels`, `graphLabel`) and its test; new `web/src/pages/Requests.dom.test.tsx` and `web/src/pages/GatewayKeys.dom.test.tsx`; `web/src/App.tsx` (`SessionCard` labels + `key:<gatewayKeyName>`, `NodePanel` `label`, `dead 路由` names, `EdgePanel` `graphLabel` path, `configLabels` memo, `RequestsPage config`), `web/src/pages/Edit.tsx` (required `名称` field with the display-only hint on provider/route/node/entrypoint, editable provider-key names, id-addressed key updates, exported `RouteNodeList` using `entityLabel`, id-based `EdgeForm` labels), `web/src/pages/presets.ts` (`ProviderKeyRow.id/originalName`, `ProviderKeyUpdate.id`, id-addressed `providerKeyUpdates`), `web/src/pages/Requests.tsx` (id-valued provider/route/node/entrypoint/gateway-key filter selects, id-valued capture-route select, `*Name` row labels, capture-rule route labels, `node`/`entrypoint` filters sent), `web/src/pages/GatewayKeys.tsx` (rename dialog calling `updateGatewayKey(id, {name})`, usage joined by key id, historical cards keyed by id, session rows labelled by route name), `web/src/pages/Nodes.tsx`/`Routes.tsx`/`Entrypoints.tsx` (`entityLabel` instead of inline lookups), `web/src/pages/Usage.tsx` (`provider.id` key + id fallback title), `web/src/pages/Topology.tsx` (`sameRoutes`/`mergeNodeHealth` parameters renamed to ids), `web/src/api/types.ts` (`ProviderUsage.id`, `GatewayKeyUsage.id`, `GatewayKeySecret` moved here), `web/src/api/client.ts` (`updateGatewayKey` accepts `name`), `web/src/components/RequestDetail.tsx` (route/key/provider/node names with id fallback), `web/src/requestLabels.ts` (`key:<gatewayKeyName>`), `web/src/locales/en-US.ts`; tests `web/src/App.test.tsx`, `web/src/pages/Edit.dom.test.tsx`, `web/src/pages/Edit.test.tsx`, `web/src/pages/Nodes.test.tsx`, `web/src/pages/GatewayKeys.test.tsx`, `web/src/pages/presets.test.ts`, `web/src/components/RequestDetail.test.tsx`.
- Tests/commands and results: RED `cd web && npx vitest run src/pages/Edit.dom.test.tsx src/pages/Edit.test.tsx src/App.test.tsx` → 8 failed / 22 passed (no name inputs, name-based key delete, unexported `RouteNodeList`, unlabelled `SessionCard`/`NodePanel`, missing route-name field); RED `npx vitest run src/labels.test.ts src/pages/Requests.dom.test.tsx src/pages/GatewayKeys.dom.test.tsx src/pages/Nodes.test.tsx src/components/RequestDetail.test.tsx` → 5 failed / 5 passed (`./labels` missing, free-text filters, no rename action). GREEN `cd web && npm test` → 41 files / 285 tests passed (baseline 38/263), `npx tsc --noEmit` clean, `npm run build` → 3447 modules, `internal/admin/dist` rebuilt. Go gate with the rebuilt bundle: `GOWORK=off go build ./...`, `GOWORK=off go vet ./...` and `GOWORK=off go test ./... -count=1` all pass. Follow-up review fixes re-ran the gate: `npm test` → 41 files / 287 tests passed (two new cases), `npx tsc --noEmit` clean, `npm run build` → `internal/admin/dist` rebuilt, `GOWORK=off go build ./...` OK. Full report: `.superpowers/sdd/2026-11-10-entity-ids/task-9-report.md`.
- Review findings and fixes: existing provider-key rows use `aria-label` instead of the `key 名称` placeholder so the older DOM tests keep addressing the new-row inputs; `providerKeyUpdates` only emits an existing-key update when the name or enable flag changed, and a delete is sent as `{id, delete:true}` with no name (the admin API rejects that combination); a blank name on an existing row blocks the save; `NodePanel` takes a `label` prop (resolved in `App`) so it stays config-free for its SSR tests; the gateway-key rename button only renders for configured keys. Review round 2 (follow-up fixes, carried by the commit that also ticks these steps): (a) the gateway-key page now shows the display-only hint (`仅用于显示，可随时修改；实体 id 不变。`) under the name input of both the rename and the create dialog, so every listed form carries the hint the plan mandates — pinned by `GatewayKeys.dom.test.tsx` ("hints that the name is display-only in the rename and add dialogs"); (b) the provider-key hint no longer claims a node pins a key by name: it now says a node can pin one of these keys and that a key rename never touches the nodes that pin it, in both the Chinese source string and its `en-US` translation, pinned by `Edit.test.tsx` ("says a node pins a key, so a rename does not touch nodes", which also asserts the old `按名称固定` wording is gone and checks the English sentence); (c) the Task 9 steps above are ticked now that the task is DONE.
- Deferred items/limitations: `Settings.tsx` and `Providers.tsx` needed no edit (nothing there prints an id, and the provider rename is the shared form they already embed); the Requests error-summary `byProvider`/`byRoute` groups still are not rendered at all (they were not before either), so id→name resolution there is unused — the "summary card" labels are the monitor `NodePanel`/`EdgePanel` surfaces; `graph/build.ts` keeps its two local `?.name ?? id` subtitles (Task 8 file); the Requests page makes one extra `listGatewayKeys()` call on mount for the key filter; rename/edit behaviour is verified only by TypeScript, Vitest SSR/jsdom DOM tests and the build (no browser visual test, no live console run); `AGENTS.md`'s name-based Requests key filter and "gateway-key PUT changes only enabled" lines remain for Task 10.

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

- [x] **Step 1: Write the failing example-config test**

```go
func TestExampleConfigLoads(t *testing.T) {
    // 1. copy ../../example.yaml to t.TempDir() so Load's migration cannot rewrite the repo file
    // 2. decode the raw copy with yaml.KnownFields(true) into config.Config (no migration):
    //    assert every entity has a non-empty ID and every reference is a valid id
    // 3. config.Load(copy) succeeds and the copy is unchanged byte for byte
}
```

- [x] **Step 2: Run it to verify it fails**

Run: `GOWORK=off go test ./internal/config -run TestExampleConfigLoads -count=1`
Expected: FAIL (the example still lists name references and no ids at step 2 of the assertion).

- [x] **Step 3: Update `example.yaml`, README and AGENTS.md**

Add ids and id references to the example; document that ids are immutable, names are display labels, the loader migrates old files with a `.bak` copy, a read-only/hand-maintained config must keep its `id` lines when renaming, and stats migrate once with a `.bak`. Amend the AGENTS.md invariants exactly as listed in the spec §8 (load order, name rules, `node.key` by id, id-based admin paths and key updates, stats graph ids and `?key=`, web ids). Record that this is a breaking change shipped as `v0.2.0`; the version string itself comes from release ldflags, so no code change is needed.

- [x] **Step 4: Run the example test and the whole gate**

Run: `GOWORK=off go test ./internal/config -run TestExampleConfigLoads -count=1`
Run: `GOWORK=off go build ./... && GOWORK=off go vet ./... && GOWORK=off go test ./... -count=1`
Expected: PASS.

- [x] **Step 5: Run the web gate**

Run: `cd web && npm test && npx tsc --noEmit && npm run build`
Expected: PASS.

- [x] **Step 6: Commit**

```bash
git add README.md example.yaml AGENTS.md internal/config
git commit -m "docs: document entity ids and the v2 migration"
```

**Implementation record**
- Date/status: 2026-11-10 — DONE (docs, fixture and example test in `e2144d18`).
- Repository + commit: `github.com/ginuerzh/cuttle` @ `e2144d18` (`docs: document entity ids and the v2 migration`); this record is added by the follow-up plan commit. No sibling repository changed.
- Files changed: `README.md` (new "Identity: ids and names" section; `v0.2.0` breaking-release note; id-carrying minimal config; `id` and id-reference rows in every entity table; gateway-key id paths and rename-on-`PUT`; id-addressed provider-key `keyUpdates`; stats v2 one-time migration with `<path>.v1.bak-<unix>`, `entity_labels`, the name-that-looks-like-an-id guard; admin API id paths, id-valued request filters, adopt-or-mint and the rejected provider-key id; Docker read-only caveat; `0600` write normalization), `example.yaml` (ids on every entity plus the commented OpenRouter and gateway-key examples, `node.key` pinned to the provider-key id, comments explaining ids vs names), `AGENTS.md` (`Load` order with migration/write-back, identity and name rules, `node.key` by id, admin id paths, id-addressed `keyUpdates`, gateway-key `PUT` rename, adopt-or-mint and rejected key id, stats v2/graph/`?key=` ids, web id invariants, `internal/identity` row, migration gotchas), `internal/config/config_test.go` (`TestExampleConfigLoads` rewritten: a raw `yaml.KnownFields(true)` decode of a temp copy without migration asserts every entity id and every id reference, then `config.Load` succeeds with the file byte-identical and no `.bak` file created), `docs/superpowers/plans/2026-11-10-entity-ids.md` (all Task 1–10 steps ticked).
- Tests/commands and results: RED `GOWORK=off go test ./internal/config -run TestExampleConfigLoads -count=1` → `config_test.go:1363: provider "deepseek": id = "", want a provider id` against the id-less example. GREEN the same command after the fixture update. Gate: `GOWORK=off go build ./...`, `GOWORK=off go vet ./...`, `GOWORK=off go test ./... -count=1` all pass (14 packages ok); `gofmt -l internal/config` clean. Web gate: `cd web && npm test` → 41 files / 287 tests passed, `npx tsc --noEmit` clean, `npm run build` → `internal/admin/dist` rebuilt.
- Review findings and fixes: the example now pins `node.key` to the provider-key id instead of leaving that reference only in a comment, so the test exercises `node.key` and no longer depends on a commented line staying in sync; the test also asserts that a load creates no `.bak` file, so a future id-less example fails loudly rather than silently rewriting the fixture; corrected stale AGENTS lines that still said route plans are cached by route name and that the authenticated key *name* travels in the request context; clarified that name adoption applies to an entity re-added in one `PUT /api/config` and that a real `DELETE` followed by a create mints a new id.
- Deferred items/limitations: docs-only task, so the `v0.2.0` version string is not set here — release tags and their ldflags own it; the docs record that the loader's write-back loses YAML comments and formatting (with the `.bak` copy as the only recovery) rather than preserving them; no console change, so there is no new UI behavior to verify; the `.superpowers` task reports stay untracked by design.
