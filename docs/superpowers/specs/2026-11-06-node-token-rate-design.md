# Per-node decode token rate from first-response TTFT

Date: 2026-11-06
Status: proposed
Repository: `llm-gw`

## Intent

The Overview monitor shows request count, error share, P50/P95 and token
totals per graph node, but not how fast a node generates output. Operators
want a per-node output token rate (`tok/s`) that is close to model decode
speed, not an end-to-end throughput number diluted by prompt processing and
time to first token.

## Semantics

- Capture **TTFT** (time to first response) for streaming requests.
- `generation_ms = max(duration_ms - ttft_ms, 0)`.
- A request contributes to the rate when `ttft_ms > 0`, `output_tokens > 0`
  and `duration_ms > ttft_ms`.
- Node rate = `sum(output_tokens) * 1000 / sum(generation_ms)` over the
  contributing requests in the current monitor session.
- Only output tokens count; input tokens are prompt processing.
- The rate is an average over the selected lookback session, not an
  instantaneous sample.
- Nodes without any contributing streaming request omit the rate.

## TTFT measurement

The x forward handler sets `ro.Time` at the start of each HTTP request and
`ro.Duration` when the request completes. The body rewriter runs on every SSE
response event, before the event is written to the client, and already
receives the session ID, direction and `sse_phase`.

Add `FirstResponseAt` to `stats.Fact` and a `MarkFirstResponse(sid, at)`
method to the rewriter sink. When `Rewriter.Rewrite` sees a response event
with a non-empty `sse_phase` other than `end`, it records the first timestamp
for that session. The recorder computes
`ttft = FirstResponseAt - ro.Time`, clamps it to `[0, ro.Duration]`, and
stores it only when `fact.Stream` is true.

The first SSE event is usually `message_start` (Anthropic) or a role-only
chunk (OpenAI Chat). It arrives before the first content delta, so this is a
client-visible TTFB-style TTFT, slightly higher than the true first-content
token. That is the accepted trade-off for approach A.

## Schema and aggregation

- Add `requests.ttft_ms INTEGER NOT NULL DEFAULT 0` through `ensureColumns`.
- Add `Request.TTFTMS` and include it in inserts and request SELECTs.
- Add `TTFTMS` to `GraphRow`; `ForEachRequestAfter` and `GraphAggregate` read
  the column.
- Add streaming-only counters to `comboStats`:
  `streamOutputTokens` and `streamGenerationMS`.
- In `Aggregator.Add`, add to those counters only for eligible rows (see
  Semantics).
- Add `OutputTokensPerSecond float64` to `NodeMetrics` (JSON
  `outputTokensPerSecond`). Accumulate the raw counters while expanding
  combos, then compute the rate once per node after expansion. The rate is
  recomputed from the window aggregate on every snapshot; it is not cached
  like percentiles.
- Both the HTTP graph endpoint and the live WebSocket snapshots use the same
  aggregator, so they cannot diverge.

## UI

- Add `outputTokensPerSecond` to the web `NodeMetrics` type.
- Add a `formatTokenRate` helper: one decimal, trailing `.0` removed,
  suffix `tok/s`.
- Binding nodes (`node:`) show the rate as a final segment in the existing
  stats line, for example:
  `10 · 20% · p95 1.5s · 42.3 tok/s`
- The node detail panel shows a labelled `输出速度` row when the rate is
  available. Non-binding nodes and nodes without streaming samples omit both.
- The compact stats line is already ellipsized; the rate is appended last so
  truncation loses it before the request/error/p95 data.

## Alternatives considered

1. **First response event as TTFT (chosen).** Protocol-agnostic, uses the
   existing rewriter sink, one new timestamp and one new column. Slightly
   high TTFT because the first SSE event may be role/start metadata.
2. **First real content delta as TTFT.** Parse OpenAI Chat, Responses,
   Anthropic and Gemini event shapes. More accurate but adds four
   protocol-specific parsers and a larger test surface. Deferred until the
   approach-A numbers prove too low.
3. **Upstream transport timing.** Measure first byte in the x forwarder.
   Most accurate but requires a sibling-repository change; rejected for this
   feature.

## Non-goals

- Non-streaming decode speed. Without a first-token timestamp the generation
  phase cannot be separated from total duration.
- Non-SSE streaming responses (for example chunked Gemini JSON). The x
  forwarder buffers those, so no first-event timestamp exists.
- Backfilling old rows; existing requests keep `ttft_ms = 0`.
- Changing rollups or the Usage page. The graph reads request detail rows.
- Exposing a new admin setting, alert or historical chart.

## Limits

- TTFT is measured at the first SSE event, not the first content delta.
- Very long streams that outlive the 10-minute fact TTL can lose their facts;
  that is a pre-existing recorder limitation.
- A request with a broken or empty stream may be excluded because
  `duration_ms <= ttft_ms` or `output_tokens == 0`.
- The rate is an average over the monitor session and will move as old
  requests age out or the session is reset.

## Testing

- Go: schema migration adds the column; recorder stores clamped TTFT only for
  streams; aggregator excludes ineligible rows and computes the weighted
  rate; HTTP graph and live aggregator parity.
- Rewriter: first response event marks once; `end` and non-response events do
  not mark.
- Web: `formatTokenRate` formatting; `NodeStats` shows the rate only when
  positive; node detail row.
- Full gates: `GOWORK=off go build/vet/test ./...`, `npm test`,
  `npx tsc --noEmit`, `npm run build`; rebuild the embedded bundle before the
  final Go build.

## Acceptance criteria

- A completed streaming request stores a positive `ttft_ms` no larger than
  its `duration_ms`.
- A non-streaming request stores `ttft_ms = 0` and does not affect the rate.
- The Overview node line and detail panel show a plausible `tok/s` value for a
  node with streaming traffic.
- The rate is absent, not `0 tok/s`, when a node has no eligible streaming
  requests.
- Existing databases open without a destructive migration and old requests
  simply have no rate.
