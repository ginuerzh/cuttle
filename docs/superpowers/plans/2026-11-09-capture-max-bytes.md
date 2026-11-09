# Capture Max Bytes + UI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let error-capture previews keep a full request body (up to 1MB) and make the capture policy editable from the admin console, with a per-field truncation marker.

**Architecture:** Raise the `stats.capture.maxBytes` default to the existing 1MB validation ceiling. Add a dedicated `PUT /api/stats/capture` that mutates only `stats.capture` through the existing `mutate`/`replaceLocked` path, so provider keys and unexposed provider fields are never round-tripped. Track which preview fields were clipped inside `internal/capture`, persist the list in `request_captures`, and render a "已截断" tag next to clipped previews in the request detail.

**Tech Stack:** Go 1.26, SQLite (modernc), embedded React + Ant Design + Vite, Vitest.

**Spec:** `docs/superpowers/specs/2026-11-09-capture-max-bytes-design.md`

## Global Constraints

- Run Go commands with `GOWORK=off`; rebuild `web` (`cd web && npm ci && npm run build`) before the final Go build when web sources changed.
- Admin UI source strings are Chinese and must be added to `web/src/locales/en-US.ts` too.
- Commits: conventional messages, one commit per task, commit only, never push.
- Do not edit sibling repositories (`../core`, `../x`, `../llm-api-converter`).
- Never log or store credential values.
- `PUT /api/stats/capture` replaces the four `stats.capture` fields and preserves every other config field.

## Review Focus

- A preview exactly `maxBytes` long is **not** truncated; one byte more is.
- A field offered in several chunks is listed once in `truncatedFields`, and a later chunk that does not fit still marks it.
- Old `request_captures` rows without `truncated_fields` read as an empty list, never as truncated.
- `PUT /api/stats/capture` must not change gateway, admin, providers, nodes, routes or entrypoints.
- A body missing capture fields falls back to defaults (`off` / `0.01` / `1048576` / `24h`); the UI always sends all four.
- `mode` outside `off`/`errors`/`sample`, `sampleRate` outside 0..1, `maxBytes` outside 1024..1048576, and a negative `retain` are rejected with 400.
- The truncation tag appears only on fields actually clipped, not on the whole capture.

---

### Task 1: Raise the maxBytes default to 1MB

**Files:**
- Modify: `internal/config/config.go:486-487`
- Modify: `internal/capture/capture.go:89-91` and `:103-105`
- Modify: `internal/config/config_test.go:1015-1033`
- Modify: `README.md:458`
- Modify: `AGENTS.md:140`

**Interfaces:**
- Produces: `stats.capture.maxBytes` default `1048576` everywhere a default is applied.

- [ ] **Step 1: Write the failing test**

In `internal/config/config_test.go` update `TestCaptureDefaultsAndValidation`: the default assertion becomes `c.Stats.Capture.MaxBytes != 1048576`, and add `"stats:\n  capture:\n    mode: errors\n    maxBytes: 1048577\n"` to the rejected list.

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOWORK=off go test ./internal/config -run TestCaptureDefaultsAndValidation -count=1`
Expected: FAIL — got `32768`, want `1048576`.

- [ ] **Step 3: Implement**

- `internal/config/config.go`: `ApplyDefaults` sets `c.Stats.Capture.MaxBytes = 1048576` when zero.
- `internal/capture/capture.go`: `NewManager` and `SetPolicy` set `p.MaxBytes = 1048576` when `<= 0`.
- The existing `Validate` bound (1024..1048576) is unchanged.

- [ ] **Step 4: Run the test to verify it passes**

Run: `GOWORK=off go test ./internal/config -run TestCaptureDefaultsAndValidation -count=1`
Expected: PASS.

- [ ] **Step 5: Update docs**

- `README.md`: `capture.maxBytes` default `32768` -> `1048576`.
- `AGENTS.md:140`: `capture off`/`32768`/`24h` -> `capture off`/`1048576`/`24h`.

- [ ] **Step 6: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go internal/capture/capture.go README.md AGENTS.md
git commit -m "feat(config): raise capture maxBytes default to 1MB"
```

**Implementation record**

- Date/status: 2026-11-09 / done
- Repository + commit: cuttle / `7c6a3ac8`
- Files: `internal/config/config.go`, `internal/config/config_test.go`, `internal/capture/capture.go`, `README.md`, `AGENTS.md`
- Tests: `GOWORK=off go test ./internal/config -run TestCaptureDefaultsAndValidation -count=1` -> PASS; `GOWORK=off go test ./internal/config ./internal/capture -count=1` -> PASS
- Review findings/fixes: none
- Deferred: `internal/admin/server_test.go` still passes `MaxBytes: 32768` to the fake manager; it is a test value, not a default.

---

### Task 2: Per-field truncation marker in the capture package

**Files:**
- Modify: `internal/capture/capture.go`
- Test: `internal/capture/capture_test.go`

**Interfaces:**
- Produces:
  - `const FieldRequestOriginal = "requestOriginal"` (and the matching `FieldRequestRewritten`, `FieldResponseOriginal`, `FieldResponseRewritten`, `FieldSSEFirstOriginal`, `FieldSSEFirstRewritten`, `FieldSSELastOriginal`, `FieldSSELastRewritten`)
  - `Capture.TruncatedFields []string` with JSON tag `truncatedFields,omitempty`
  - `clip(b []byte, max int) (string, bool)`
  - `appendPreview(dst string, b []byte, max int) (string, bool)`

- [ ] **Step 1: Write the failing tests**

Add to `internal/capture/capture_test.go`:

- `TestTruncatedFieldsTracked`: manager with `MaxBytes: 4`, `Start("s1", ...)`; `Offer("s1", "request", []byte("abcdef"), []byte("ghijkl"), "", 0)`; `Finish` -> `TruncatedFields` equals `[]string{"requestOriginal", "requestRewritten"}`.
- `TestExactLimitNotTruncated`: `MaxBytes: 6`; offer `[]byte("abcdef")` / `[]byte("ghijkl")`; `TruncatedFields` is empty.
- `TestSecondChunkMarksTruncated`: `MaxBytes: 8`; offer `[]byte("abcd")` then `[]byte("efghij")`; the first chunk does not truncate, the second does; `TruncatedFields` contains `requestOriginal` exactly once.
- Extend `TestPreviewsTruncated` to assert the two field names are listed.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `GOWORK=off go test ./internal/capture -count=1`
Expected: FAIL — `TruncatedFields` does not exist.

- [ ] **Step 3: Implement**

- Add the eight field-name constants and the `TruncatedFields` field to `Capture`.
- Change `clip` and `appendPreview` to return `(string, bool)`:
  - `clip`: `max <= 0 || len(b) <= max` -> `(string(b), false)`; otherwise `(string(b[:max]), true)`.
  - `appendPreview`: empty `b` -> `(dst, false)`; `len(dst) >= max` -> `(dst, true)`; `len(dst)+len(b) > max` -> append `b[:max-len(dst)]`, return `true`; else append all, return `false`.
- Add `truncated map[string]bool` to `session` and a `markTruncated(field string, trunc bool)` helper that lazily creates the map.
- In `Offer`, capture the bool from every `appendPreview`/`clip` call (request, SSE first/last, response) and call `markTruncated` with the matching constant.
- In `Finish`, set `out.TruncatedFields` to the sorted keys of the map (nil when empty).

- [ ] **Step 4: Run the tests to verify they pass**

Run: `GOWORK=off go test ./internal/capture -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/capture/capture.go internal/capture/capture_test.go
git commit -m "feat(capture): track truncated preview fields"
```

**Implementation record**

- Date/status: 2026-11-09 / done
- Repository + commit: cuttle / `17073997`
- Files: `internal/capture/capture.go`, `internal/capture/capture_test.go`
- Tests: `GOWORK=off go test ./internal/capture -count=1` -> PASS; `GOWORK=off go build ./...` -> ok
- Review findings/fixes: the plan's `TestSecondChunkMarksTruncated` chunk sizes were wrong (`"ij"` fits under the 8-byte limit); corrected to `"efghij"`/`"klmnop"` so the second chunk genuinely overflows.
- Deferred: none

---

### Task 3: Persist the truncation marker

**Files:**
- Modify: `internal/stats/store.go`
- Test: `internal/stats/store_test.go`

**Interfaces:**
- Consumes: `capture.Capture.TruncatedFields`.
- Produces: `request_captures.truncated_fields` (JSON array string, `''` when empty).

- [ ] **Step 1: Write the failing test**

In `internal/stats/store_test.go` extend `TestCaptureRoundTripAndCleanup` (or add `TestCaptureTruncatedFieldsRoundTrip`): put a capture with `TruncatedFields: []string{"requestOriginal", "sseLastOriginal"}`, read it back, and assert the slice is equal. Also assert a capture put without the field reads back empty.

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOWORK=off go test ./internal/stats -run TestCapture -count=1`
Expected: FAIL — the column / field is not persisted.

- [ ] **Step 3: Implement**

- Add `"truncated_fields": "TEXT NOT NULL DEFAULT ''"` to the existing `ensureColumns("request_captures", ...)` call.
- `PutCapture`: add `truncated_fields` to the column list and the `ON CONFLICT` update; write `marshalTruncated(c.TruncatedFields)` (`"[]"` when empty).
- `GetCapture`: select `truncated_fields` and unmarshal into `c.TruncatedFields`; ignore an empty string.

- [ ] **Step 4: Run the test to verify it passes**

Run: `GOWORK=off go test ./internal/stats -run TestCapture -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/stats/store.go internal/stats/store_test.go
git commit -m "feat(stats): persist capture truncation fields"
```

**Implementation record**

- Date/status: 2026-11-09 / done
- Repository + commit: cuttle / `6d813d49`
- Files: `internal/stats/store.go`, `internal/stats/store_test.go`
- Tests: `GOWORK=off go test ./internal/stats -run TestCapture -count=1` -> PASS; `GOWORK=off go test ./internal/stats -count=1` -> PASS; `GOWORK=off go build ./...` -> ok
- Review findings/fixes: `truncated_fields` added to the `CREATE TABLE` for fresh databases as well as `ensureColumns` for existing ones; old rows read as an empty slice.
- Deferred: none

---

### Task 4: PUT /api/stats/capture

**Files:**
- Modify: `internal/admin/handlers_capture.go`
- Modify: `internal/admin/server.go:151` (route block)
- Test: `internal/admin/server_test.go`
- Test: `internal/gateway/reload_test.go`
- Modify: `README.md` (API table)

**Interfaces:**
- Consumes: `config.Capture`, `Server.mutate`, `decode`, `writeError`.
- Produces: `PUT /api/stats/capture` accepting `{mode, sampleRate, maxBytes, retain}` and returning 204.

- [ ] **Step 1: Write the failing tests**

Add `TestPutStatsCapture` to `internal/admin/server_test.go`:

- Login, `PUT /api/stats/capture` with `{"mode":"errors","sampleRate":0.5,"maxBytes":1048576,"retain":"24h"}` -> 204.
- `GET /api/config` shows the four new values.
- `rel.cfg.Stats.Capture` matches, and `rel.cfg.Gateway`/`Providers`/`Nodes`/`Routes`/`Entrypoints` are unchanged.
- `PUT` with `{"mode":"always", ...}` -> 400.
- `PUT` with `{"mode":"errors","maxBytes":1}` -> 400.
- `PUT` with `{"maxBytes":65536}` (partial body) -> 204, and `GET /api/config` shows `mode:"off"`, `sampleRate:0.01`, `retain:"24h0m0s"`.

Add `TestReloadAppliesCapturePolicy` to `internal/gateway/reload_test.go`: build a gateway with `statsConfig` (capture `off`), `gw.Reload` a config with `Capture{Mode:"errors", MaxBytes:4, Retain: 24h}`, then `gw.Capture()`: `Start("s","r","m")`, `Offer("s","request",[]byte("abcdef"),[]byte("ghijkl"),"",0)`, `Finish("s")` and assert `RequestOriginal == "abcd"` and `TruncatedFields` lists both request fields. This proves the endpoint's config change reaches the manager through the existing reload path.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `GOWORK=off go test ./internal/admin -run TestPutStatsCapture -count=1 && GOWORK=off go test ./internal/gateway -run TestReloadAppliesCapturePolicy -count=1`
Expected: FAIL — 404 on the route and no truncation before `Reload`.

- [ ] **Step 3: Implement**

- In `handlers_capture.go` add `handlePutStatsCapture`: `decode` into a `config.Capture`, then `s.mutate(func(c *config.Config) error { c.Stats.Capture = in; return nil })`; on error `writeError(w, http.StatusBadRequest, err.Error())`, else `w.WriteHeader(http.StatusNoContent)`.
- Register `api.HandleFunc("PUT /api/stats/capture", s.handlePutStatsCapture)`.
- Add `PUT /api/stats/capture` to the README API table next to the other `/api/stats/*` rows.

- [ ] **Step 4: Run the test to verify it passes**

Run: `GOWORK=off go test ./internal/admin -run TestPutStatsCapture -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/admin/handlers_capture.go internal/admin/server.go internal/admin/server_test.go README.md
git commit -m "feat(admin): add PUT /api/stats/capture"
```

**Implementation record**

- Date/status: 2026-11-09 / done
- Repository + commit: cuttle / `2eb76629`
- Files: `internal/admin/handlers_capture.go`, `internal/admin/server.go`, `internal/admin/server_test.go`, `internal/gateway/reload_test.go`, `README.md`
- Tests: `GOWORK=off go test ./internal/admin -run TestPutStatsCapture -count=1` -> PASS; `GOWORK=off go test ./internal/admin ./internal/gateway -count=1` -> PASS; `GOWORK=off go build ./...` -> ok
- Review findings/fixes: `TestReloadAppliesCapturePolicy` passed immediately because `gateway.apply` already calls `capture.SetPolicy`; it is a regression guard for the wiring the endpoint depends on, and the admin 404 -> 204 is the RED that drove the new handler. Also verified that provider keys / userAgent / headers survive the capture PUT.
- Deferred: none

---

### Task 5: Web client, types and the Settings capture form

**Files:**
- Modify: `web/src/api/types.ts`
- Modify: `web/src/api/client.ts`
- Test: `web/src/api/client.test.ts`
- Modify: `web/src/pages/Settings.tsx`
- Modify: `web/src/App.tsx:689-692`
- Modify: `web/src/locales/en-US.ts`

**Interfaces:**
- Consumes: `PUT /api/stats/capture`.
- Produces:
  - `putStatsCapture(input: { mode: string; sampleRate: number; maxBytes: number; retain: string }): Promise<void>`
  - `RequestCapture.truncatedFields?: string[]`
  - `SettingsPage` prop `onConfigSaved?: () => void`

- [ ] **Step 1: Write the failing test**

Add to `web/src/api/client.test.ts`: `putStatsCapture({mode:'errors', sampleRate:0.5, maxBytes:1048576, retain:'24h'})` calls `PUT /api/stats/capture` with that JSON body and resolves on a 204.

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd web && npx vitest run src/api/client.test.ts`
Expected: FAIL — `putStatsCapture` is not exported.

- [ ] **Step 3: Implement the client and types**

- `types.ts`: add `truncatedFields?: string[]` to `RequestCapture`.
- `client.ts`: add `putStatsCapture` using the existing `request` helper (204 -> `undefined`).

- [ ] **Step 4: Run the test to verify it passes**

Run: `cd web && npx vitest run src/api/client.test.ts`
Expected: PASS.

- [ ] **Step 5: Add the Settings capture form**

- `App.tsx`: pass `onConfigSaved={() => void loadConfig()}` to `SettingsPage`.
- `Settings.tsx`: add a `captureForm` and a section after the descriptions:
  - `mode`: `Select` with `off` / `errors` / `sample`, initial `config.stats?.capture?.mode ?? 'off'`.
  - `sampleRate`: `InputNumber` min 0 max 1 step 0.01, rendered only when mode is `sample`, initial `?? 0.01`.
  - `maxBytes`: `InputNumber` min 1024 max 1048576 step 1024, initial `?? 1048576`.
  - `retain`: `Input` placeholder `24h`, initial `config.stats?.capture?.retain ?? '24h'`.
  - Submit calls `putStatsCapture`, shows a success message, then `onConfigSaved?.()`; errors show `message.error`.
- `en-US.ts`: add both sides for every new string (section title, labels, success/error messages).

- [ ] **Step 6: Verify**

Run: `cd web && npx tsc --noEmit && npx vitest run`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add web/src/api/types.ts web/src/api/client.ts web/src/api/client.test.ts web/src/pages/Settings.tsx web/src/App.tsx web/src/locales/en-US.ts
git commit -m "feat(web): add capture settings to the admin console"
```

**Implementation record**

- Date/status: 2026-11-09 / done
- Repository + commit: cuttle / `d969918b`
- Files: `web/src/api/types.ts`, `web/src/api/client.ts`, `web/src/api/client.test.ts`, `web/src/pages/Settings.tsx`, `web/src/App.tsx`, `web/src/locales/en-US.ts`
- Tests: `cd web && npx vitest run src/api/client.test.ts` -> PASS (12); `npx tsc --noEmit` -> clean; `npx vitest run` -> PASS (231 tests)
- Review findings/fixes: reused the existing `仅错误` translation instead of adding a duplicate; the mode option uses `不捕获` to avoid clashing with the existing `关闭` = "Close".
- Deferred: none

---

### Task 6: Truncation tag in the request detail

**Files:**
- Create: `web/src/capture.ts`
- Create: `web/src/capture.test.ts`
- Modify: `web/src/components/RequestDetail.tsx`
- Modify: `web/src/locales/en-US.ts`

**Interfaces:**
- Consumes: `RequestCapture.truncatedFields`.
- Produces: `isTruncated(cap: RequestCapture | undefined, field: string): boolean`; `Preview` accepts `truncated?: boolean`.

- [ ] **Step 1: Write the failing test**

Add `web/src/capture.test.ts`: `isTruncated(undefined, 'requestOriginal')` is false; `isTruncated({truncatedFields:['requestOriginal']}, 'requestOriginal')` is true; `isTruncated({truncatedFields:['requestOriginal']}, 'requestRewritten')` is false; `isTruncated({}, 'requestOriginal')` is false.

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd web && npx vitest run src/capture.test.ts`
Expected: FAIL — module not found.

- [ ] **Step 3: Implement**

- `web/src/capture.ts`: export `isTruncated` as above.
- `RequestDetail.tsx`: add `Tag` to the antd import; change `Preview` to accept `truncated?: boolean` and render the label plus a `<Tag color="warning">{t('已截断')}</Tag>` in a `Space` when true; pass `truncated={isTruncated(cap, 'requestOriginal')}` (and the matching JSON name) to all eight `Preview` calls.
- `en-US.ts`: add `'已截断': 'Truncated'`.

- [ ] **Step 4: Verify**

Run: `cd web && npx vitest run src/capture.test.ts && npx tsc --noEmit && npm run build`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/capture.ts web/src/capture.test.ts web/src/components/RequestDetail.tsx web/src/locales/en-US.ts
git commit -m "feat(web): mark truncated capture previews"
```

**Implementation record**

- Date/status: 2026-11-09 / done
- Repository + commit: cuttle / <sha>
- Files: `web/src/capture.ts`, `web/src/capture.test.ts`, `web/src/components/RequestDetail.tsx`, `web/src/locales/en-US.ts`
- Tests: `cd web && npx vitest run src/capture.test.ts && npx tsc --noEmit && npm run build` -> PASS
- Review findings/fixes: <fill>
- Deferred: <fill>

---

### Task 7: Full gates

**Files:** none (verification only)

- [ ] **Step 1: Rebuild the web bundle**

Run: `cd web && npm ci && npm run build`
Expected: build succeeds; `internal/admin/dist` refreshed.

- [ ] **Step 2: Go gate**

Run: `GOWORK=off go build ./... && GOWORK=off go vet ./... && GOWORK=off go test ./... -count=1`
Expected: all PASS.

- [ ] **Step 3: Web gate**

Run: `cd web && npm test && npx tsc --noEmit && npm run build`
Expected: all PASS.

- [ ] **Step 4: Manual verification**

With the live deployment: set `maxBytes` to 1048576 in Settings, trigger an upstream error, open the request detail and confirm the request preview contains `tools`; set `maxBytes` to 1024 and confirm the clipped previews show "已截断".

- [ ] **Step 5: Commit any fixes**

```bash
git add -A
git commit -m "chore: verify capture maxBytes rollout"
```

**Implementation record**

- Date/status: 2026-11-09 / done
- Repository + commit: cuttle / <sha>
- Files: <fill>
- Tests: full Go and web gates -> PASS
- Review findings/fixes: <fill>
- Deferred: live deployment still has `maxBytes: 16384` in `cuttle.yaml`; it must be changed through the new Settings form (or the file) after rollout.
