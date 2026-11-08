# Notification Bell Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the always-on Overview auth-failure panel with a clearable notification bell beside the edit toggle, backed by a generic notification model.

**Architecture:** The existing bounded `authfail.Tracker` gains a `Clear` method exposed through an authenticated `DELETE /api/auth-failures`. The web app adapts the existing `authFailures` event payload into generic `AppNotification` entries and renders them in a bell Popover in `OverviewToolbar`. The Overview WebSocket runs in both monitor and edit modes so notifications keep arriving.

**Tech Stack:** Go 1.26, gorilla/websocket, React + Ant Design, Vitest.

**Spec:** `docs/superpowers/specs/2026-11-08-notification-bell-design.md`

## Global Constraints

- Never store or return credential values.
- Clearing affects only the in-memory tracker; 401s stay out of SQLite.
- Keep the events payload additive and the tracker bounded at 50 entries.
- Admin-only clear endpoint; CSRF header handled by the existing web client.
- UI strings are Chinese source keys with `en-US` translations.
- Do not commit or push unless the user explicitly asks (workspace convention).
- Run Go commands with `GOWORK=off`; rebuild the embedded web bundle before the
  final Go build.

## Review Focus

- Clear must be idempotent and must not panic when the tracker is absent.
- Clearing must survive the next `/api/events` tick (no reappearing entries).
- The red dot must reflect the current list, including after a reload.
- The bell must not block the edit toggle or graph interactions.
- Notifications must stay newest-first and bounded in the UI.

---

### Task 1: Clear endpoint

**Files:**
- Modify: `internal/authfail/authfail.go`
- Modify: `internal/authfail/authfail_test.go`
- Modify: `internal/admin/handlers_events.go`
- Modify: `internal/admin/server.go`
- Test: `internal/admin/events_test.go`

**Interfaces:**
- Produces: `func (t *Tracker) Clear()`
- Produces: `DELETE /api/auth-failures` → 204
- Consumes: `Tracker.Snapshot()`

- [ ] **Step 1: Write the failing tracker test**

Add `TestClearDropsEntriesAndTruncation`: add two entries with `New(2)`, call `Clear`, assert `Snapshot()` has zero entries and `Truncated == false`.

- [ ] **Step 2: Run it and watch it fail**

Run: `GOWORK=off go test ./internal/authfail -run TestClear -count=1`
Expected: FAIL because `Clear` does not exist.

- [ ] **Step 3: Implement `Tracker.Clear` and the admin handler**

`Clear` locks, zeroes `entries` while keeping capacity, and resets `total`.
The handler calls `s.authFailures.Clear()` when non-nil and always writes 204.
Register `api.HandleFunc("DELETE /api/auth-failures", s.handleClearAuthFailures)`.

- [ ] **Step 4: Write the failing admin test**

`TestEventsClearAuthFailures`: attach a tracker with one failure, log in, send
`DELETE /api/auth-failures` with the CSRF header (reuse the existing helper
pattern), assert 204, then dial `/api/events` and assert the snapshot has an
empty `authFailures.recent`.

- [ ] **Step 5: Run the admin test**

Run: `GOWORK=off go test ./internal/admin -run 'TestEventsClearAuthFailures' -count=1`
Expected: PASS.

**Implementation record**

- Date/status: 2026-11-08 / done
- Repository + commit: cuttle / pending
- Files: `internal/authfail/authfail.go`, `internal/authfail/authfail_test.go`, `internal/admin/handlers_events.go`, `internal/admin/server.go`, `internal/admin/events_test.go`
- Tests: `GOWORK=off go test ./internal/authfail ./internal/admin -run 'TestClear|TestEventsClearAuthFailures' -count=1` → PASS
- Review findings/fixes: clear is idempotent and safe on a nil tracker; the handler returns 204 even when no tracker is attached
- Deferred: none

---

### Task 2: Generic notifications and the bell

**Files:**
- Create: `web/src/notifications.ts`
- Create: `web/src/notifications.test.ts`
- Create: `web/src/components/NotificationBell.tsx`
- Create: `web/src/components/NotificationBell.test.tsx`
- Modify: `web/src/api/client.ts`
- Modify: `web/src/components/OverviewToolbar.tsx`
- Modify: `web/src/components/OverviewToolbar.test.tsx`
- Modify: `web/src/App.tsx`
- Modify: `web/src/pages/Topology.tsx`
- Modify: `web/src/locales/en-US.ts`
- Delete: `web/src/components/AuthFailureAlert.tsx`
- Delete: `web/src/components/AuthFailureAlert.test.tsx`

**Interfaces:**
- Produces:
  - `interface AppNotification { id: string; kind: string; ts: string; title: string; detail: string }`
  - `authFailureNotifications(snapshot: AuthFailureSnapshot | null | undefined): AppNotification[]`
  - `function NotificationBell({ items, onClear }: { items: AppNotification[]; onClear: () => void })`
  - `function NotificationList({ items, onClear }: { items: AppNotification[]; onClear: () => void })`
  - `clearAuthFailures(): Promise<void>`
- Consumes: `formatDateTime` from `web/src/time.ts`; `t` from `web/src/i18n`.

- [ ] **Step 1: Write the failing adapter test**

`notifications.test.ts`: an empty/null snapshot yields `[]`; two entries yield
newest-first titles, details containing the reason label, client IP and
`method path`, and stable ids.

- [ ] **Step 2: Write the failing component test**

`NotificationBell.test.tsx` (SSR): `NotificationList` with no items shows
`暂无通知` and a disabled `清除`; with items shows titles, details, times and an
enabled clear button. `NotificationBell` with items contains `ant-badge-dot`;
with no items it does not.

- [ ] **Step 3: Run the tests and watch them fail**

Run: `cd web && npx vitest run src/notifications.test.ts src/components/NotificationBell.test.tsx`
Expected: FAIL because the modules do not exist.

- [ ] **Step 4: Implement the adapter and bell**

Adapter reverses the server list (newest first) and maps
`ts + index` to the id. Bell = `Popover` containing `NotificationList`, with a
`Badge dot` around a text `Button` using `BellOutlined`. Clear button calls
`onClear`; empty state uses `Empty`.

- [ ] **Step 5: Wire the toolbar, App and Topology**

OverviewToolbar gains required `notifications` and `onClearNotifications`
props; wrap the edit button and bell in one `Space`. App derives
`notifications` with `useMemo`, clears via `clearAuthFailures()` then sets
`authFailures` to `{ recent: [] }`, and enables `useLiveGraph` for
`page === 'topology'` in both modes. Remove the Panel and `authFailures` prop
from Topology and delete the old alert component.

- [ ] **Step 6: Run the tests and typecheck**

Run: `cd web && npx vitest run src/notifications.test.ts src/components/NotificationBell.test.tsx src/components/OverviewToolbar.test.tsx && npx tsc --noEmit`
Expected: PASS.

**Implementation record**

- Date/status: 2026-11-08 / done
- Repository + commit: cuttle / pending
- Files: `web/src/notifications.ts`, `web/src/notifications.test.ts`, `web/src/components/NotificationBell.tsx`, `web/src/components/NotificationBell.test.tsx`, `web/src/components/OverviewToolbar.tsx`, `web/src/components/OverviewToolbar.test.tsx`, `web/src/App.tsx`, `web/src/pages/Topology.tsx`, `web/src/api/client.ts`, `web/src/locales/en-US.ts`; deleted `web/src/components/AuthFailureAlert.tsx` and its test
- Tests: `npx vitest run src/notifications.test.ts src/components/NotificationBell.test.tsx src/components/OverviewToolbar.test.tsx src/App.test.tsx` → PASS (18 tests); `npx tsc --noEmit` → PASS
- Review findings/fixes: introduced `sameAuthFailures` so the 1s snapshot does not re-render the edit view when nothing changed; the bell stays live in both monitor and edit modes while monitor stats stay paused in edit mode
- Deferred: read/unread state; notification history

---

### Task 3: Documentation and gates

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Update README**

Replace the Overview warning-panel text with the bell: red dot, click to open,
clear button, in-memory since process start, not in SQLite. Document
`DELETE /api/auth-failures` in the admin API section and note that the bell
stays live in edit mode.

- [ ] **Step 2: Run the Go gate**

Run: `GOWORK=off go build ./... && GOWORK=off go vet ./... && GOWORK=off go test ./... -count=1`
Expected: PASS.

- [ ] **Step 3: Run the web gate**

Run: `cd web && npm test && npx tsc --noEmit && npm run build`
Expected: PASS.

- [ ] **Step 4: Rebuild the embedded bundle and binary**

Run: `cd web && npm run build && cd .. && GOWORK=off go build -o cuttle .`
Expected: PASS.

**Implementation record**

- Date/status: 2026-11-08 / done
- Repository + commit: cuttle / pending
- Files: `README.md`
- Tests: Go gate (`build`, `vet`, `test ./... -count=1`) → PASS; web gate (`npm test` 230 tests, `npx tsc --noEmit`, `npm run build`) → PASS; embedded bundle rebuilt and `GOWORK=off go build -o cuttle .` → PASS
- Review findings/fixes: README now documents the bell, the clear action and `DELETE /api/auth-failures`; the graph no longer has a persistent overlay
- Deferred: none
