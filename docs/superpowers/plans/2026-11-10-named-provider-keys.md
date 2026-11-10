# Named Provider Keys and Node Pinning Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give every provider key a name and let a node optionally pin one key by name, so a route or model can be isolated to a specific upstream account.

**Architecture:** `provider.keys` becomes a list of `{name, key, enabled}`. A node's optional `key` field names one of its provider's keys. The gateway builds one x node per enabled key (or exactly the pinned key), the admin API manages keys by name, and the console edits them without ever receiving a secret.

**Tech Stack:** Go (`gopkg.in/yaml.v3`, `net/http`), React + TypeScript + Ant Design (Vite), Vitest.

**Spec:** `docs/superpowers/specs/2026-11-10-named-provider-keys-design.md`

## Global Constraints

- Run every Go command with `GOWORK=off`; the module uses published tags and has no `replace`.
- `config.Load` uses `yaml.KnownFields(true)`; the key shape is a hard cut with no compatibility path.
- x node names stay `<route>/<provider>/<node>[/<key>]`; `stats.parseNodeName` reads parts[2] as the node.
- Provider key values are write-only through the admin API. Never return, log or store them anywhere else.
- Key names must not contain `/`, `$` or control characters, and are at most 64 runes.
- Console source strings are Chinese; add both the Chinese string and its `en-US` translation.
- Rebuild the web bundle (`cd web && npm run build`) before any Go build that embeds `internal/admin/dist`.
- Commit after every task. Never push unless explicitly asked.
- Keep the plan's per-task **Implementation record** filled in and committed with the task.

## Review Focus

These five inputs/failure modes are easy to get wrong; each task below pins them with a test.

1. A key name that looks like an index (`1`, `02`) must not confuse `stats.parseNodeName`; the node is still parts[2] (Task 2).
2. A provider key value containing `$` must fail validation, because `config.Load` expands the environment on every reload (Task 1).
3. A node pin resolves only against the node's own provider; a same-named key on another provider must not satisfy it (Task 1).
4. A GET then PUT of the masked provider view must not wipe stored secrets; an empty `key` with an existing name keeps the stored value (Task 4).
5. A pinned-but-disabled key must stay a valid config (so an operator can disable a key without breaking the reload) and skip the node at build time (Task 1 validation, Task 2 build).

---

### Task 1: Config schema and validation

**Files:**
- Modify: `internal/config/config.go`
- Modify (mechanical): `internal/provider/models.go`, `internal/provider/usage.go`, `internal/admin/handlers.go`, `internal/admin/handlers_usage.go`, `internal/admin/probe.go`, `internal/gateway/build.go`
- Modify (mechanical): every test that constructs `Provider.Keys` as `[]string` — the compiler lists them
- Test: `internal/config/config_test.go`

**Interfaces:**
- Produces:
  - `type ProviderKey struct { Name string; Key string; Enabled *bool }` with YAML/JSON tags `name`, `key`, `enabled`.
  - `func (k ProviderKey) EnabledValue() bool`
  - `Provider.Keys []ProviderKey`
  - `Node.Key string` with YAML/JSON tag `key`
  - `func validateProviderKeys(providerName string, keys []ProviderKey) error`
  - `func nextProviderKeyName(keys []ProviderKey) string` (temporary; removed in Task 4)

- [ ] **Step 1: Write the failing tests** in `internal/config/config_test.go`

Add these tests (reuse the existing `writeConfig` helper):

- `TestProviderKeysNamedShape`: load a config whose provider has
  `keys: [{name: account-a, key: "${TEST_KEY}"}]` with `TEST_KEY=sk-test`.
  Assert `Keys[0].Name == "account-a"`, `Keys[0].Key == "sk-test"`, and
  `Keys[0].EnabledValue()` is true.
- `TestProviderKeysScalarRejected`: `keys: ["sk-a"]` makes `Load` return a
  parse error.
- `TestProviderKeyNameValidation`: table-driven over `""`, a 65-rune name, a
  duplicate name, `"a/b"`, `"a$b"`, and a name containing `"\n"`; each makes
  `Validate` fail with an error that names the provider.
- `TestProviderKeyValueValidation`: table-driven over `""`, a key containing
  `"$"`, and a key containing `"\n"`; each makes `Validate` fail.
- `TestProviderKeyEnabledValue`: nil is true, an explicit false is false.
- `TestNodeKeyPinResolves`: a node with `key: account-a` on a provider that
  has `account-a` passes `Validate`.
- `TestNodeKeyPinUnknownFails`: a node with `key: nope` fails with an error
  that names the node and `nope`.
- `TestNodeKeyPinOtherProviderFails`: provider A has `account-a`, provider B
  has `account-b`; a node on B with `key: account-a` fails.
- Update `TestLoadExpandsEnvAndValidates` to the named shape and to assert
  `c.Providers[0].Keys[0].Key`.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `GOWORK=off go test ./internal/config -run 'TestProviderKey|TestNodeKey|TestLoadExpandsEnv' -count=1`
Expected: FAIL with undefined `ProviderKey`.

- [ ] **Step 3: Implement the schema**

In `internal/config/config.go`:
- Add `ProviderKey` and `EnabledValue`.
- Change `Provider.Keys` to `[]ProviderKey` and add `Node.Key`.
- `applyDefaults`: trim `Keys[i].Name` and `Keys[i].Key`.
- `expandEnv`: expand `Keys[i].Name` and `Keys[i].Key`.
- Replace the provider-key loop in `Validate` with `validateProviderKeys`.
- In the node loop, when `n.Key != ""`, require a key with that name on
  `n.Provider`; the error names the node and the key. A pinned-but-disabled
  key is valid.
- Add `validateProviderKeys` (name required, trimmed, at most 64 runes,
  unique, no `/`/`$`/control; key non-empty, no `$`/control) and
  `nextProviderKeyName` (`key-N` skipping existing names).

- [ ] **Step 4: Update the mechanical call sites so the repo compiles**

Keep behavior unchanged for now:
- `internal/provider/models.go`: `key = p.cfg.Keys[0].Key`.
- `internal/provider/usage.go`: iterate `for i, k := range p.cfg.Keys` and use
  `k.Key`; keep `KeyIndex: i`.
- `internal/admin/handlers.go`: `viewProvider` builds `KeyPreviews` from
  `p.Keys[i].Key`; `applyProviderKeyUpdates` takes and returns
  `[]config.ProviderKey`, deletes by index, and appends with
  `nextProviderKeyName`.
- `internal/admin/handlers_usage.go`: `preview(cfg.Keys[acc.KeyIndex].Key)`.
- `internal/admin/probe.go`: `key = p.Keys[0].Key`.
- `internal/gateway/build.go`: iterate `for i, k := range keys` and use
  `k.Key`; keep the `/i` name suffix.
- In every test with `Keys: []string{...}`, write
  `Keys: []gwconfig.ProviderKey{{Name: "key-1", Key: ...}, ...}`.

- [ ] **Step 5: Run the full Go gate**

Run: `GOWORK=off go build ./... && GOWORK=off go vet ./... && GOWORK=off go test ./... -count=1`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/config internal/provider internal/admin internal/gateway
git commit -m "feat(config): name provider keys and add node key pin"
```

**Implementation record:** (fill after completion)
- Date/status: 2026-11-10 · complete
- Repository + commit: cuttle `0408eb45` — `feat(config): name provider keys and add node key pin`
- Files changed: `internal/config/config.go` (`ProviderKey`, `Node.Key`, validation, defaults, expandEnv); mechanical call-site updates in `internal/provider/{models,usage}.go`, `internal/admin/{handlers,handlers_usage,probe}.go`, `internal/gateway/build.go`; `example.yaml`; tests (`internal/config/config_test.go` and every `Keys: []string` fixture, plus the JSON `"keys":[...]` payloads in admin/gateway tests).
- Tests/commands + results: `GOWORK=off go build ./...` PASS; `GOWORK=off go vet ./...` PASS; `GOWORK=off go test ./... -count=1` all packages PASS.
- Review findings + fixes: no reviewer at task level (native execution). Self-review caught an intermediate bad edit that removed `ModelOverride.Disabled`; restored before the gate. Moved `nextProviderKeyName` to `internal/admin` (unexported helper cannot cross packages).
- Deferred/limitations: the admin view is still `keysSet`/`keyPreviews` and `keyUpdates` still uses indices with auto-generated `key-N` names; Task 4 replaces both. Catalog/usage/probe still use the first key; Task 3 changes that.

---

### Task 2: Gateway build

**Files:**
- Modify: `internal/gateway/build.go`
- Modify: `internal/stats/recorder.go` (comment only)
- Modify: `internal/gateway/provider_capabilities_test.go` (the two `buildNodes` call sites)
- Test: `internal/gateway/node_keys_test.go` (new), `internal/stats/recorder_test.go`

**Interfaces:**
- Consumes: `config.ProviderKey`, `Provider.Keys`, `Node.Key`.
- Produces:
  - `var errNodeKeyDisabled = errors.New("node has no enabled key")`
  - `func resolveNodeKeys(p gwconfig.Provider, n gwconfig.Node) ([]gwconfig.ProviderKey, error)`
  - `nodePlan` gains `keys []gwconfig.ProviderKey`
  - `buildNodes(p gwconfig.Provider, n gwconfig.Node, keys []gwconfig.ProviderKey, route string, target upstreamTarget, rewriter string) ([]*xconfig.NodeConfig, error)`

- [ ] **Step 1: Write the failing tests**

In `internal/gateway/node_keys_test.go`:
- `TestResolveNodeKeysPinned`: provider keys `account-a`/`account-b`; node
  `key: account-a` returns exactly `[account-a]`.
- `TestResolveNodeKeysUnpinnedSkipsDisabled`: keys `a` (enabled), `b`
  (disabled), `c` (enabled), no pin, returns `[a, c]`.
- `TestResolveNodeKeysAllDisabled`: no pin, every key disabled,
  `errors.Is(err, errNodeKeyDisabled)`.
- `TestResolveNodeKeysPinnedDisabled`: `errors.Is(err, errNodeKeyDisabled)`.
- `TestResolveNodeKeysPinnedMissing`: a non-sentinel error naming the key.
- `TestResolveNodeKeysKeyless`: no keys returns one entry with an empty `Key`.
- `TestBuildNodesSuffixUsesKeyName`: unpinned with two enabled keys produces
  the names `route/p/n/account-a` and `route/p/n/account-b`.
- `TestBuildNodesSingleKeyHasNoSuffix`: a pinned node is named `route/p/n`.
- `TestPinnedNodeUsesOnlyPinnedKey`: an `httptest` upstream records the
  `Authorization` header; provider keys `sk-a`/`sk-b`, node pinned to `b`;
  one request reaches the upstream with `Bearer sk-b`.
- `TestUnpinnedNodeRotatesKeys`: two requests reach the upstream with `sk-a`
  and `sk-b` (round-robin).
- `TestDisabledPinnedKeySkipsNode`: pinned disabled key, request returns 5xx,
  upstream hits stay 0 (mirror `TestDisabledTargetModelSkipsNode`).

In `internal/stats/recorder_test.go`:
- `TestParseNodeNameIgnoresKeySegment`: `parseNodeName("route/provider/node/account-a")`
  and `parseNodeName("route/provider/node/1")` both return node `node`.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `GOWORK=off go test ./internal/gateway ./internal/stats -run 'TestResolveNodeKeys|TestBuildNodes|TestPinnedNode|TestUnpinnedNode|TestDisabledPinned|TestParseNodeName' -count=1`
Expected: FAIL (undefined `resolveNodeKeys`, wrong `buildNodes` signature).

- [ ] **Step 3: Implement**

In `internal/gateway/build.go`:
- Add `errNodeKeyDisabled` and `resolveNodeKeys` (pinned → exactly that key; missing
  → error; disabled → sentinel; unpinned → enabled keys; no keys → one empty
  `ProviderKey`; all disabled → sentinel).
- Add `keys` to `nodePlan`. In `buildRoutePlan`, resolve once per node after
  `compileNodeTargets`; on `errors.Is(err, errNodeKeyDisabled)` log a warning
  and `continue`; any other error fails the build.
- Change `buildNodes` to accept `keys []gwconfig.ProviderKey`, use `k.Key`,
  and suffix the node name with `k.Name` only when `len(keys) > 1`.
- Pass `np.keys` from `buildVariantHop`.

In `internal/stats/recorder.go`: change the `parseNodeName` comment to
`<route>/<provider>/<node>[/<key>]`.

Update the two `buildNodes` calls in `provider_capabilities_test.go` to pass
`keys` (use `resolveNodeKeys(p, n)` or a literal `[]gwconfig.ProviderKey`).

- [ ] **Step 4: Run the focused tests**

Run: `GOWORK=off go test ./internal/gateway ./internal/stats -count=1`
Expected: PASS.

- [ ] **Step 5: Run the full Go gate**

Run: `GOWORK=off go test ./... -count=1`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/gateway internal/stats
git commit -m "feat(gateway): pin nodes to a named provider key"
```

**Implementation record:** (fill after completion)
- Date/status: 2026-11-10 · complete
- Repository + commit: cuttle `3ca4924b` — `feat(gateway): pin nodes to a named provider key`
- Files changed: `internal/gateway/build.go` (`resolveNodeKeys`, `errNodeKeyDisabled`, `nodePlan.keys`, `buildNodes` takes the resolved keys and suffixes with the key name); `internal/stats/recorder.go` (comment); `internal/gateway/node_keys_test.go` (new); `internal/gateway/provider_capabilities_test.go` (`buildNodes` call sites); `internal/stats/recorder_test.go`.
- Tests/commands + results: `GOWORK=off go test ./internal/gateway ./internal/stats -count=1` PASS; `GOWORK=off go build ./... && GOWORK=off go vet ./... && GOWORK=off go test ./... -count=1` PASS.
- Review findings + fixes: the new `authUpstream` test helper collided with `auth_test.go`; renamed to `namedKeyUpstream`. `TestParseNodeNameIgnoresKeySegment` pins existing behavior, so it passed before the implementation; it is a regression guard for Review Focus item 1.
- Deferred/limitations: per-key request attribution is still not recorded; the key name lives only in the x node name and `parseNodeName` strips it (per the spec's non-goals).

---

### Task 3: Control-plane key selection and usage names

**Files:**
- Modify: `internal/config/config.go` (add `FirstEnabledKey`)
- Modify: `internal/provider/models.go`
- Modify: `internal/provider/usage.go`
- Modify: `internal/admin/probe.go`
- Test: `internal/provider/models_test.go`, `internal/provider/usage_test.go`, `internal/admin/probe_test.go` (new)

**Interfaces:**
- Produces:
  - `func (p Provider) FirstEnabledKey() (string, bool)`
  - `AccountUsage.Name string` with JSON tag `name`

- [ ] **Step 1: Write the failing tests**

- `internal/provider/models_test.go`, `TestFetchModelsUsesFirstEnabledKey`:
  provider keys `a` (disabled, `sk-a`) and `b` (enabled, `sk-b`); the catalog
  request carries `Authorization: Bearer sk-b`.
- `internal/provider/usage_test.go`, `TestUsageSetsKeyNames`: `Usage` returns
  one account per key, in order, each with `Name` equal to the configured
  name.
- `internal/admin/probe_test.go`, `TestProbeUsesFirstEnabledKey`: probe with
  keys `a` (disabled) and `b` (enabled); the probe request carries `sk-b`.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `GOWORK=off go test ./internal/provider ./internal/admin -run 'TestFetchModelsUsesFirstEnabledKey|TestUsageSetsKeyNames|TestProbeUsesFirstEnabledKey' -count=1`
Expected: FAIL.

- [ ] **Step 3: Implement**

- Add `func (p Provider) FirstEnabledKey() (string, bool)` in
  `internal/config/config.go` (first key whose `EnabledValue()` is true).
- `internal/provider/models.go`: use `FirstEnabledKey()`; keep the
  `CatalogKeyless()` override.
- `internal/admin/probe.go`: use `FirstEnabledKey()`; keep the `keyless`
  override at the existing call site.
- `internal/provider/usage.go`: add `Name` to `AccountUsage`; set
  `Name: k.Name` while iterating the keys.

- [ ] **Step 4: Run the focused tests, then the full Go gate**

Run: `GOWORK=off go test ./internal/provider ./internal/admin -count=1 && GOWORK=off go test ./... -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config internal/provider internal/admin
git commit -m "fix(provider): use the first enabled key and name usage accounts"
```

**Implementation record:** (fill after completion)
- Date/status: 2026-11-10 · complete
- Repository + commit: cuttle `566b090e` — `fix(provider): use the first enabled key and name usage accounts`
- Files changed: `internal/config/config.go` (`Provider.FirstEnabledKey`); `internal/provider/models.go`; `internal/provider/usage.go` (`AccountUsage.Name`); `internal/admin/probe.go`; tests in `internal/provider/models_test.go`, `internal/provider/usage_test.go`, `internal/admin/probe_test.go`.
- Tests/commands + results: `GOWORK=off go test ./internal/provider ./internal/admin -count=1` PASS; `GOWORK=off go build ./... && GOWORK=off go vet ./... && GOWORK=off go test ./... -count=1` PASS.
- Review findings + fixes: none.
- Deferred/limitations: the admin usage view does not yet carry `name`/`enabled`; Task 4 adds it.

---

### Task 4: Admin provider key API

**Files:**
- Modify: `internal/admin/handlers.go`
- Modify: `internal/admin/handlers_usage.go`
- Test: `internal/admin/handlers_provider_keys_test.go` (new)

**Interfaces:**
- Consumes: `config.ProviderKey`, `ProviderKey.EnabledValue()`, `AccountUsage.Name`.
- Produces:
  - `type providerKeyView struct { Name string; KeyPreview string; Enabled bool }`
  - `providerView.Keys []providerKeyView` (replaces `KeysSet`/`KeyPreviews`)
  - `type providerKeyUpdate struct { Name string; Key string; Enabled *bool; Delete bool }`
  - `func applyProviderKeyUpdates(keys []config.ProviderKey, updates []providerKeyUpdate) ([]config.ProviderKey, error)`
  - `func mergeProviderKeys(old, incoming []config.ProviderKey) ([]config.ProviderKey, error)`
  - `func ensureKeysNotPinned(c *config.Config, providerName string, keys []config.ProviderKey) error`
  - `accountUsageView.Name string`, `accountUsageView.Enabled bool`

- [ ] **Step 1: Write the failing tests** in `internal/admin/handlers_provider_keys_test.go`

Use the existing admin test harness (`server_test.go`) for login and JSON calls.
- `TestProviderViewReturnsNamedMaskedKeys`: GET the provider and `/api/config`;
  `keys` is `[{name, keyPreview, enabled}]` and no raw key appears.
- `TestKeyUpdatesAppendByName`: `{name: c, key: sk-c}` appends; names become
  `[a, b, c]`.
- `TestKeyUpdatesDuplicateNameRejected`: appending an existing name is 400.
- `TestKeyUpdatesDeleteByName`.
- `TestKeyUpdatesDeletePinnedKeyRejected`: a node on the same provider pins
  `a`; deleting `a` is 400 and names the node.
- `TestKeyUpdatesDeletePinnedKeyOtherProviderAllowed`: a node on another
  provider pins `a`; deleting this provider's `a` is 204.
- `TestKeyUpdatesToggleEnabled`: `{name: a, enabled: false}` disables `a`.
- `TestKeyUpdatesDeleteAndReaddSameName`: one request deletes `a` and appends
  `{name: a, key: sk-new}`; the result is one `a` with the new value.
- `TestKeyUpdatesMissingNameRejected`: `{name: nope, delete: true}` is 400.
- `TestMergeProviderKeys`: nil keeps old; `[]` clears; a non-empty list
  replaces by name; an empty `key` with an existing name keeps the old value;
  an empty `key` with a new name errors.
- `TestPutConfigPreservesProviderKeys`: GET `/api/config`, PUT the same body,
  assert the stored keys and values are unchanged.
- `TestUsageViewIncludesNameAndEnabled`: the usage response accounts carry the
  configured name and enabled flag.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `GOWORK=off go test ./internal/admin -run 'TestProviderViewReturnsNamedMaskedKeys|TestKeyUpdates|TestMergeProviderKeys|TestPutConfigPreservesProviderKeys|TestUsageViewIncludesNameAndEnabled' -count=1`
Expected: FAIL.

- [ ] **Step 3: Implement**

In `internal/admin/handlers.go`:
- Add `providerKeyView`; `viewProvider` returns it and masks values with the
  existing `preview`.
- Replace `providerKeyUpdate` with the name-based struct.
- Rewrite `applyProviderKeyUpdates`: validate names, apply deletes first,
  then `enabled` sets, then appends; a duplicate name is checked against the
  post-delete set; a delete of a missing name errors.
- Add `mergeProviderKeys` and use it in `handleUpdateProvider` (the
  no-`keyUpdates` branch) and `handlePutConfig`.
- Add `ensureKeysNotPinned` (scan `c.Nodes` for `n.Provider == providerName &&
  n.Key != ""` and reject a removed name) and call it after key mutations in
  both update paths.
- Remove `nextProviderKeyName`.

In `internal/admin/handlers_usage.go`: add `Name` and `Enabled` to
`accountUsageView`; fill them from `cfg.Keys[acc.KeyIndex]`.

- [ ] **Step 4: Run the focused tests, then the full Go gate**

Run: `GOWORK=off go test ./internal/admin -count=1 && GOWORK=off go test ./... -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/admin
git commit -m "feat(admin): manage named provider keys"
```

**Implementation record:** (fill after completion)
- Date/status: 2026-11-10 · complete
- Repository + commit: cuttle `af01e6eb` — `feat(admin): manage named provider keys`
- Files changed: `internal/admin/handlers.go` (`providerKeyView`, `providerView.Keys`, name-based `providerKeyUpdate`/`applyProviderKeyUpdates`, `mergeProviderKeys`, `ensureKeysNotPinned`, `handleUpdateProvider`, `handlePutConfig`); `internal/admin/handlers_usage.go` (`accountUsageView.Name`/`Enabled`); `internal/admin/handlers_provider_keys_test.go` (new); `internal/admin/server_test.go` (provider view and keyUpdates tests updated).
- Tests/commands + results: focused `go test ./internal/admin -run 'TestProviderViewReturnsNamedMaskedKeys|TestKeyUpdates|TestMergeProviderKeys|TestPutConfigPreservesProviderKeys|TestUsageViewIncludesNameAndEnabled' -count=1` PASS; `go test ./internal/admin -count=1` PASS; full `go build ./... && go vet ./... && go test ./... -count=1` PASS.
- Review findings + fixes: two test assertions corrected — the error body escapes quotes (assert on `is used by node`) and appended keys land after surviving keys by design (assert by name).
- Deferred/limitations: the console bundle still uses the old view shape until Task 5; provider key renaming is still delete+re-add (per the spec).

---

### Task 5: Web provider key management

**Files:**
- Modify: `web/src/api/types.ts`
- Modify: `web/src/pages/presets.ts`
- Modify: `web/src/pages/Edit.tsx` (`ProviderForm`)
- Modify: `web/src/pages/Providers.tsx`
- Test: `web/src/pages/presets.test.ts`, `web/src/pages/Edit.test.tsx`

**Interfaces:**
- Consumes: the Task 4 view `keys: [{name, keyPreview, enabled}]` and name-based `keyUpdates`.
- Produces:
  - `ProviderKeyView { name: string; keyPreview: string; enabled: boolean }`
  - `ProviderKeyInput { name: string; key: string; enabled?: boolean }`
  - `ProviderKeyRow { name: string; preview?: string; value: string; enabled: boolean; existing: boolean }`
  - `providerKeyRows(keys: ProviderKeyView[]): ProviderKeyRow[]`
  - `providerKeyUpdates(rows: ProviderKeyRow[], deleted: string[]): ProviderKeyUpdate[]`

- [ ] **Step 1: Write the failing tests**

In `presets.test.ts`:
- `providerKeyRows` maps named view keys to rows with name, preview and
  enabled.
- `providerKeyUpdates` emits `{name, delete: true}` for deleted existing
  rows, `{name, key}` for new rows, and `{name, enabled}` for a changed
  switch.

In `Edit.test.tsx`:
- The provider form renders one row per named key with a name, preview, enable
  switch and delete button.
- The add row requires a name; saving sends name-based `keyUpdates`.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd web && npx vitest run src/pages/presets.test.ts src/pages/Edit.test.tsx`
Expected: FAIL.

- [ ] **Step 3: Implement**

- `types.ts`: replace `keysSet`/`keyPreviews` with `keys: ProviderKeyView[]`;
  add `ProviderKeyInput`; keep the raw input type separate from the view.
- `presets.ts`: rewrite `providerKeyRows`/`providerKeyUpdates` for names;
  `providerPayload` sends named keys on create.
- `Edit.tsx` `ProviderForm`: named rows with an enabled switch, delete, and an
  add row that takes a name and a value.
- `Providers.tsx`: show the key names or count.
- Add the Chinese strings and their `en-US` translations.

- [ ] **Step 4: Run the web gate and rebuild the bundle**

Run: `cd web && npm test && npx tsc --noEmit && npm run build`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src
git commit -m "feat(web): manage named provider keys"
```

**Implementation record:** (fill after completion)
- Date/status: 2026-11-10 · complete
- Repository + commit: cuttle `97c4a7d4` — `feat(web): manage named provider keys`
- Files changed: `web/src/api/types.ts` (`ProviderKeyView`, `ProviderKeyInput`, `Provider.keys`); `web/src/api/client.ts` (`ProviderInput`); `web/src/pages/presets.ts` (name-based key rows/updates, `providerPayload` drops keys); `web/src/pages/Edit.tsx` (`ProviderKeyEditor`, `ProviderForm`, `KeyField`, `AddProviderModal`); `web/src/pages/Providers.tsx`; `web/src/graph/build.ts`; `web/src/locales/en-US.ts`; tests in `presets.test.ts`, `Edit.test.tsx`, `graph/build.test.ts`, `pages/selection.test.ts`.
- Tests/commands + results: `cd web && npx tsc --noEmit` PASS; `npm test` 236/236 PASS; `npm run build` PASS; `GOWORK=off go build ./... && GOWORK=off go test ./... -count=1` PASS.
- Review findings + fixes: the `keysSet` fixtures in `graph/build.ts`, `graph/build.test.ts` and `selection.test.ts` moved to `keys`; the create modal now requires a name and a value per key.
- Deferred/limitations: provider key renaming is still delete+re-add (per the spec); the topology label shows `keys×N`, not key names.

---

### Task 6: Web node pin and usage display

**Files:**
- Modify: `web/src/api/types.ts` (`Node.key`, `AccountUsage.name`)
- Modify: `web/src/pages/Edit.tsx` (`NodeForm`)
- Modify: `web/src/pages/Nodes.tsx`
- Modify: `web/src/pages/Usage.tsx`
- Test: `web/src/pages/Edit.test.tsx`, `web/src/pages/Usage.test.ts`

**Interfaces:**
- Consumes: `Node.key`, `AccountUsage.name`.
- Produces: the node key `Select`, the pinned-key tag, and the usage account name.

- [ ] **Step 1: Write the failing tests**

In `Edit.test.tsx`:
- `NodeForm` renders a key `Select` with "全部 key" plus every named key of
  the node's provider; saving sends `key`.

In `Usage.test.ts`:
- An account row shows the key name beside the masked preview.

In the `Nodes.tsx` test file (create one if absent):
- A node with `key: account-a` shows `account-a` on its card.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd web && npx vitest run src/pages/Edit.test.tsx src/pages/Usage.test.ts src/pages/Nodes.test.tsx`
Expected: FAIL (the last file may not exist yet; create it).

- [ ] **Step 3: Implement**

- `types.ts`: add `key?: string` to `Node` and `name?: string` to
  `AccountUsage`.
- `Edit.tsx` `NodeForm`: add the key `Select` (options: "全部 key" plus each
  `config.providers[i].keys[j].name` with its preview); send `key` (empty
  when "全部 key").
- `Nodes.tsx`: show the pinned key name as a tag.
- `Usage.tsx`: show `name` beside the preview.
- Add the Chinese strings and their `en-US` translations.

- [ ] **Step 4: Run the web gate, rebuild the bundle, then the Go gate**

Run: `cd web && npm test && npx tsc --noEmit && npm run build`
Run: `GOWORK=off go build ./... && GOWORK=off go test ./... -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src
git commit -m "feat(web): pin a node to a provider key"
```

**Implementation record:** (fill after completion)
- Date/status: 2026-11-10 · complete
- Repository + commit: cuttle `7d88e7dd` — `feat(web): pin a node to a provider key`
- Files changed: `web/src/api/types.ts` (`Node.key`, `AccountUsage.name`/`enabled`); `web/src/pages/presets.ts` (`nodeKeyOptions`); `web/src/pages/Edit.tsx` (`NodeForm` key select + save); `web/src/pages/Nodes.tsx` (pinned-key tag); `web/src/pages/Usage.tsx` (name/disabled tags); `web/src/locales/en-US.ts`; tests in `Edit.test.tsx`, `Usage.test.ts`, `Nodes.test.tsx` (new), `presets.test.ts`.
- Tests/commands + results: focused `npx vitest run ...` PASS; `npx tsc --noEmit` PASS; `npm test` 240/240 PASS; `npm run build` PASS; `GOWORK=off go build ./... && GOWORK=off go test ./... -count=1` PASS.
- Review findings + fixes: `NodeForm` falls back to `node.provider` when `Form.useWatch` is empty, so the key options render on the first pass and under SSR.
- Deferred/limitations: per-key request stats are still not recorded (spec non-goal); a node pins at most one key.

---

### Task 7: Docs

**Files:**
- Modify: `README.md`
- Modify: `example.yaml`
- Modify: `AGENTS.md`

- [ ] **Step 1: Update `example.yaml`**

Change the provider `keys` to the named shape:
`keys: [{name: default, key: "${DEEPSEEK_API_KEY}"}]`. Update the commented
OpenRouter example the same way. Add a commented `# key: default` under one
node with a one-line explanation.

- [ ] **Step 2: Update `README.md`**

Cover: named provider keys, `{name, key, enabled}`, `node.key` pinning,
unpinned nodes spread across all enabled keys, disabled keys, and the admin
API's name-based key management.

- [ ] **Step 3: Update `AGENTS.md`**

Update the configuration invariants (named keys, validation rules, pin
resolution), the admin API invariants (view shape, name-based `keyUpdates`,
the full-replace merge rule, the pinned-key delete guard), and the web
invariants (named key rows, node pin select).

- [ ] **Step 4: Verify the example config still loads**

Run: `GOWORK=off go test ./internal/config -run TestExampleConfigLoads -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add README.md example.yaml AGENTS.md
git commit -m "docs: document named provider keys and node pinning"
```

**Implementation record:** (fill after completion)
- Date/status: 2026-11-10 · complete
- Repository + commit: cuttle `ba1cab32` — `docs: document named provider keys and node pinning`
- Files changed: `README.md` (feature list, routing model, minimal config, `providers[]`, `nodes[]`, admin provider-key flow), `example.yaml` (named keys, commented `node.key`), `AGENTS.md` (config/admin/web invariants).
- Tests/commands + results: `GOWORK=off go test ./internal/config -run TestExampleConfigLoads -count=1` PASS.
- Review findings + fixes: none.
- Deferred/limitations: none.

---

### Task 8: k3s rollout

**Files:** none (operational).

**Dependency:** the code must be pushed and the new image built by CI first.
Do not push without explicit permission.

- [ ] **Step 1: Back up the live config and copy it out**

```bash
kubectl --context pi -n llm exec deploy/llm-gw -- \
  cp /data/cuttle.yaml /data/cuttle.yaml.bak-20261110-pre-named-keys
kubectl --context pi -n llm exec deploy/llm-gw -- cat /data/cuttle.yaml > /tmp/cuttle.yaml
```

- [ ] **Step 2: Add a name to every provider key**

Edit `/tmp/cuttle.yaml`: each provider's scalar `- <secret>` becomes
`- name: <name>` plus `key: <secret>`. Use a meaningful account name (for
example `default` for the existing single key). Leave node names, models,
routes and all secrets unchanged.

- [ ] **Step 3: Write it back and roll out the new image**

```bash
kubectl --context pi -n llm exec -i deploy/llm-gw -- sh -c 'cat > /data/cuttle.yaml' < /tmp/cuttle.yaml
kubectl --context pi -n llm rollout restart deploy/llm-gw
kubectl --context pi -n llm rollout status deploy/llm-gw
```

- [ ] **Step 4: Verify**

```bash
curl -fsS http://llm-gw.home.pi/api/health
kubectl --context pi -n llm logs deploy/llm-gw --tail=50
```

Assert `{"ok":true,...}` and no config parse error in the logs. On failure,
`kubectl --context pi -n llm rollout undo deploy/llm-gw` and restore
`/data/cuttle.yaml.bak-20261110-pre-named-keys`.

- [ ] **Step 5: Record and commit**

Append the deploy facts (image digest, version, pod, health) to the
`llm-gw-k3s-production` memory note in `/root/code/go-gost/.memory/`, and
commit the workspace memory separately from the cuttle repository.

```bash
cd /root/code/go-gost
git add .memory
git commit -m "docs(memory): record named provider keys deploy"
```

**Implementation record:** (fill after completion)
- Date/status:
- Repository + commit:
- Files changed:
- Tests/commands + results:
- Review findings + fixes:
- Deferred/limitations:

---

## Final review (2026-11-10)

A fresh reviewer read the whole branch (`7bd76325..HEAD`). No Critical issues.
Two Important issues and six Minor findings:

- **Important (fixed):** the provider add-key row lost focus after every keystroke
  because its React key included the editable name (`web/src/pages/Edit.tsx`);
  fixed with a stable key plus a DOM regression test.
- **Important (fixed):** the web interaction/save tests the plan asked for were
  missing; added `web/src/pages/Edit.dom.test.tsx` (add-key payload, partial-row
  guard, focus retention, node pin save).
- **Minor (fixed after re-grading):** a full `keys` replacement could silently
  re-enable a disabled key; `mergeProviderKeys` now preserves the stored
  `enabled` unless the incoming entry sets it.
- **Minor (fixed after re-grading):** `mergeProviderKeys`/`ensureKeysNotPinned`
  now trim names consistently.
- **Minor (fixed):** `AGENTS.md` still said the x node suffix is `<keyIndex>`;
  updated to `<key>`.
- **Minor (deferred):** `NodeForm` does not clear `key` when the provider
  changes; the save then fails with a clear 400.
- **Minor (deferred):** README does not document the key-name character rules or
  the old-scalar-config rejection.

Verification after the fix pass: `GOWORK=off go build ./... && go vet ./... &&
go test ./... -count=1` green; `cd web && npx tsc --noEmit && npm test` (244/244)
and `npm run build` green.
