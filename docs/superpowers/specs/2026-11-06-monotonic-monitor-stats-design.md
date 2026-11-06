# Real-time monotonic topology monitoring over WebSocket

## Intent

The topology monitor currently polls `GET /api/stats/graph` with a sliding
window (`from = now - range`, `to = now`). Old rows age out, so request,
error and token totals move down. Each poll also re-scans the whole window,
and the queried window grows without bound once the start is fixed.

The monitor should accumulate from a fixed start and push updates. The
duration selector chooses the initial lookback; the end is always `now`.

## Semantics

- Entering the topology monitor starts a lookback session with
  `since = serverNow - selected range`.
- Every update covers `[since, now]`; `since` never moves during the session.
- Requests are attributed by their start time (`ts`), consistent with the
  summary and graph APIs.
- Request, error and token totals are monotonically non-decreasing between
  resets.
- The **Reset** button starts a zero-based session at `since = serverNow`.
  Counters begin at 0 and grow from there. The client clears the displayed
  metrics and reconnects the WebSocket with `reset=1`; the server sets
  `since = now` for the new connection. No historical query is performed.
- Changing the duration selector starts a new lookback session with
  `since = serverNow - new range`. Counts may jump up or down because the
  start moved; that is an explicit new session.
- Reloading the page starts a lookback session again. The stream reconnects
  automatically; there is no separate manual refresh action.
- Navigating to another console page and back preserves the session.
- Switching to edit mode pauses the stream; returning resumes the same
  session and catches up.
- P50/P95 remain percentiles over `[since, now]`. They are not cumulative
  sums and can move in either direction; only counts and tokens are
  guaranteed monotonic.
- Logout closes the stream and clears the session.

## Why WebSocket

- One long-lived connection replaces per-tab polling.
- The server can keep one cursor per subscriber and read only rows inserted
  since the last tick.
- The client can be pushed absolute snapshots, so reconnect, backpressure and
  late rows need no delta reconciliation.
- `gorilla/websocket` is already in the build graph through `x`; no new
  module download is required.

## Incremental cursor: monotonic seq, not ts

`stats.Request.TS` is the request **start** time (`x/handler/forward/local/
handler.go` creates the recorder object with `start := time.Now()` and writes
the row when the request completes). A long LLM stream can start before
`lastPoll` and finish after it, so `WHERE ts > lastPoll` would silently miss
it.

Use the `requests.seq` column, allocated from the single-row `request_seq`
counter in the same transaction as the insert. It is monotonic across
retention deletes; SQLite can reuse a deleted `rowid`, so `rowid` is not safe
as a long-lived cursor.

1. Snapshot: read `MAX(seq)` as `high`, then aggregate
   `WHERE seq <= high AND ts >= since AND ts < to`.
2. Tail: aggregate `WHERE seq > lastSeq AND ts >= since AND ts < now`
   ordered by `seq`, then advance `lastSeq`.
3. Rows inserted after the snapshot and rows that completed late are both
   picked up because insertion order is the cursor.
4. Migration v1 backfills `seq = rowid` for existing rows and initializes the
   counter once; the counter is never decremented.

The snapshot and tail share the same aggregation code as the existing
`GraphWithFilter`, so HTTP and WebSocket results cannot diverge.

## Architecture

### Transport

- New endpoint `GET /api/events` under the existing `requireAuth` wrapper.
  The handshake is a GET, so the session cookie works; CSRF does not apply.
- Validate the `Origin` header against `r.Host`; reject cross-site handshakes.
- Server sends WebSocket ping frames every 30s. Client reconnects with
  exponential backoff and jitter.
- On session expiry or logout, close with code 4401. The browser WebSocket
  API hides the HTTP status, so the client calls `GET /api/config` on close
  to distinguish 401 from a network failure.
- A session older than `wsMaxAge` (default 7 days) is closed with code 4408;
  the client starts a new lookback session with the selected range.
- Shutdown closes hijacked connections explicitly; `http.Server.Close` does
  not track them.
- Vite dev proxy needs `ws: true` for `/api`.

### Protocol

The subscription is encoded in the WebSocket URL. Reconnecting is the only
control operation, which keeps the server stateless between connections.

```text
GET /api/events?range=1h                 # since = now - 1h
GET /api/events?since=2026-11-06T11:00:00Z  # resume an existing session
GET /api/events?reset=1                  # since = now, zero-based
```

- `since` wins over `range`; `reset=1` wins over both.
- The server clamps a future `since` to now and an old `since` to
  `now - maximum session age` (default 7 days). The first snapshot returns
  the effective `since`.
- The first snapshot returns the chosen `since`, so the client can display it
  and reuse it for a network reconnect.
- Changing the duration selector reconnects with the new `range`.
- A manual reset reconnects with `reset=1`.
- A network reconnect reconnects with the last server-provided `since`.

Server:

```json
{"type":"snapshot","since":"...","to":"...","metrics":{...}}
{"type":"error","error":"..."}
```

Snapshots are **absolute**, not deltas. The client replaces its metrics. This
makes dropped messages, reconnects and late-arriving rows harmless.

### Subscriber aggregation

Each connection owns:

- `since`, `lastRowID`, `lastSnapshotAt`.
- A compact combo map keyed by
  `(route, node, provider, clientModel)`, with request/error/token counters and
  duration samples.
- A ring of the most recent error rows.

At snapshot time, combos are expanded into graph nodes and edges with the
current entrypoint resolver and `TopologyIncludes` filter. Storing the raw
dimensions rather than already-resolved `entrypoint:` keys preserves the
existing query-time attribution: renaming an entrypoint or deleting a route
must affect historical metrics immediately.

P50/P95 are computed from the duration samples every 10s and cached between
recomputes; counts and tokens are pushed every second. This keeps the exact
percentile semantics of the current HTTP endpoint. Cached keys are not
re-materialized on every tick, and error samples are kept in a bounded ring
(200) so the per-second snapshot does not copy an unbounded history.

Duplicate `request_id`s are skipped with a bounded recently-seen set.
`INSERT OR REPLACE` is defensive today; the recorder fires once per
connection.

### UI

- Keep the duration selector with its existing labels; it now means
  "initial lookback" rather than "sliding window".
- Add a **Reset** button next to the selector. It clears the displayed
  metrics, closes the socket and reconnects with `reset=1`; the first
  snapshot is 0. No historical query is made.
- The desktop status badge shows both the last poll time and the session
  start, for example `更新 12:00:02 · 统计自 11:00:00`.
- Mobile keeps the existing compact layout (status badge hidden).
- The stream reconnects automatically after network failures; there is no
  extra Refresh button.

### Config changes

The handler copies the entrypoint/route/node lists under `s.mu` before each
snapshot, exactly like `handleGraph`. The next snapshot reflects a SIGHUP or
admin save without reconnecting.

## Alternatives considered

- **Fixed-since HTTP polling**: smallest change, but the server still scans
  the growing window on every poll and each tab repeats the query. Not chosen.
- **WebSocket plus a server ticker running the full query**: removes per-tab
  queries but not the growing scan. Not enough.
- **Recorder event bus with in-memory buckets**: avoids the DB entirely, but
  duplicates storage/aggregation, needs warm-up and dedup, and loses the DB
  as the single source of truth. Rejected for v1.

## Non-goals

- Changing the Usage page; it remains a manual-refresh report.
- In-flight request visibility; the recorder still fires at completion.
- Multi-instance fan-out; llm-gw is a single process with one SQLite file.
- Approximate percentiles. P50/P95 stay exact.

## Limits

- The initial snapshot still scans `[since, now]`; only reconnects pay this.
- Exact duration samples grow with the number of requests in the session.
  The maximum session age (7 days) bounds it; an older open session is closed
  with 4408 and restarted from the selected lookback. At very high request
  rates a histogram can replace the samples later.
- A reconnecting client's `since` older than the maximum age is clamped to
  that age; the client keeps using the effective `since` returned by the
  server.
- The live tail considers rows with `ts >= since`; the initial snapshot still
  uses `ts < to`, so a row exactly at the snapshot's end is picked up by the
  next tail tick rather than lost.

## Testing

- Go: aggregation parity between HTTP `GraphWithFilter` and the WS snapshot;
  seq tail catches a row inserted with an older `ts`; monotonic snapshots;
  auth/origin rejection; session-expiry close; shutdown.
- Web: pure snapshot reducer tests; reconnect/backoff and reset behavior;
  a reset reconnect uses `reset=1` and shows zero; toolbar reset/status
  rendering. No browser E2E exists in this repo.
- Full gates: `GOWORK=off go build/vet/test ./...`, `npm test`,
  `npx tsc --noEmit`, `npm run build`; rebuild the embedded bundle before the
  final Go build.

## Acceptance criteria

- With a fixed range, request/error/token totals never decrease while the
  stream is connected.
- Pressing Reset clears the display, reconnects with `reset=1`, and the
  first snapshot is zero; counts then grow from zero.
- Changing the range starts a new lookback session and the display reflects
  the new start.
- Late-completing requests are counted in the tick after they are inserted.
- Reconnect resumes the same `since` and catches up without a drop.
- The HTTP fallback, if kept, uses the same fixed-`since` semantics.
- Documentation lists the WS endpoint and reverse-proxy Upgrade requirements.
