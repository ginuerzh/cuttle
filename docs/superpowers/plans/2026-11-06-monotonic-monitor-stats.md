# Real-time Monotonic Topology Monitoring Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace topology polling with a WebSocket stream that accumulates from a fixed start, reads only new SQLite rows through a rowid cursor, and resets to zero by reconnecting.

**Architecture:** A new `GET /api/events` WebSocket sends absolute graph snapshots every second. Each connection seeds one `[since, now]` snapshot from SQLite, then reads only rows above its `rowid` high-water mark. The fixed start makes totals monotonic; range changes and Reset recreate the connection instead of sending control frames.

**Tech Stack:** Go, SQLite (`modernc.org/sqlite`), `gorilla/websocket` v1.5.3, React 18, TypeScript, Vite, Ant Design, Vitest.

**Spec:** `docs/superpowers/specs/2026-11-06-monotonic-monitor-stats-design.md`

## Global Constraints

- Run every Go command with `GOWORK=off`.
- `github.com/gorilla/websocket` is already in `go.sum` at v1.5.3. Add the import and run `GOWORK=off go mod tidy`; it becomes a direct dependency.
- `/api/events` lives under the existing `requireAuth` wrapper. Validate `Origin` against `r.Host`; allow an empty Origin for non-browser clients.
- WebSocket query parameters: `range=1m|5m|15m|30m|1h|6h|12h|24h`, `since=RFC3339`, `reset=1`. `since` wins over `range`; `reset=1` wins over both.
- A future `since` clamps to now; an old `since` clamps to `now - 7 days`. Return the effective `since` in every snapshot.
- Snapshot JSON: `{"type":"snapshot","since":"...","to":"...","metrics":{...}}`. Snapshots are absolute; the client replaces its metrics.
- Tick every 1s; recompute exact P50/P95 every 10s; ping every 30s; write deadline 5s.
- Reset is client-side reconnect with `reset=1`; no control frames and no historical query.
- Counts and tokens are monotonic between resets. P50/P95 are windowed percentiles and may move either way.
- UI source strings are Chinese; add English entries in `web/src/locales/en-US.ts`.
- Commit after every task, including that task's Implementation record and code. Never push.

## Review Focus

- A long request starts before the cursor and completes after it; the next tick must still count it. Tested in Task 1.
- An out-of-window row inserted above the cursor must not stall the cursor forever. Tested in Task 1.
- A duplicate `request_id` (`INSERT OR REPLACE`) must not double count. Tested in Task 1.
- Entrypoint/route/node config changes must re-attribute or filter historical combos at read time. Tested in Task 1.
- Logout, session expiry and server shutdown must close hijacked WebSockets without leaking goroutines. Tested in Task 2.
- A row exactly at `since` is included and a row exactly at `to` is excluded, matching `GraphWithFilter`. Tested in Task 1.
- Reset with `since=now` must not resurrect rows completed before the reset. Tested in Task 2.

---

### Task 1: Stats aggregator and rowid cursor

**Files:**
- Modify: `internal/stats/graph.go`
- Test: `internal/stats/graph_live_test.go` (create)

**Interfaces:**
- Consumes: existing `Store.Insert`, `Request`, `GraphMetrics`, `EntrypointResolver`, `RowFilter`.
- Produces:
  - `type GraphRow struct { ID string; TS int64; Route, Node, Provider, ClientModel string; Status int; Error string; DurationMS, InputTokens, OutputTokens, CachedTokens int64 }`
  - `type Aggregator struct { /* combos, error samples, seen request IDs */ }`
  - `func NewAggregator() *Aggregator`
  - `func (a *Aggregator) Add(row GraphRow)`
  - `func (a *Aggregator) Metrics(resolve EntrypointResolver, include RowFilter, prev GraphMetrics, recomputePercentiles bool) GraphMetrics`
  - `func (s *Store) GraphAggregate(ctx context.Context, from, to time.Time) (*Aggregator, int64, error)`
  - `func (s *Store) ForEachRequestAfter(ctx context.Context, afterRowID int64, fn func(GraphRow)) (int64, error)`

- [ ] **Step 1: Write the failing tests**

Create `internal/stats/graph_live_test.go` with these cases:

```go
func TestAggregatorMatchesGraphWithFilter(t *testing.T) {
    // Reuse the TestGraphAggregates fixture, plus a client model and a
    // resolver/filter pair.
    agg, _, err := s.GraphAggregate(ctx, from, to)
    if err != nil { t.Fatal(err) }
    want, err := s.GraphWithFilter(ctx, from, to, resolve, include)
    if err != nil { t.Fatal(err) }
    got := agg.Metrics(resolve, include, GraphMetrics{}, true)
    if !reflect.DeepEqual(got, want) { t.Fatalf("got %#v, want %#v", got, want) }
}

func TestGraphAggregateTailIncludesLateRow(t *testing.T) {
    // Seed one row at base, snapshot it, then insert a row at base-30m.
    // The second row has a higher rowid but an older ts.
    last, err := s.ForEachRequestAfter(ctx, high, func(row GraphRow) {
        if row.TS >= from.UnixMilli() && row.TS < to.UnixMilli() { agg.Add(row) }
    })
    // assert last > high and provider:p requests == 2
}

func TestGraphAggregateCursorAdvancesPastOutOfWindowRows(t *testing.T) {
    // Insert an in-window row, then an older-than-from row.
    // Query their rowids directly, call ForEachRequestAfter(0, ...) and
    // assert the returned cursor equals the out-of-window row's rowid.
    // A second call with that cursor must produce zero callbacks.
}

func TestAggregatorDeduplicatesRequestID(t *testing.T) {
    // Add the same GraphRow twice; Metrics(...).Nodes["provider:p"].Requests == 1.
}

func TestAggregatorAppliesResolverAndFilterAtReadTime(t *testing.T) {
    // Add one row with ClientModel "gpt-4.5".
    // Metrics with a resolver has entrypoint:fast; Metrics with nil does not.
    // Metrics with an always-false include has no nodes.
}

func TestAggregatorPercentileCache(t *testing.T) {
    // Add durations 10 and 20; first := Metrics(..., true).
    // Add 30; cached := Metrics(..., first, false); p50/p95 equal first.
    // recomputed := Metrics(..., first, true); p50/p95 reflect all three.
}

func TestGraphWindowBoundaries(t *testing.T) {
    // Insert rows at exactly from, from+1ms, to-1ms and to.
    // GraphAggregate(from, to) must count the first three and exclude to.
}
```

- [ ] **Step 2: Run the focused tests and verify failure**

Run: `GOWORK=off go test ./internal/stats -run 'TestAggregator|TestGraphAggregate|TestForEachRequestAfter|TestGraphWindowBoundaries' -count=1`

Expected: compile failure because `GraphRow`, `Aggregator`, `GraphAggregate`, and `ForEachRequestAfter` do not exist.

- [ ] **Step 3: Implement the aggregator and cursor**

In `internal/stats/graph.go`:

- Add `GraphRow` and an `Aggregator` that keeps a combo map keyed by `(route, node, provider, clientModel)`. Each combo stores request/error counts, token sums and the raw duration samples. Keep a bounded `seen` map plus FIFO of request IDs (cap 200,000) so a replaced row cannot double count.
- Keep raw error samples (request ID, ts, dimensions, client model, status, error message). `Metrics` sorts a copy by ts descending, applies `include`, resolves the entrypoint and takes the first 20.
- `Metrics` iterates combos, applies `include`, expands `provider:`, `node:`, `route:`/`unrouted`, `entrypoint:` and the three edge shapes exactly as `GraphWithFilter` does today. It collects durations per graph key. When `recomputePercentiles` is false, copy `prev.Nodes[key].P50MS/P95MS` for keys present in `prev`; compute for new keys. Reuse `percentileSorted`.
- `GraphAggregate` reads `COALESCE(MAX(rowid),0)`, then selects rows with `rowid <= high AND ts >= from AND ts < to` and adds them to a new `Aggregator`. Return `(agg, high, err)`.
- `ForEachRequestAfter` selects every row with `rowid > afterRowID` **without a ts filter**, ordered by `rowid`, invokes the callback and returns the maximum rowid scanned. This advances the cursor past rows outside the window instead of rescanning them forever. The caller filters by `ts`.
- Rewrite `GraphWithFilter` as `GraphAggregate` followed by `Metrics(resolve, include, GraphMetrics{}, true)` so HTTP and WebSocket share one implementation.
- Keep `GraphMetrics.Nodes`, `.Edges` and `.Errors` non-nil, matching the current JSON contract.

- [ ] **Step 4: Run the focused and package tests**

Run:

```bash
GOWORK=off go test ./internal/stats -run 'TestAggregator|TestGraphAggregate|TestForEachRequestAfter|TestGraphWindowBoundaries' -count=1
GOWORK=off go test ./internal/stats -count=1
```

Expected: PASS, including the existing `TestGraphAggregates`, `TestGraphEntrypointMetrics`, `TestGraphEntrypointsUseRowRoute` and `TestGraphTopologyFilter`.

- [ ] **Step 5: Commit**

```bash
git add internal/stats/graph.go internal/stats/graph_live_test.go \
  docs/superpowers/specs/2026-11-06-monotonic-monitor-stats-design.md \
  docs/superpowers/plans/2026-11-06-monotonic-monitor-stats.md
git commit -m "feat(stats): add incremental graph aggregator and rowid cursor"
```

**Implementation record (fill after completion):**
- Date/status: 2026-11-06 — complete.
- Repo + commit: llm-gw `feat/monotonic-monitor` `b034b262` (code); record finalized in the follow-up `docs(plan)` commit.
- Files changed: `internal/stats/graph.go`, `internal/stats/graph_live_test.go`, `docs/superpowers/specs/2026-11-06-monotonic-monitor-stats-design.md`, `docs/superpowers/plans/2026-11-06-monotonic-monitor-stats.md`.
- Tests/commands and results: `GOWORK=off go test ./internal/stats -run 'TestAggregator|TestGraphAggregate|TestForEachRequestAfter|TestGraphWindowBoundaries' -count=1` → PASS; `GOWORK=off go test ./internal/stats -count=1` → PASS.
- Review findings and fixes: Refactored `GraphWithFilter` onto the same aggregator as the live path; kept exact percentile semantics and the 20-row error cap; cursor scans all rows above the high-water mark so out-of-window rows cannot stall it; bounded request-ID dedup ring.
- Deferred items/limitations: exact duration samples grow with session length; bounded by the 7-day session limit in Task 2.

---

### Task 2: WebSocket `/api/events`

**Files:**
- Modify: `internal/admin/server.go`
- Modify: `internal/admin/handlers_stats.go`
- Create: `internal/admin/handlers_events.go`
- Create: `internal/admin/events_test.go`
- Modify: `internal/admin/auth.go` (logout close hook)
- Modify: `internal/admin/handlers.go` (password change close hook)
- Modify: `main.go`
- Modify: `go.mod`, `go.sum` (`go mod tidy`)

**Interfaces:**
- Consumes: Task 1's `GraphAggregate`, `ForEachRequestAfter`, `Aggregator.Metrics`.
- Produces:
  - Route `GET /api/events` inside the authenticated `/api/` mux.
  - `func (s *Server) Close()` closes all WebSocket connections.
  - `func parseSubscription(q url.Values, now time.Time) (time.Time, error)` returns the effective `since`.
  - `func sameOrigin(r *http.Request) bool`.
  - Snapshot JSON `{"type":"snapshot","since":"RFC3339","to":"RFC3339","metrics":{...}}`.
  - Error JSON before upgrade uses the existing `{"error":"..."}` shape.

- [ ] **Step 1: Write the failing tests**

Create `internal/admin/events_test.go` using `newTestAdmin`, `login`, and `doJSON`. Add `t.Cleanup(s.Close)` in `newTestAdmin` (or in every event test) so hijacked connections cannot outlive the test. Add a helper that dials with a cookie jar:

```go
func dialEvents(t *testing.T, tsURL string, jar http.CookieJar, query, origin string) (*websocket.Conn, *http.Response, error) {
    u := "ws" + strings.TrimPrefix(tsURL, "http") + "/api/events" + query
    h := http.Header{}
    if origin != "" { h.Set("Origin", origin) }
    return (&websocket.Dialer{Jar: jar}).Dial(u, h)
}
```

Cases:

```go
func TestEventsRequiresAuth(t *testing.T) {
    // No jar: Dial returns ErrBadHandshake and resp.StatusCode == 401.
}

func TestEventsRejectsCrossOrigin(t *testing.T) {
    // Logged-in jar with Origin "http://evil.example": resp.StatusCode == 403.
    // Same jar with Origin ts.URL: first snapshot arrives.
}

func TestEventsSnapshotIsMonotonicAndCatchesLateRows(t *testing.T) {
    // s.wsInterval = 20*time.Millisecond; s.wsPctInterval = 20*time.Millisecond
    // Dial ?range=1h; first snapshot has zero requests.
    // Insert a request at time.Now() and read until provider:p requests == 1.
    // Insert a second request with TS time.Now().Add(-time.Minute) (late but
    // inside the window); read until requests == 2.
    // Assert no observed provider request count ever decreases.
}

func TestEventsResetReconnectsAtZero(t *testing.T) {
    // After a nonzero snapshot, close the connection.
    // Dial ?reset=1; first snapshot has zero requests and since ~= now.
    // Insert one request; the next snapshot has requests == 1.
}

func TestEventsClosesOnLogout(t *testing.T) {
    // Dial, then POST /api/logout with CSRF; the next read must fail within 2s.
}

func TestEventsClosesOnSessionExpiry(t *testing.T) {
    // Dial, clear s.sessions under s.mu; the next read must fail within 2s.
}

func TestEventsClosesOnServerClose(t *testing.T) {
    // Dial, call s.Close(); the next read must fail within 2s.
}

func TestParseSubscription(t *testing.T) {
    // range=1h -> since ~= now-1h; reset=1 -> since ~= now;
    // since=<RFC3339> -> exact; future since clamps to now;
    // since older than 7d clamps to now-7d; invalid range returns an error.
}

func TestSameOrigin(t *testing.T) {
    // Empty Origin is allowed; matching host is allowed; different host is not.
}
```

- [ ] **Step 2: Run the focused tests and verify failure**

Run: `GOWORK=off go test ./internal/admin -run 'TestEvents|TestParseSubscription|TestSameOrigin' -count=1`

Expected: compile failure because the route and helpers do not exist.

- [ ] **Step 3: Implement the endpoint and lifecycle**

- Add `github.com/gorilla/websocket` imports; run `GOWORK=off go mod tidy`.
- Extend `Server` with:
  - `wsInterval time.Duration` (default 1s)
  - `wsPctInterval time.Duration` (default 10s)
  - `wsMaxAge time.Duration` (default 7*24h)
  - `wsMu sync.Mutex`
  - `wsConns map[*wsClient]struct{}`
- `New` initializes the intervals, max age and map.
- Add `GET /api/events` to the authenticated API mux.
- `handleEvents`:
  - Reject `s.stats == nil` with 503.
  - Reject a non-matching `Origin` with 403.
  - Parse `range`/`since`/`reset` before upgrading. `range` uses `time.ParseDuration`, valid 1m–24h, default 1h. Clamp `since` as specified. Return 400 JSON on invalid values.
  - Upgrade with `CheckOrigin: func(*http.Request) bool { return true }` after the explicit check.
  - Register a `wsClient` (connection + token hash + `done` + `sync.Once`). `defer` unregister and close.
  - Start a read pump: read limit 4096, read deadline 60s, pong handler extends the deadline; on error close the client.
  - `agg, high, err := s.stats.GraphAggregate(ctx, since, now)`; on error close with 1011.
  - Send the first snapshot using `agg.Metrics(cfg.ResolveEntrypointName, cfg.TopologyIncludes, GraphMetrics{}, true)`.
  - Tick at `s.wsInterval`: check `s.validSessionHash(tokenHash)` (close 4401 on failure), call `s.stats.ForEachRequestAfter(ctx, lastRowID, func(row) { if row.TS >= sinceMs && row.TS < nowMs { agg.Add(row) } })`, update `lastRowID`, recompute percentiles only when `time.Since(lastPct) >= s.wsPctInterval`, send an absolute snapshot. Use a 5s write deadline.
  - A separate 30s ticker sends a ping via `conn.WriteControl`.
  - Build the config snapshot via a new `func (s *Server) monitorConfig() *config.Config`; use it from both `handleGraph` and `handleEvents`.
- Close/lifecycle:
  - Add `func (s *Server) validSessionHash(key string) bool` and make the existing `validSession(token)` delegate with `hashToken(token)`. The WebSocket client stores the token hash and checks it without re-hashing.
  - `wsClient.close(code, reason)` uses `sync.Once`, a 1s `WriteControl` deadline, then `conn.Close()` and `close(done)`.
  - `closeWS(tokenHash)` closes matching connections; `closeAllWS()` closes every connection; `Server.Close()` calls it.
  - `handleLogout` calls `s.closeWS(hashToken(token))`.
  - `handlePassword` calls `s.closeAllWS()` when clearing sessions.
  - `main.go` closes the admin server through a defer that calls `adm.Close()` before `adminSrv.Close()`.
- Ensure `Server.Close` is idempotent and safe when no connections exist.

- [ ] **Step 4: Run the focused and package tests**

Run:

```bash
GOWORK=off go test ./internal/admin -run 'TestEvents|TestParseSubscription|TestSameOrigin' -count=1
GOWORK=off go test ./internal/admin -count=1
GOWORK=off go test ./internal/stats -count=1
```

Expected: PASS. If the WebSocket timing tests are flaky, increase read deadlines, do not loosen the monotonic assertions.

- [ ] **Step 5: Commit**

```bash
git add internal/admin/server.go internal/admin/handlers_stats.go internal/admin/handlers_events.go internal/admin/events_test.go internal/admin/auth.go internal/admin/handlers.go main.go go.mod go.sum \
  docs/superpowers/plans/2026-11-06-monotonic-monitor-stats.md
git commit -m "feat(admin): stream incremental graph snapshots over websocket"
```

**Implementation record (fill after completion):**
- Date/status: 2026-11-06 — complete.
- Repo + commit: llm-gw `feat/monotonic-monitor` `7bcfbb12` (code); record finalized in the follow-up `docs(plan)` commit.
- Files changed: `internal/admin/handlers_events.go` (new), `internal/admin/events_test.go` (new), `internal/admin/server.go`, `internal/admin/handlers_stats.go`, `internal/admin/auth.go`, `internal/admin/handlers.go`, `main.go`, `go.mod`, plan.
- Tests/commands and results: `GOWORK=off go test ./internal/admin -run 'TestEvents|TestParseSubscription|TestSameOrigin' -count=1` → PASS; `GOWORK=off go test ./internal/admin -count=1` → PASS; `GOWORK=off go test ./internal/stats -count=1` → PASS; `GOWORK=off go vet ./internal/admin ./internal/stats` → PASS.
- Review findings and fixes: the first WS test used routed rows while the test config had no topology, so `TopologyIncludes` filtered them; tests now use unrouted rows. Added a one-second grace to the initial snapshot upper bound so a request completing during `MAX(rowid)` cannot be skipped by the cursor. Split `validSessionHash` out so the socket loop does not re-hash the stored token hash.
- Deferred items/limitations: no browser E2E; exact duration samples grow with session length; reverse proxies must preserve Host and Upgrade for `/api/events` (documented in Task 4).

---

### Task 3: Frontend live monitor and Reset

**Files:**
- Create: `web/src/api/events.ts`
- Create: `web/src/api/events.test.ts`
- Create: `web/src/useLiveGraph.ts`
- Modify: `web/src/App.tsx`
- Modify: `web/src/components/OverviewToolbar.tsx`
- Modify: `web/src/components/OverviewToolbar.test.tsx`
- Modify: `web/src/locales/en-US.ts`
- Modify: `web/vite.config.ts`

**Interfaces:**
- Consumes: `/api/events` query params and snapshot JSON from Task 2.
- Produces:
  - `export type SnapshotMessage = { type: 'snapshot'; since: string; to: string; metrics: GraphMetrics }`
  - `export type LiveMessage = SnapshotMessage | { type: 'error'; error: string }`
  - `export function eventsPath(params: { range?: string; since?: string; reset?: boolean }): string`
  - `export function parseLiveMessage(data: string): LiveMessage | null`
  - `export function useLiveGraph(opts: { enabled: boolean; range: string; resetKey: number; onSnapshot: (msg: SnapshotMessage) => void; onStatus: (connected: boolean) => void; onAuthError: () => void }): void`
  - `OverviewToolbarProps` adds `sinceAt: string` and `onReset: () => void`.

- [ ] **Step 1: Write the failing tests**

`web/src/api/events.test.ts`:

```ts
describe('eventsPath', () => {
  it('chooses reset, since or range', () => {
    expect(eventsPath({ reset: true })).toBe('/api/events?reset=1');
    expect(eventsPath({ since: '2026-11-06T11:00:00Z' })).toContain('since=');
    expect(eventsPath({ range: '1h' })).toBe('/api/events?range=1h');
  });
});

describe('parseLiveMessage', () => {
  it('accepts snapshots and rejects malformed messages', () => {
    const snapshot = parseLiveMessage(JSON.stringify({
      type: 'snapshot', since: '2026-11-06T11:00:00Z', to: '2026-11-06T12:00:00Z',
      metrics: { nodes: {}, edges: {}, errors: [] },
    }));
    expect(snapshot?.type).toBe('snapshot');
    expect(parseLiveMessage('not json')).toBeNull();
    expect(parseLiveMessage(JSON.stringify({ type: 'other' }))).toBeNull();
  });
});
```

Extend `OverviewToolbar.test.tsx` with a monitor render using `sinceAt="2026-11-06 11:00:00"` and `onReset={() => undefined}`. Update the two existing render calls with the new required props. Assert the HTML contains `>重置统计<`, `更新`, `统计自`, and the range select. In edit mode assert `重置统计` is absent and the paused tag is present.

- [ ] **Step 2: Run the web tests and verify failure**

Run: `cd web && npm test -- events OverviewToolbar`

Expected: failure because `events.ts` and the new toolbar props do not exist.

- [ ] **Step 3: Implement the helpers, hook and UI**

`web/src/api/events.ts`:

- `eventsPath` uses `URLSearchParams` in the order reset, since, range; returns the path only (the hook prefixes protocol and host).
- `parseLiveMessage` validates `type === 'snapshot'` with string `since`/`to` and object `metrics`, or `type === 'error'` with a string error; returns null otherwise.

`web/src/useLiveGraph.ts`:

- One effect keyed on `[enabled, range, resetKey]`; keep callbacks in refs so inline App callbacks do not reconnect every render.
- Mode ref: `{kind:'range', range}` initially and on a range change; `{kind:'reset'}` on a `resetKey` change; `{kind:'since', since}` after the first snapshot.
- Build `ws(s)://${location.host}${eventsPath(...)}` and open a `WebSocket`.
- On snapshot: store `since` in a ref, switch mode to `since`, call `onSnapshot` and `onStatus(true)`, reset the retry counter.
- On close/error: call `onStatus(false)`. If code is 4401, call `onAuthError`. Otherwise call `getConfig()` once; on `UnauthorizedError` call `onAuthError`, else schedule a reconnect with exponential backoff (1s base, 30s cap, jitter) using the current mode.
- Close when `document.hidden`; reconnect with the current mode when visible.
- Cleanup: mark disposed, clear timers, remove the visibility listener, close the socket, and ignore callbacks from stale sockets.
- Do not reset the mode when `enabled` becomes false (page navigation or edit mode); re-enable resumes with the last `since`.

`web/src/App.tsx`:

- Remove the polling effect and the `getGraph` import.
- Add `since` and `resetKey` state; keep `stale`/`updatedAt`.
- Call `useLiveGraph` with `enabled = session === 'yes' && mode === 'monitor' && page === 'topology'`.
- `onSnapshot`: `setMetrics(msg.metrics)`, `setSince(msg.since)`, `setUpdatedAt(formatDateTime(msg.to))`, `setStale(false)`.
- `onStatus`: `setStale(!connected)`.
- `onAuthError`: `setSession('no')`.
- `handleRange(next)`: `setRange(next)`; the hook reconnects with the new range.
- `handleReset`: set metrics to `{nodes:{},edges:{},errors:[]}`, clear `since`/`updatedAt`, increment `resetKey`. The hook reconnects with `reset=1`.
- On logout: clear metrics, `since`, `updatedAt`, and `stale`.
- Keep the edit-mode effect that clears metrics/selection.

`web/src/components/OverviewToolbar.tsx`:

- Add `sinceAt` and `onReset`.
- Status text: `stale ? '数据延迟' : updatedAt ? t('更新 {time} · 统计自 {since}', { time: updatedAt, since: sinceAt || '—' }) : '等待数据'`.
- Add a small Reset button with `ReloadOutlined`, `onClick={onReset}`, `title={t('重置统计')}` and text `t('重置统计')`, placed after the range Select.

`web/src/locales/en-US.ts`:

```ts
'重置统计': 'Reset stats',
'更新 {time} · 统计自 {since}': 'Updated {time} · since {since}',
```

`web/vite.config.ts`:

```ts
proxy: {
  '/api': { target: 'http://127.0.0.1:8788', ws: true },
},
```

- [ ] **Step 4: Run the web gate**

Run:

```bash
cd web
npm test
npx tsc --noEmit
npm run build
```

Expected: all pass. `noUnusedLocals`/`noUnusedParameters` are enabled; remove dead imports and variables.

- [ ] **Step 5: Rebuild the embedded bundle and commit**

```bash
cd web && npm run build
cd ..
git add web/src/api/events.ts web/src/api/events.test.ts web/src/useLiveGraph.ts web/src/App.tsx web/src/components/OverviewToolbar.tsx web/src/components/OverviewToolbar.test.tsx web/src/locales/en-US.ts web/vite.config.ts \
  docs/superpowers/plans/2026-11-06-monotonic-monitor-stats.md
git commit -m "feat(web): stream topology metrics and add reset"
```

**Implementation record (fill after completion):**
- Date/status: 2026-11-06 — complete.
- Repo + commit: llm-gw `feat/monotonic-monitor` `6e89f6c3` (code); record finalized in the follow-up `docs(plan)` commit.
- Files changed: `web/src/api/events.ts` (new), `web/src/api/events.test.ts` (new), `web/src/useLiveGraph.ts` (new), `web/src/App.tsx`, `web/src/components/OverviewToolbar.tsx`, `web/src/components/OverviewToolbar.test.tsx`, `web/src/locales/en-US.ts`, `web/vite.config.ts`, plan.
- Tests/commands and results: `cd web && npm test` → 18 files / 122 tests PASS; `npx tsc --noEmit` → PASS; `npm run build` → PASS; `GOWORK=off go build ./...` and `GOWORK=off go build -o /tmp/llm-gw .` → PASS.
- Review findings and fixes: added a `sessionKey` option so logout/session expiry resets the hook's internal `since` instead of resuming the pre-logout session; guarded reconnect/auth callbacks with `disposed` so a stale socket cannot force a login or reconnect; kept range changes and resets as fresh socket URLs rather than control frames.
- Deferred items/limitations: no browser E2E exists; UI behavior is covered by Vitest static tests, `tsc`, the Vite build and Go WebSocket integration tests. Vite dev uses `ws: true`; reverse proxies must forward Upgrade and preserve Host (documented in Task 4).

---

### Task 4: Documentation and full verification

**Files:**
- Modify: `README.md`
- Modify: `AGENTS.md`
- Modify: `docs/superpowers/plans/2026-11-06-monotonic-monitor-stats.md` (records only)

**Interfaces:**
- Consumes: all previous tasks.
- Produces: operator documentation and the final gate evidence.

- [ ] **Step 1: Update README**

Add a topology monitoring subsection covering:

- The monitor uses `GET /api/events` over WebSocket.
- The duration selector sets the initial lookback; the end is always now.
- Request/error/token totals are cumulative and monotonic; Reset starts at zero.
- P50/P95 are percentiles over the accumulated window.
- Reset clears the display and reconnects with `reset=1`; no history is queried.
- Reverse proxies must forward WebSocket Upgrade for `/api/events` and preserve the `Host` header so the Origin check passes; Vite dev proxies `/api` with `ws: true`.
- The `GET /api/stats/graph` HTTP endpoint remains for API clients.

- [ ] **Step 2: Update AGENTS.md invariants**

- Admin API: `/api/events` under `requireAuth`, Origin check, `range`/`since`/`reset=1`, absolute snapshots, closes on logout/expiry/shutdown.
- Stats: rowid cursor in insertion order, never `ts`; `GraphAggregate`/`ForEachRequestAfter`; dedup by request ID; counts/tokens monotonic, P50/P95 windowed.
- Web: topology uses WebSocket; range = initial lookback; Reset clears and reconnects; status shows `since`.
- Gotchas: reverse proxy Upgrade, Vite `ws: true`.

- [ ] **Step 3: Run the full Go gate**

```bash
GOWORK=off go build ./...
GOWORK=off go vet ./...
GOWORK=off go test ./... -count=1
```

Expected: all pass.

- [ ] **Step 4: Run the full web gate and rebuild the bundle**

```bash
cd web
npm test
npx tsc --noEmit
npm run build
cd ..
GOWORK=off go build -o /tmp/llm-gw .
```

Expected: all pass. State explicitly that no browser visual E2E exists; WebSocket behavior is covered by Go integration tests and the web gate.

- [ ] **Step 5: Final review and commit**

Review the diff for accidental workspace changes from the sibling repositories or the demo config. Confirm no secrets are staged. Commit:

```bash
git add README.md AGENTS.md docs/superpowers/plans/2026-11-06-monotonic-monitor-stats.md
git commit -m "docs: describe real-time monotonic monitoring"
```

**Implementation record (fill after completion):**
- Date/status: 2026-11-06 — complete.
- Repo + commit: llm-gw `feat/monotonic-monitor` `a49dcf91` (docs); record finalized in the follow-up `docs(plan)` commit.
- Files changed: `README.md`, `AGENTS.md`, plan.
- Tests/commands and results: `GOWORK=off go build ./...` → PASS; `GOWORK=off go vet ./...` → PASS; `GOWORK=off go test ./... -count=1` → PASS (all packages); `cd web && npm test` → 18 files / 122 tests PASS; `npx tsc --noEmit` → PASS; `npm run build` → PASS; `GOWORK=off go build -o /tmp/llm-gw .` → PASS.
- Review findings and fixes: README and AGENTS now document the WebSocket endpoint, fixed-start monotonic semantics, reset-by-reconnect, rowid cursor, Origin/Host checks and reverse-proxy Upgrade requirements.
- Deferred items/limitations: no browser visual E2E exists; UI behavior is verified by Vitest, TypeScript, the Vite build and Go WebSocket integration tests. Exact duration samples and the initial snapshot grow with session length; the 7-day clamp bounds them. An unrelated untracked `2026-11-06-error-aggregation-p1-design.md` was left untouched.

---

### Post-review fix pass

**Files:**
- Modify: `internal/stats/store.go`, `internal/stats/graph.go`, `internal/stats/graph_live_test.go`, `internal/stats/store_test.go`
- Modify: `internal/admin/handlers_events.go`, `internal/admin/events_test.go`
- Modify: `web/src/useLiveGraph.ts`, `web/package.json`, `web/package-lock.json`
- Create: `web/src/useLiveGraph.test.tsx`
- Modify: `README.md`, `AGENTS.md`, spec, plan

**Findings addressed:**
1. Important: SQLite can reuse rowids after retention deletes the high-water row; replaced the live cursor with `requests.seq` backed by a `request_seq` counter, a `PRAGMA user_version=1` migration that backfills `seq = rowid`, and a retention-delete regression test.
2. Important: unbounded per-tick work and memory; bounded error samples to 200, skip duration materialization for cached percentile keys, and close open sessions older than `wsMaxAge` with code 4408.
3. Important: no client hook tests; added `jsdom` as a dev dependency and 8 `useLiveGraph` tests covering range/resume, reset, session reset, 4401, 4408, auth probe, visibility, and stale sockets.

**Deferred minors:** the live tail includes the tick's inclusive end boundary (documented); `since` is returned with second precision; the visibility/`getConfig` replay race; `closeAllWS` under the admin lock; missing Go assertions for since-resume and the 4401 close code.

**Implementation record:**
- Date/status: 2026-11-06 — complete.
- Repo + commit: llm-gw `feat/monotonic-monitor` (fix commit; SHA recorded in the follow-up `docs(plan)` commit).
- Files changed: `internal/stats/store.go`, `internal/stats/graph.go`, `internal/stats/graph_live_test.go`, `internal/stats/store_test.go`, `internal/admin/handlers_events.go`, `internal/admin/events_test.go`, `web/src/useLiveGraph.ts`, `web/src/useLiveGraph.test.tsx` (new), `web/package.json`, `web/package-lock.json`, `README.md`, `AGENTS.md`, spec, plan.
- Tests/commands and results: `GOWORK=off go build ./...` → PASS; `GOWORK=off go vet ./...` → PASS; `GOWORK=off go test ./... -count=1` → PASS; `cd web && npm test` → 19 files / 130 tests PASS; `npx tsc --noEmit` → PASS; `npm run build` → PASS.
- Review findings and fixes: `TestGraphAggregateCursorSurvivesRetentionDeletes` failed before the sequence column and passes after; `TestOpenMigratesMonotonicSeq` covers the old-DB backfill; `TestAggregatorBoundsErrorSamples` covers the ring; `useLiveGraph.test.tsx` covers the reconnect state machine.
- Deferred items/limitations: see the deferred minors above; no browser visual E2E exists.
