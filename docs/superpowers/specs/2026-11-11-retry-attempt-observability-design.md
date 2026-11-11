# Retry attempt observability — design

Date: 2026-11-11
Repositories: `ginuerzh/cuttle`, `go-gost/x`
Status: proposed

## Intent

Route retries ([[2026-11-10-route-retry-design]]) discard the attempt that
returned `429`/`5xx` and replay the request against the next node. Nothing about
the discarded attempt survives: x returns before recording it, the rewriter never
sees its response body, and the capture manager drops the previous attempt as
soon as the next one starts. The only trace of the failure is `retries: 1` on a
successful row.

Production consequence (2026-11-11): `oc-go-free` was marked `dead` every few
minutes on the `deepseek-flash` route, the topology showed only the badge, and
neither the monitoring panel nor the node's recent sessions could say what the
upstream had actually answered. The answer (429 `rate_limit_error` with a
multi-hour `retry-after`, mixed with 429 `server_error` "inference service is
overloaded") existed only in error captures of the *final* attempts.

Every attempt that causes a retry must be recorded, counted in the monitoring
statistics, and visible in the node's recent sessions.

## Goals

- Record every **discarded** attempt (the ones that triggered a retry): node,
  status, upstream message, error class, duration, bytes in/out, bounded
  response head/body.
- Count them per node and per edge in `/api/stats/graph` and `/api/events`:
  - `attempts` — one per discarded attempt,
  - `retriedRequests` — distinct parent requests that had at least one
    discarded attempt on that node.
  - A status breakdown so `429 × 12` is visible next to the `dead` badge.
- Show them in the node panel's 最近会话 (marked as retry attempts, linked to the
  parent request).
- Make the `dead` badge explain itself: tooltip with the window's failure counts
  by status — both discarded attempts and final failed requests — and the most
  recent one.
- Query them: `GET /api/attempts` with time/node/route/provider/status/error
  filters; the request detail payload carries its attempts.

## Non-goals

- No change to retry policy, selection, marker (`maxFails`/`failTimeout`) or
  `failCodes` semantics.
- No change to `requests` rows, `rollups`, `key_rollups`, `summary`, token
  accounting or usage pages: the parent row stays "one client request".
- No full request/response capture for attempts (bounded preview only).
- No backfill of historical retried requests — old failures are unrecoverable.
- No per-attempt token usage (a rejected attempt is never generated).

## Anti-goals

- Attempts must never inflate `requests`/`errors` aggregates or token totals.
- No unbounded storage: hard caps on body preview and attempts per request.
- No provider-specific error parsing inside x.
- Do not make the recorder fire more than once per client request (that would
  change the contract for every x consumer).

## Constraints from the repository

- x is pinned by tag in `cuttle/go.mod` (currently `x v0.31.1`), no `replace`.
  The new recorder field must be additive so an older x simply yields no
  attempts; cuttle must tolerate an empty list.
- x records exactly once per client request, from the forward handler
  (`x/handler/forward/local/handler.go`), serializing
  `xrecorder.HandlerRecorderObject`. The retry machinery lives in
  `x/internal/util/forwarder`.
- cuttle's `stats.Recorder.Record` maps that object into `stats.Request`;
  `Store.Insert` writes the row and the rollups in one transaction.
- `/api/stats/graph` and `/api/events` stream absolute window snapshots
  (`Store.GraphAggregate` → `Aggregator.Metrics`), so extra counters can be
  merged per snapshot without a new sequence cursor.
- Node panel rows come from `/api/requests?node=…`; the request detail drawer
  from `/api/requests/{id}`.

## Approaches considered

### A. Attempts as sibling rows in `requests` (extra `attempt` column)

One storage path, node panel and graph pick them up for free. Rejected: a dozen
existing queries (`summary`, `rollups`, `key_rollups`, `percentile`, `errors`,
`alerts`, `graph`, captures) would each need to exclude `attempt > 0`; one
missed filter silently doubles the statistics.

### B. Separate `request_attempts` table (chosen)

x ships attempts inside the existing single recorder object; cuttle writes the
parent row and the attempts in the same transaction. Existing semantics are
untouched, attempts get their own query endpoint, and the monitoring counters
are an additive aggregate. Costs one more table, its retention rule and a merge
in the node panel.

### C. `attempts` JSON column on the parent row

Smallest diff, but "which node returned 429 twelve times" is not queryable in
SQL and the node panel could only show a summary line. Rejected by the
requirement to count and list the attempts.

## Design

### x: carry discarded attempts in the recorder object (`go-gost/x`)

New additive fields in `x/recorder/recorder.go`:

```go
// HTTPAttemptRecorderObject describes one upstream attempt that was discarded
// because its response opted into a retry. The final attempt is the recorder
// object itself and never appears here.
type HTTPAttemptRecorderObject struct {
	Attempt        int         `json:"attempt"`
	Node           string      `json:"node"`
	StatusCode     int         `json:"statusCode"`
	Error          string      `json:"error,omitempty"`
	DurationMS     int64       `json:"durationMs"`
	BytesIn        int64       `json:"bytesIn"`
	BytesOut       int64       `json:"bytesOut"`
	ResponseHeader http.Header `json:"responseHeader,omitempty"`
	ResponseBody   []byte      `json:"responseBody,omitempty"`
}

// HTTPRecorderObject gains:
Attempts []HTTPAttemptRecorderObject `json:"attempts,omitempty"`
```

`x/internal/util/forwarder/sniffer_http.go` appends to that slice at the two
places that spend a retry unit:

1. **Retryable status** (the `retryable status %d` branch of `httpRoundTrip`):
   before closing the discarded response, read at most `attemptBodyLimit` (4 KiB)
   from `resp.Body` under the existing `ho.ReadTimeout` deadline, best effort —
   a read error only truncates the preview and must never fail the retry. The
   attempt start time comes from `rs.start` plus the attempt index, or a local
   timestamp captured when the attempt is dialed.
2. **Dial failure** (the pre-dial retry branch in `HandleHTTP`): status `0`,
   `Error` = the dial error, no headers/body.

Both branches record the attempt under the node that served it (`node.Name`),
so cuttle can attribute the failure. Body previews are capped per attempt, and
at most `retry.max` (≤10) attempts are recorded, so the worst case is ~40 KiB
per request.

**Gotcha the builder must handle:** `HandleHTTP` re-creates
`ro.HTTP = &xrecorder.HTTPRecorderObject{...}` at the top of each keep-alive
iteration, and a retry `continue`s into it. The attempts therefore live in a
local slice for the life of one client request and are attached after each
re-init (and reset together with `attempt`/`rs` when the next request is read).

### cuttle: store (`cuttle/internal/stats`)

```sql
CREATE TABLE IF NOT EXISTS request_attempts (
	request_id TEXT NOT NULL,
	attempt    INTEGER NOT NULL,
	seq        INTEGER NOT NULL DEFAULT 0,
	ts         INTEGER NOT NULL,
	route      TEXT NOT NULL DEFAULT '',
	node       TEXT NOT NULL DEFAULT '',
	provider   TEXT NOT NULL DEFAULT '',
	gateway_key TEXT NOT NULL DEFAULT '',
	status     INTEGER NOT NULL DEFAULT 0,
	error      TEXT NOT NULL DEFAULT '',
	error_class TEXT NOT NULL DEFAULT '',
	duration_ms INTEGER NOT NULL DEFAULT 0,
	bytes_in   INTEGER NOT NULL DEFAULT 0,
	bytes_out  INTEGER NOT NULL DEFAULT 0,
	upstream   TEXT NOT NULL DEFAULT '',
	response_headers TEXT NOT NULL DEFAULT '{}',
	body_preview TEXT NOT NULL DEFAULT '',
	PRIMARY KEY (request_id, attempt)
);
CREATE INDEX IF NOT EXISTS request_attempts_seq ON request_attempts(seq);
CREATE INDEX IF NOT EXISTS request_attempts_ts ON request_attempts(ts);
CREATE INDEX IF NOT EXISTS request_attempts_node ON request_attempts(node, ts);
```

`seq` is the **parent's** sequence number, allocated by the same `request_seq`
increment inside `Insert`'s transaction. It lets the live event path tail
attempts with the same watermark it already uses for requests, instead of
re-walking a `ts` window on every 1 s tick.

- `stats.Attempt` mirrors the columns; `stats.Request` gains
  `Attempts []Attempt \`json:"attempts,omitempty"\``.
- `Recorder.Record` maps `ro.HTTP.Attempts` into `stats.Attempt`: node name is
  parsed with the same `parseNodeName` (route/provider/node), `ts` is the parent
  `ro.Time`, `gateway_key` comes from the context, `attempt` from the object.
- Message extraction is cuttle's job (`attemptMessage`): parse the JSON preview
  for `error.message` / `error.type` (also `message`), fall back to the first
  line of the preview truncated to 200 chars; classify with the existing
  `ClassifyError(status, message, "")`.
- `Store.Insert` writes the parent row and the attempts **in the same
  transaction**, and does **not** touch `rollups`. Parent and attempts share the
  parent's `ts`.
- `Store.Cleanup` deletes `request_attempts` older than `requestsBefore`.
- Migration: `CREATE TABLE IF NOT EXISTS` + indexes in `migrate()`; no backfill
  (documented limitation).

### cuttle: counting (`GET /api/stats/graph`, `/api/events`)

Attempts are aggregated on the same two paths the graph already has:

- **Snapshot** (`GraphAggregate`): the existing `seq <= high AND ts >= since`
  read is joined by an attempt read with the same watermark
  (`SELECT … FROM request_attempts WHERE seq <= ? AND seq > 0 AND ts >= ? …`).
- **Live tail** (`ForEachRequestAfter`): a parallel attempt tail
  (`seq > afterSeq`) feeding the same accumulator, so the 1 s tick stays
  incremental and never walks a `ts` window.

Both paths produce, per `(route, node, provider)`:

- `attempts` — number of discarded attempts,
- `retriedRequests` — `COUNT(DISTINCT request_id)`,
- `attemptStatus` — discarded attempts by status (`"429": 12`),
- `failureStatus` — **final** failed parent rows by status, so a `dead` node with
  `retries: 0`, a failure outside `retryCodes`, or a budget-exhausted last
  attempt still has a number to show (`<status>: n`, `"0"` for a failed dial).

`NodeMetrics` gains all four; `EdgeMetrics` gains `attempts` and
`retriedRequests`. Provider graph nodes are aggregated too (the DDL carries
`provider`), so a provider card and the node→provider edge below it agree.
Entrypoint attribution is skipped: an attempt carries no client model.
`GraphMetrics.Errors` is left unchanged — nothing in the console renders it
today (the 最近错误 banner is `/api/stats/errors`, over `requests` only) and
folding attempts into that summary would break its request denominators.

Semantics, fixed here: `attempts` counts each discarded attempt;
`retriedRequests` counts distinct parent requests with at least one discarded
attempt on that node. A request that failed on two nodes therefore counts once
per node, so the sum over nodes may exceed the number of affected client
requests.

### cuttle: query API and UI

- `GET /api/attempts?from&to&node&route&provider&status&errorClass&limit&offset`
  returns attempts (newest first) including `bodyPreview`; `GET
  /api/requests/{id}` includes `attempts` in the detail payload.
- Node panel 最近会话 merges the node's recent attempts into the existing list
  (attempt card: status, message, duration, `重试尝试` marker, click opens the
  parent request detail). The 仅错误 toggle and the empty state apply to both.
- Request detail drawer gains an 上游尝试 section listing each attempt.
- `dead` badge tooltip: `失败 3（429×2, 503×1）· 最后 03:00:16 429` from
  `failureStatus` + `attemptStatus`, plus `重试丢弃 N` when there were discarded
  attempts and their newest attempt. When both sets are empty the tooltip says so
  (`窗口内没有失败记录`) instead of showing `dead` with no number.
- The global 请求 list is unchanged (it stays one row per client request).

## Edge cases and failure behavior

- Only discarded attempts are recorded; the final attempt is the parent row, so
  nothing is double-counted.
- An old x (no `Attempts`) yields an empty list; cuttle behaves exactly as today.
- Attempt preview read failure/timeout: preview truncated, retry unaffected.
- Attempts outside the current topology are dropped by the same `RowFilter` the
  graph applies to parent rows.
- Cleanup keeps attempts for the same window as their parent requests; orphan
  attempts (parent already pruned) are pruned by the same `ts` cutoff.
- Duplicate `(request_id, attempt)` from a resent recorder call: `INSERT OR
  REPLACE` keeps the newest.

## Testing

- x: retry test asserting one recorded attempt per discarded response
  (`status`, node, body preview, duration), a dial-failure retry recording
  status `0`, and a non-retried request recording none. Existing retry tests
  must stay green (`go test -count=1 ./internal/util/forwarder/...`).
- cuttle: store test — parent + attempts in one transaction, `rollups`,
  `summary` and `Count` unchanged; `attemptMessage` and `ClassifyError` cases
  (`rate_limit_error` body, `server_error` body, non-JSON body); graph counter
  test (`attempts`, `retriedRequests`, `attemptStatus`, Errors rows); query API
  filter test; retention test; node-panel SSR test for the attempt card and the
  dead tooltip text.
- Live verification after deploy: the `oc-go-free` 429s appear as attempts with
  `429 × n` next to the dead badge, and the node's recent sessions list them.

## Cross-repository release

The x side must reach a tag before production records attempts, because the
field is only emitted by x. Decided: **cuttle decodes the attempts from the
recorder payload with its own small struct** instead of naming
`xrecorder.HTTPAttemptRecorderObject`. Consequences:

- The cuttle half merges, builds and deploys against today's x `v0.31.1`
  (`GOWORK=off` clean) and records nothing until the x bump lands — the counters
  read zero, the node panel shows no attempt cards.
- x gets its change committed and, when a tag is authorised, published as a
  patch release; cuttle then bumps `go.mod` in a separate, small task (no
  `replace` directives ever).
- Removing the local struct in favour of the x type is optional cleanup after
  the bump; it is not required for correctness.

## Decisions taken here (check these while reading)

- Separate `request_attempts` table, written in the parent's transaction;
  `requests`/`rollups` semantics untouched (this supersedes the "no row per
  attempt" anti-goal of the retry design — the row is outside `requests`).
- Attempts carry the parent's `seq`, so the live event stream tails them instead
  of re-walking a `ts` window every second.
- Attempts travel inside the existing single recorder object: one `Record` call
  per client request, additive JSON field.
- x stores a bounded body preview only; message extraction and classification
  stay in cuttle.
- Fixed 4 KiB preview cap and ≤ `retry.max` attempts per request; no new config
  knobs.
- `retriedRequests` is attributed to the failing node, so per-node sums are not
  mutually exclusive.
- The dead tooltip reports both discarded attempts and final failed requests
  (`failureStatus`); a dead node must never show an unexplained zero.
- cuttle decodes attempts from the recorder JSON with a local struct, so it
  builds and ships before the x tag; the x type is not referenced in cuttle.
- Attempts appear in the node panel, not in the global 请求 list, and
  `GraphMetrics.Errors` / `/api/stats/errors` stay as they are.

## Left to the builder

- Exact Go type/field names beyond the two shown, DDL details and index choice.
- Whether `bodyPreview` is returned by the list endpoint or only detail (payload
  size vs. convenience).
- UI layout of the attempt card and of the attempt section in the detail drawer;
  i18n keys (`重试尝试`, tooltip strings).
- How the node panel merges two lists without reordering jitter.
- Whether `attemptStatus`/`failureStatus` keys are strings or a small struct
  list.
- The accumulator's shape (one struct per bucket vs. parallel maps) and the
  tests that pin it.
