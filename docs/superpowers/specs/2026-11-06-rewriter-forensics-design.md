# Rewriter forensics: request IDs, structured errors and body capture

Date: 2026-11-06
Status: design proposed

## Intent

Diagnose rewriter defects that produce client-visible corruption while HTTP
still looks healthy. The three pieces are one workflow:

1. every response carries the request ID that appears in logs and stats;
2. rewriter failures identify the direction, stage, protocols and SSE phase;
3. a bounded capture of the original and rewritten bodies can be enabled on
   demand or for errors, then viewed against that request ID.

The goal is a support loop: client reports a bad response with an ID -> operator
opens that request -> sees what entered the rewriter, what it emitted, and where
it failed.

## Goals

- Add `X-LLM-GW-Request-ID` to every response produced through a node hop.
- Store the same ID as the stats `sid`/`requestId` and show/copy it in the UI.
- Record inbound protocol, rewriter error stage/direction and SSE phase/index in
  stats and structured logs.
- Capture request/response bodies both before and after rewriting, keyed by the
  request ID.
- Capture modes: `off`, `errors`, `sample`, plus time/limit-bounded on-demand
  rules.
- Keep capture off by default; enforce size limits, short retention and admin
  authentication.
- Add a request-detail UI for the structured fields and captured previews.
- Add a small UI/API to start and inspect on-demand captures.

## Non-goals

- Capturing headers (including credentials) or full transport traces.
- Storing full SSE streams; only bounded previews and event counts.
- Changing the data-plane routing or provider adapters.
- Sending a request ID when no HTTP response is produced (request-side rewrite
  failure or connection drop); that remains a later x-layer change.
- Sending a request ID on gateway-generated 502/503 responses that never reach a
  node hop (unmatched entrypoint, unreachable upstream). Those responses do not
  pass through a node-level response-header rewrite. This is a documented v1
  gap; the operator can still locate them by time/model/route.
- Redacting prompts or arbitrary client data inside bodies. Capture is opt-in,
  short-lived and admin-only.
- Metrics/tracing/shadow traffic; those follow this project.

## Constraints

- Do not modify `../x` or any sibling repository.
- The request ID is the existing x session ID (`sid`), which is already stored
  as `requestId` and appears in JSON logs.
- The x HTTP header rewrite plugin supports dynamic header values via metadata
  and no sibling changes are needed.
- SQLite migrations must be additive; old databases keep working.
- Existing configs without a capture block behave as capture-off.
- Replacing `stats.storeBody` is a breaking config change; the field was
  reserved and had no effect. It is removed in favour of `stats.capture`.
- No capture while `stats.capture.mode` is `off` and no rule is active.

## Approaches considered

### A. Capture in the llm-gw rewriter layer (chosen)

The body rewriter sees the exact input and output of every request and response
body, including each SSE event. It can consult the capture policy and buffer only
when needed. It also knows the direction and SSE phase.

Trade-off: more code in `internal/rewriter` and a new `internal/capture` package,
but no cost for requests that are not captured and no x changes.

### B. Reuse x recorder HTTP body capture

Enable `RecorderOptions.HTTPBody` and read `OriginalRequest`/`Request`/
`OriginalResponse`/`Response` in the stats recorder. Less code, but x buffers a
prefix for every request, cannot switch on-demand per request, and exposes no SSE
event information. Rejected.

### Request ID

Use an in-process x header rewriter registered once by the gateway. Every node
adds a response-header rewrite with an empty name (always invoke) and the plugin
name. The plugin reads `sid` from rewrite metadata and appends
`X-LLM-GW-Request-ID`; it fails open (returns the original header block) if the
block cannot be parsed.

Scope: this covers responses that pass through a node hop. Gateway-generated
502/503 responses (no entrypoint match, upstream unreachable before headers) do
not carry the header; covering them would need a service-level response
interceptor or an x change. That is a later step.

## Structured errors

`stats.Fact` and `stats.Request` gain:

- `InboundProtocol`: `openai-chat`, `openai-responses`, `anthropic`, `gemini` or
  empty for an unrecognized URI.
- `ErrorStage`: `request_rewrite`, `response_rewrite`, `stream_rewrite`, or
  empty for non-rewriter failures.
- `ErrorDirection`: `request` or `response`.
- `SSEPhase`: `start`, `event`, `end` when known.
- `EventIndex`: the SSE event index when known.

The body rewriter records the error in Facts before returning it and writes one
structured error log line with `sid`, route, node, provider, inbound/upstream
protocol, direction, URI, SSE phase, event index and error.

`status` keeps its current meaning (recorded HTTP status). The UI presents
`status + errorStage` together and marks any row with `error != ""` as failed.

## Capture model

### Configuration

```yaml
stats:
  capture:
    mode: off          # off | errors | sample
    sampleRate: 0.01   # used when mode=sample
    maxBytes: 32768    # per preview field
    retain: 24h        # capture rows older than this are deleted
```

- `mode: off` -> no buffering unless an on-demand rule matches.
- `mode: errors` -> buffer every request, persist only when the request finishes
  with `status >= 400` or a non-empty error.
- `mode: sample` -> buffer a random sample; persist captured samples.
- On-demand rules are checked first and always persist.
- `maxBytes` bounds each of the four preview fields; defaults are applied in
  config validation.
- `stats.storeBody` is removed.

### On-demand rules

Rules are in-memory, ephemeral and admin-authenticated:

```text
POST   /api/captures   {route?, clientModel?, limit, ttlSeconds, reason}
GET    /api/captures
DELETE /api/captures/{id}
```

A rule matches the current route and client model (empty means any), stops after
`limit` requests or `ttlSeconds`, and returns a reason string shown in the
capture. `clientModel` is a case-insensitive glob (`claude-*` works); `route` is
an exact name. `limit` and `ttlSeconds` must be positive; both are capped by
`capture.MaxRuleLimit` (1000) and `capture.MaxRuleTTL` (1 hour). When several
rules match, the oldest rule wins and is the only one whose counter is consumed.
The manager also admits at most one capture per request ID.

Active rules are in-memory and survive a config reload; they are lost on
restart. A capture-policy change (`mode`, `sampleRate`, `maxBytes`, `retain`)
applies on SIGHUP/admin save through `Gateway.apply`.

### Capture flow

`internal/capture.Manager` owns the policy, rules and per-SID buffers:

- `Start(sid, route, clientModel)` decides on the request direction and returns a
  reason; the decision is remembered for the response direction.
- `Offer(sid, direction, original, rewritten []byte, ssePhase string,
  eventIndex int)` appends bounded previews and hashes. For SSE it keeps the
  first and last events and increments a counter of non-empty input events, not
  the stream.
- On a rewrite error the engine returns no output; `rewritten` is empty and the
  original input is kept alongside `error_stage`/`sse_phase`/`event_index`.
- `Finish(sid)` returns and removes the capture. The stats recorder drops it when
  the reason is `mode:errors` and the request did not fail.
- Memory is bounded by `maxBytes`, a maximum active session count and a total
  byte budget; excess sessions are dropped with a recorded reason.

### Storage

New table, keyed by request ID:

```sql
CREATE TABLE IF NOT EXISTS request_captures (
    request_id TEXT PRIMARY KEY,
    request_original TEXT NOT NULL DEFAULT '',
    request_rewritten TEXT NOT NULL DEFAULT '',
    response_original TEXT NOT NULL DEFAULT '',
    response_rewritten TEXT NOT NULL DEFAULT '',
    sse_first_original TEXT NOT NULL DEFAULT '',
    sse_first_rewritten TEXT NOT NULL DEFAULT '',
    sse_last_original TEXT NOT NULL DEFAULT '',
    sse_last_rewritten TEXT NOT NULL DEFAULT '',
    request_sha256 TEXT NOT NULL DEFAULT '',
    response_sha256 TEXT NOT NULL DEFAULT '',
    sse_events INTEGER NOT NULL DEFAULT 0,
    reason TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL DEFAULT 0
);
```

`request_sha256`/`response_sha256` hash the full logical rewritten
(client-visible) body; for SSE that is the concatenation of non-empty rewritten
response events, without x's added framing. Previews are truncated to `maxBytes`
per field. For SSE, the four `sse_*` fields hold the first and last logical
rewriter events (original and rewritten) and `sse_events` counts non-empty
response-phase input events. Rows are deleted by `capture.retain` during
cleanup.

Captured previews are the bodies visible to the rewriter, not necessarily
byte-identical wire bytes: x skips compressed SSE and chunked/unknown-length
non-streaming bodies over its `MaxChunkSize` (1 MiB default), and adds SSE
framing after rewriting. Those blind spots are documented in the README; raising
`MaxChunkSize` would trade memory for coverage.

`GET /api/requests/{id}` returns an optional `capture` object. The list endpoint
does not load bodies.

## UI

- Requests list: any `error != ""` row gets a red tag and an error marker; the
  existing errors filter keeps working. Add a cheap `hasCapture` flag and a
  "captured" filter so an operator can find on-demand captures without a client
  request ID.
- Request detail: request ID with copy action, inbound/upstream protocol,
  error stage/direction, SSE phase/index, and a capture section with:
  - reason, capture time, SSE event count;
  - original vs rewritten request/response previews side by side;
  - hashes;
  - a clear "capture disabled" state.
- A "Capture" control on the Requests page opens a small modal for route, client
  model, limit, TTL and reason, lists active rules, and allows deletion.
- New strings go through `en-US.ts`.

## Security

- Capture is off by default.
- Captured bodies may contain prompts, tool arguments or user data; retention is
  24h by default and access requires an admin session.
- No header capture, so `Authorization` and API keys are not captured by this
  feature. `log.level: trace` remains a separate header-dump risk.
- Preview size and total memory are capped; a capture rule cannot exceed the
  configured TTL/limit.
- On-demand rules are lost on restart by design; this avoids a forgotten capture
  surviving indefinitely.

## Testing

- `internal/rewriter`: inbound protocol mapping, error stage/direction/SSE phase,
  request ID header plugin adds/overwrites the header and fails open.
- `internal/capture`: policy modes, sampling, rule matching/limits/TTL, SSE
  first/last/count, truncation, memory caps, finish/cleanup.
- `internal/stats`: additive columns, insert/select/scan, capture insert/load,
  cleanup, request detail JSON.
- `internal/admin`: capture CRUD, auth, 503 without a manager, request detail
  includes capture.
- `internal/gateway`: response carries `X-LLM-GW-Request-ID`; stats row uses the
  same value; structured error fields survive to the request row; error-only
  capture drops successful requests and keeps failures.
- Web: request detail rendering, error tag, capture modal/client calls, i18n,
  TypeScript and Vitest.
- Manual: enable `errors`, force a rewrite failure, fetch the request by ID and
  compare original/rewritten bodies; start a rule for one route and confirm only
  that route is captured.

## Acceptance criteria

- Every node-forwarded HTTP response has `X-LLM-GW-Request-ID` equal to the
  stats `requestId`, and the UI can copy it. Gateway-generated 502/503 responses
  are a documented gap.
- A rewriter failure shows stage/direction/protocol in the request row and log.
- With capture off, no capture rows or body buffers are created.
- `errors` captures only failed requests; `sample` captures a bounded sample;
  on-demand rules capture only matching requests and stop at limit/TTL.
- SSE captures store event counts and first/last previews, never the whole
  stream; failed rewrites keep the original input and an empty rewritten
  preview.
- Capture rows are deleted after `capture.retain`; existing databases migrate
  additively.
- All Go/web gates pass; no sibling repository changes.

## Decisions left to the builder

- Exact capture-rule UI placement (Requests toolbar vs Settings) while keeping
  it discoverable.
- Exact memory cap values and eviction policy.
- Whether request/response hashes cover the full body for SSE (the design allows
  either full or preview hashes; document the choice in the UI).
- Test file names and whether to split admin capture tests from request tests.
