# Auth Failure Visibility Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Show recent client gateway-key authentication failures in the Overview monitor without persisting them or touching request statistics.

**Architecture:** A bounded in-memory ring in `internal/authfail` is filled by the data-plane auth handler and exposed through the existing `/api/events` WebSocket snapshot. The console keeps the latest snapshot and renders a warning panel on Overview, filtered to the monitor's current window.

**Tech Stack:** Go 1.26, gorilla/websocket, embedded React + Ant Design + React Flow, Vitest.

**Spec:** `docs/superpowers/specs/2026-11-08-auth-failure-visibility-design.md`

## Global Constraints

- Never store or log credential values; the reason is one of `missing`, `disabled`, `invalid`.
- Keep memory bounded: ring of 50; truncate client-controlled strings (path 256 bytes, user agent 200 bytes).
- 401 requests stay absent from `requests`, `rollups` and `key_rollups`; do not touch `internal/stats`.
- The events payload is additive: `authFailures` is optional and old clients must keep working.
- Admin-only: the tracker is exposed only through session-authenticated admin routes.
- Do not edit sibling repositories.
- Do not commit or push unless the user explicitly asks (workspace convention).
- Run Go commands with `GOWORK=off`; rebuild `web` before the final Go build if web sources changed.

## Review Focus

- A burst of invalid keys must not grow memory, block the auth path, or displace anything else.
- A 1 MiB path or user agent must not be stored whole or break JSON/UI rendering.
- Absent or empty tracker must leave the events payload unchanged for existing parsers.
- Range changes and Reset must not show failures outside the current window.
- The panel must be read-only and must not interfere with graph drag/select.

---

### Task 1: Bounded failure tracker

**Files:**
- Create: `internal/authfail/authfail.go`
- Test: `internal/authfail/authfail_test.go`

**Interfaces:**
- Produces:
  - `type Failure struct { TS time.Time; Reason, Method, Path, ClientIP, UserAgent string }`
  - `type Snapshot struct { Recent []Failure; Truncated bool }`
  - `func New(limit int) *Tracker`
  - `func (t *Tracker) Add(f Failure)`
  - `func (t *Tracker) Snapshot() Snapshot`
  - `const DefaultLimit = 50`

- [ ] **Step 1: Write the failing tests**

Cover: oldest-first order; a limit of 2 keeps the two newest and sets `Truncated`; `Snapshot` returns a copy that later `Add` calls cannot mutate; a nil tracker is safe; `Recent` is never nil.

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOWORK=off go test ./internal/authfail -count=1`
Expected: FAIL because the package does not exist.

- [ ] **Step 3: Implement the tracker**

Mutex-guarded `[]Failure` ring, `total` counter, `New` normalizes `limit <= 0` to `DefaultLimit`, `Add` sets `TS` to `time.Now().UTC()` when zero and normalizes non-zero values to UTC.

- [ ] **Step 4: Run the test to verify it passes**

Run: `GOWORK=off go test ./internal/authfail -count=1`
Expected: PASS.

**Implementation record**

- Date/status: 2026-11-08 / done
- Repository + commit: cuttle / not committed (workspace convention)
- Files: `internal/authfail/authfail.go`, `internal/authfail/authfail_test.go`
- Tests: `GOWORK=off go test ./internal/authfail -count=1` → PASS (5 tests)
- Review findings/fixes: none
- Deferred: none

---

### Task 2: Record failures in the data plane

**Files:**
- Modify: `internal/gateway/auth.go`
- Modify: `internal/gateway/gateway.go`
- Test: `internal/gateway/auth_test.go`

**Interfaces:**
- Consumes: `authfail.New`, `authfail.Failure`, `authfail.DefaultLimit`.
- Produces: `func (g *Gateway) AuthFailures() *authfail.Tracker`.

- [ ] **Step 1: Write the failing test**

Extend `TestGatewayAuthRejectsMissingInvalidAndDisabled` to assert one tracker entry after each 401 with the matching reason, method `POST`, path `/v1/chat/completions`, non-empty client IP, and no secret in any field. Add a success case asserting the tracker stays empty.

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOWORK=off go test ./internal/gateway -run 'TestGatewayAuthRejectsMissingInvalidAndDisabled' -count=1`
Expected: FAIL because `Gateway.AuthFailures` does not exist.

- [ ] **Step 3: Implement**

Add a `failures *authfail.Tracker` field to `Gateway`, create it in `New`, pass it to `newGatewayAuthHandler`, and record in the `!authenticated` branch using `sanitizeGatewayAuthMethod`, `gatewayAuthLogTarget`, `gatewayAuthClientIP` (split host from `RemoteAddr`) and `truncateAuthFailure` for the user agent. Do not change the response or the log line.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `GOWORK=off go test ./internal/gateway -run 'TestGatewayAuth' -count=1`
Expected: PASS.

**Implementation record**

- Date/status: 2026-11-08 / done
- Repository + commit: cuttle / not committed (workspace convention)
- Files: `internal/gateway/auth.go`, `internal/gateway/gateway.go`, `internal/gateway/auth_test.go`
- Tests: `GOWORK=off go test ./internal/gateway -run 'TestGatewayAuth' -count=1` → PASS; full gateway package in Task 5 → PASS
- Review findings/fixes: path capped at 256 bytes and user agent at 200 bytes; the response and log line are unchanged; successful requests assert an empty tracker
- Deferred: per-key name for a disabled match (not needed for the minimal version)

---

### Task 3: Expose the tracker on the events socket

**Files:**
- Modify: `internal/admin/server.go`
- Modify: `internal/admin/handlers_events.go`
- Modify: `main.go`
- Test: `internal/admin/events_test.go`

**Interfaces:**
- Consumes: `authfail.Tracker`.
- Produces: `func (s *Server) SetAuthFailures(t *authfail.Tracker)`; `eventMessage.AuthFailures *authfail.Snapshot`; `authFailureSnapshot() *authfail.Snapshot`.

- [ ] **Step 1: Write the failing test**

In `TestEventsCarriesAuthFailures`, attach a tracker with one failure before dialing, read the first snapshot, and assert the JSON has `authFailures.recent[0]` with the reason and client IP. Keep `eventSnapshot` in the test tolerant of the new field.

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOWORK=off go test ./internal/admin -run 'TestEventsCarriesAuthFailures' -count=1`
Expected: FAIL because the setter does not exist.

- [ ] **Step 3: Implement**

Add the field/setter, add the optional payload to both snapshot writes, and call `adm.SetAuthFailures(gw.AuthFailures())` in `main.go` after `admin.New`.

- [ ] **Step 4: Run the test to verify it passes**

Run: `GOWORK=off go test ./internal/admin -run 'TestEvents' -count=1`
Expected: PASS.

**Implementation record**

- Date/status: 2026-11-08 / done
- Repository + commit: cuttle / not committed (workspace convention)
- Files: `internal/admin/server.go`, `internal/admin/handlers_events.go`, `internal/admin/events_test.go` (no `main.go` change needed: `New` type-asserts the reloader for `AuthFailures()`, the same pattern used for `Stats`/`Capture`/`RouteHealth`)
- Tests: `GOWORK=off go test ./internal/admin -run 'TestEvents|TestNewWiresAuthFailuresFromReloader' -count=1` → PASS
- Review findings/fixes: added `TestNewWiresAuthFailuresFromReloader` so the production type-assertion wiring cannot silently stop delivering the tracker; kept `SetAuthFailures` for tests and explicit wiring
- Deferred: historical persistence

---

### Task 4: Render the Overview warning

**Files:**
- Modify: `web/src/api/types.ts`
- Modify: `web/src/api/events.ts`
- Modify: `web/src/App.tsx`
- Modify: `web/src/pages/Topology.tsx`
- Create: `web/src/components/AuthFailureAlert.tsx`
- Create: `web/src/components/AuthFailureAlert.test.tsx`
- Modify: `web/src/api/events.test.ts` (if present) or add parser coverage in the new test
- Modify: `web/src/locales/en-US.ts`

**Interfaces:**
- Produces:
  - `type AuthFailure`, `type AuthFailureSnapshot`
  - `recentAuthFailures(snapshot: AuthFailureSnapshot | null | undefined, since?: string): AuthFailure[]`
  - `function AuthFailureAlert({ failures, since }: { failures?: AuthFailureSnapshot | null; since?: string })`
  - `Topology` prop `authFailures?: AuthFailureSnapshot | null`

- [ ] **Step 1: Write the failing tests**

`AuthFailureAlert.test.tsx` (SSR, same style as `RequestDetail.test.tsx`): renders nothing for `null`, `{recent: []}` and entries older than `since`; renders the count, reason label, client IP and path for an in-window failure; prints `≥` when `truncated`. Add parser assertions in `web/src/api/events.test.ts` if that file exists; otherwise extend `AuthFailureAlert.test.tsx` with `parseLiveMessage` cases for present, absent and malformed `authFailures`.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd web && npx vitest run src/components/AuthFailureAlert.test.tsx`
Expected: FAIL because the module does not exist.

- [ ] **Step 3: Implement**

Add the types and parser pass-through; store the snapshot in `App` (reset on logout/auth error/reset); pass it to `Topology`; render an Ant Design `Alert` inside a React Flow `Panel position="top-left"` only when `recentAuthFailures` is non-empty. Reason labels: `missing` → `未提供密钥`, `disabled` → `密钥已禁用`, `invalid` → `密钥无效`. Add all Chinese source strings to `en-US.ts`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd web && npx vitest run src/components/AuthFailureAlert.test.tsx src/api/events.test.ts && npx tsc --noEmit`
Expected: PASS.

**Implementation record**

- Date/status: 2026-11-08 / done
- Repository + commit: cuttle / not committed (workspace convention)
- Files: `web/src/api/types.ts`, `web/src/api/events.ts`, `web/src/api/events.test.ts`, `web/src/App.tsx`, `web/src/pages/Topology.tsx`, `web/src/components/AuthFailureAlert.tsx`, `web/src/components/AuthFailureAlert.test.tsx`, `web/src/locales/en-US.ts`
- Tests: `npx vitest run src/components/AuthFailureAlert.test.tsx src/api/events.test.ts` → PASS (10 tests); `npx tsc --noEmit` → PASS
- Review findings/fixes: parser validates `authFailures.recent` strictly; the panel is filtered by the server-provided `since`, so range changes and Reset cannot resurface old entries
- Deferred: no browser visual test (SSR/TS only)

---

### Task 5: Documentation and full gates

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Update README**

In the console table, extend the Overview row with "recent gateway-key auth failures". In the monitor section, state that failures are kept in memory (last 50, since process start) and filtered by the monitor window; they are not written to SQLite. In the gateway-key section, say the console surfaces recent failures with reason, client IP, method and path. In the stats section, clarify that 401s still produce no request/usage rows.

- [ ] **Step 2: Run the Go gate**

Run: `GOWORK=off go build ./... && GOWORK=off go vet ./... && GOWORK=off go test ./... -count=1`
Expected: PASS.

- [ ] **Step 3: Run the web gate**

Run: `cd web && npm test && npx tsc --noEmit && npm run build`
Expected: PASS.

- [ ] **Step 4: Rebuild the embedded bundle and build the binary**

Run: `cd web && npm run build && cd .. && GOWORK=off go build -o cuttle .`
Expected: PASS.

**Implementation record**

- Date/status: 2026-11-08 / done
- Repository + commit: cuttle / not committed (workspace convention)
- Files: `README.md`
- Tests: Go gate (`build`, `vet`, `test ./... -count=1`) → PASS; web gate (`npm test` 227 tests, `npx tsc --noEmit`, `npm run build`) → PASS; embedded bundle rebuilt and `GOWORK=off go build -o cuttle .` → PASS
- Review findings/fixes: README now says 401s stay out of SQLite but are visible in memory; Overview table and monitor sections updated
- Deferred: none
