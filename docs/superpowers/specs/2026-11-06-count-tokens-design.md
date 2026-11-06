# Local `count_tokens` for routes without a native endpoint

Date: 2026-11-06
Status: approved 2026-11-06
Repository: `llm-gw`

## Intent

Claude Code calls `POST /v1/messages/count_tokens` to size tool definitions,
skills and conversation fragments before it decides how much to load. Routes
whose upstream speaks Anthropic natively answer this request correctly
(DeepSeek's official Anthropic endpoint does). Routes whose upstream speaks
OpenAI Chat, OpenAI Responses or Gemini have no equivalent endpoint, so the
request reaches the upstream and returns 404.

A 404 is not free. Claude Code falls back to real `max_tokens: 1` probe
requests; field reports describe roughly 22 extra requests and ~35k input
tokens per session start. Some Claude Code versions also map any API 404 to
"the selected model may not exist", which fails the whole turn.

The gateway must answer the request locally when the target has no native
counter, without adding an upstream call, without sending the conversation
anywhere new, and without changing the behavior of routes that already work.

## Goals

- Return `200 {"input_tokens": N}` for `count_tokens` requests whose route
  targets Chat, Responses or Gemini.
- Always return a positive, conservative value. Never return 0.
- Preserve pass-through for routes whose target is Anthropic.
- Make no extra upstream request and incur no token cost.
- Do not store the conversation in stats, captures or the topology graph.
- Require no configuration and no schema migration in v1.

## Non-goals

- Exact tokenizer parity with the target model.
- A runtime DeepSeek tokenizer or any CGO dependency.
- Gemini's native `:countTokens` endpoint.
- A configuration switch or admin-console control (deferred).
- Falling back to an estimate when an Anthropic-compatible upstream returns
  404 for `count_tokens`.
- Exact image, PDF, audio or file token accounting.

## Anti-goals

- Do not replace the x hop group/selector with a custom dispatcher.
- Do not add a synthetic node to the routing graph.
- Do not emit a stats row or a capture entry for a locally answered request.
- Do not return `{"input_tokens": 0}`.
- Do not change the request/response path for `/v1/messages` itself.

## Current behavior and constraints

- `internal/gateway/protocol.go` includes `/v1/messages/count_tokens` in the
  Anthropic inbound group.
- `internal/gateway/upstream.go` rewrites the path only when the target API is
  Anthropic; other targets keep the historical pass-through.
- `internal/rewriter/rewriter.go` substitutes only the top-level `model` for
  Anthropic targets.
- `internal/gateway/protocol_test.go` currently asserts that a Chat target
  passes `count_tokens` through unchanged. That assertion changes by design.
- The GOST rewriter interface is body-only (`Rewrite(ctx, body) ([]byte,
  error)`). `HTTPURLRewriteConfig` rewrites `req.URL.Path`, not the host, so a
  local estimator cannot be reached by URL rewriting.
- The gateway sets `http.keepalive=false`, so node-forwarded responses
  advertise `Connection: close` and compliant clients reconnect. The wrapper
  intercepts the first request per connection; a client that ignores
  `Connection: close` and reuses the connection can bypass the wrapper on later
  requests. Claude Code honors the header.
- The image builds with `CGO_ENABLED=0` and has no local `replace` directives.

## Approaches considered

1. **Handler wrapper (chosen).** Wrap the `forwardlocal` handler: inspect the
   first request line, answer `count_tokens` locally, otherwise replay the
   consumed bytes and delegate. The entrypoint's model glob selects the route,
   and the route's compiled targets decide estimate vs pass-through.
2. **Synthetic local node.** Add a `count_tokens` hop variant that points at a
   loopback estimator. This reuses the selector but introduces a node that is
   not in the config, so it pollutes stats, captures and the graph; it also
   needs a second listener. Rejected.
3. **Always estimate.** No route lookup. This is simpler but overrides the
   exact native count on DeepSeek's Anthropic route. Rejected.

## Design

### Component overview

| Path | Responsibility |
| --- | --- |
| `internal/tokenest/estimate.go` | Pure Anthropic-body token estimator. No I/O, no deps. |
| `internal/gateway/count_tokens.go` | Handler wrapper, request parsing/replay, policy lookup, local response. |
| `internal/gateway/build.go` | Per-route estimate decision and ordered policy table. |
| `internal/gateway/gateway.go` | Wiring, atomic policy swap on reload. |
| `internal/tokenest/testdata/calibration.jsonl` | Regression fixture: text and official token count. |
| `scripts/gen-count-tokens-fixtures.py` | Offline fixture generator; never runs in CI. |

### Interception flow

`countTokensHandler` implements `handler.Handler` and wraps the already
constructed `forwardlocal` handler:

1. Peek the first 5 bytes with a short read deadline.
2. If they are not `POST `, delegate through a `bufferedConn` that drains the
   `bufio.Reader` first. This covers `GET`, TLS bytes and any non-HTTP traffic.
3. Read the request line with `ReadSlice('\n')`. With a 15s deadline. If it is
   not the Anthropic `count_tokens` path (query ignored), replay the consumed
   line plus the buffered reader and delegate.
4. Reconstruct the full request with
   `http.ReadRequest(bufio.NewReader(io.MultiReader(line, br)))`.
5. If `Expect: 100-continue` is present, write `HTTP/1.1 100 Continue` before
   reading the body.
6. Read the body up to 32 MiB. A larger body returns `413`.
7. If `Content-Encoding` is present and not `identity`, delegate (the body
   cannot be parsed; preserve the old behavior).
8. Parse the body as a JSON object. Invalid JSON or a non-object delegates with
   the original body. This preserves the existing pass-through behavior for
   malformed `count_tokens` bodies; the gateway cannot route a body whose model
   it cannot read, and the upstream should produce its own error.
9. Extract the top-level `model`, call `policy.estimateFor(model)`. If false,
   replay the full request with `req.Write` and delegate.
10. Estimate. Write `200`, `application/json`, `Connection: close`, then
    `{"input_tokens": N}` and close the connection.
11. Log at debug level: model, route decision, estimated tokens, body bytes.

`countTokensHandler.Init` delegates to the wrapped handler, so `New` can wrap
before initialization. `countTokensHandler` does not implement
`handler.Forwarder`; `Gateway.forwarder` keeps pointing at the wrapped
handler and `Reload` continues to call `Forward`.

### Replay connection

Delegation must preserve bytes already read from the client:

```go
type bufferedConn struct {
    net.Conn
    r *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) { return c.r.Read(p) }
```

For a request whose line was consumed, `http.ReadRequest` parses the headers and
body; delegation rebuilds the request with `req.Write` after restoring
`req.Body` from the captured bytes, `req.ContentLength`, and clearing
`req.TransferEncoding`. Header order may change; the body bytes are identical.

### Policy table

At config-build time each route gets a boolean:

```go
estimate := false
for _, np := range enabledNodePlans {
    target := np.targets.defaultTarget
    if v, ok := np.targets.variants[provider.Anthropic]; ok {
        target = v
    }
    if target.api != provider.Anthropic {
        estimate = true
        break
    }
}
```

- All enabled nodes Anthropic-capable → pass through.
- Any enabled node without an Anthropic target → estimate for the whole route.
  Mixed routes therefore estimate all `count_tokens` requests; documented.
- No enabled nodes → pass through, preserving the existing routing failure.

The ordered entrypoint list becomes:

```go
type countTokensPolicy struct {
    entries []countTokensEntry
}

type countTokensEntry struct {
    matcher  *regexp.Regexp // nil for a catch-all entry
    estimate bool
}

func (p *countTokensPolicy) estimateFor(model string) bool
```

`estimateFor` returns the first matching entry in entrypoint order, mirroring
routing precedence. A model-less body matches only a catch-all entry. No match
returns false (delegate). The table reuses `modelGlobPattern` from
`protocol.go`.

`Gateway` owns `countTokens atomic.Pointer[countTokensPolicy]`. `apply` stores
the new table together with the hop swap; a failed build leaves the old table
in place. A request racing a reload can use the new policy with the old hop for
a few microseconds; this is harmless because the decision only toggles between
estimate and pass-through.

### Estimator

`internal/tokenest` exposes:

```go
func EstimateAnthropic(body []byte, safetyFactor float64) (int, error)
```

Parsing:

- `system`: a string or an array of content blocks.
- `messages[].content`: a string or an array of content blocks.
- Blocks counted: `text`, `tool_use` (name + input JSON), `tool_result`
  (string or nested blocks), `thinking` text and `redacted_thinking` data,
  `image` (fixed 1600 tokens), `document` (fixed 2000 tokens), unknown blocks
  via their raw JSON.
- `tools[]`: the raw JSON of each tool schema.
- Overheads: 3 tokens per request, 4 per message, 8 per tool.

Text model: additive over runes plus per-run overhead. Six classes:

| Class | Characters | Char coefficient | Run coefficient |
| --- | --- | --- | --- |
| letter | ASCII letters | 0.0817 | 0.4920 |
| digit | ASCII digits | 0.7438 | 0 |
| space | space, tab, CR, LF | 0.2590 | 0 |
| punct | other ASCII | 0.1416 | 0.9205 |
| cjk | Han, Hiragana, Katakana, Hangul | 0.7617 | 0 |
| other | everything else, including emoji | 1.0000 | 0.3490 |

`total = Σ charCoef·chars + Σ runCoef·runs + overheads`, then
`N = max(1, ceil(total · safetyFactor))`. The production safety factor is 1.1.
The coefficients were derived offline; `char_other` is a conservative floor
(the fit preferred 0) so emoji-heavy content is not undercounted.

The exported body estimator is a thin wrapper over an unexported
`estimateText(text string) float64`. The calibration test calls `estimateText`
directly, because the fixture stores raw text and has no message overheads.

The estimator is deterministic, allocation-light and safe for concurrent use.
A 100KB body is expected to take well under a millisecond.

### Response and error shapes

Success:

```json
{"input_tokens": 1234}
```

Errors use Anthropic's error envelope:

```json
{"type":"error","error":{"type":"invalid_request_error","message":"..."}}
```

| Condition | Status |
| --- | --- |
| body over 32 MiB | 413 (safety limit; applies to every route) |
| write failure | return the write error |
| invalid JSON / non-object body | pass through to the upstream |

### Behavior matrix

| Scenario | Behavior |
| --- | --- |
| Route target is Anthropic | Pass through unchanged |
| Route target is Chat / Responses / Gemini | Local estimate |
| Mixed route | Local estimate for all `count_tokens` |
| No entrypoint matches the model | Pass through |
| `?beta=true` query | Same as the bare path |
| Request body compressed | Pass through |
| Malformed JSON body | Pass through unchanged |
| `/v1/messages` or any other path | Unchanged |

### Observability

- One debug log line per local answer: model, tokens, body bytes, decision.
- No stats row, no capture, no topology node. A locally answered request is
  gateway work, not upstream model usage.

### Compatibility

- The only behavior change is for valid `count_tokens` requests that previously
  reached a non-Anthropic upstream and returned 404. Malformed bodies continue
  to pass through, so `TestCountTokensInvalidBodyPassesThrough` keeps passing.
- Bodies over 32 MiB return 413 on every route; this is a new safety limit for
  a pathological input that the gateway now buffers.
- No config field, no YAML schema change, no admin API change, no web change.
- The Docker build stays `CGO_ENABLED=0` with no new modules.
- `TestProtocolMatrix/count_tokens is not converted` is replaced by a test that
  asserts the new behavior for Chat targets; a new subtest asserts Anthropic
  pass-through.

## Testing

### Estimator

- Table-driven cases: empty, English, Chinese, code, JSON schema, emoji, mixed.
- Every result is `>= 1`.
- Monotonicity: adding text never decreases the estimate.
- Safety factor: `EstimateAnthropic(body, 1.1) >= EstimateAnthropic(body, 1.0)`.
- Calibration fixture: text-level MAPE `<= 16%`, at most 8% of samples
  under-estimated, and every sample `>= 1`. The fixture is a regression guard,
  not proof of generalization; coefficients were fit on a 70/30 split of the
  corpus. The fixture stores raw text, so the test calls the text estimator
  directly and does not apply the per-request or per-message overheads.

### Gateway

- Chat target: response is 200 with a positive `input_tokens`; the upstream
  capture is empty.
- Anthropic target: the upstream receives the request and its response is
  returned unchanged.
- Mixed route: estimate.
- `?beta=true`: estimate.
- Invalid JSON: passes through unchanged (Anthropic route, existing test) and
  also passes through on a Chat route (new test asserting the upstream saw it).
- Non-`count_tokens` paths still forward and convert.
- Two sequential requests succeed (the client reconnects because the local
  response closes the connection).
- Reload from a Chat route to an Anthropic route flips the decision.

### Gates

```bash
GOWORK=off go build ./...
GOWORK=off go vet ./...
GOWORK=off go test ./... -count=1
```

No web gate is required because no web source changes.

## Calibration provenance

- Tokenizer: `deepseek_v4_tokenizer.zip` from
  `https://cdn.deepseek.com/api-docs/deepseek_v4_tokenizer.zip` (file date
  2026-08-28; 128k vocab, 127,741 merges).
- Corpus: exactly 141 chunks. 132 come from seven files: `llm-gw` README (37),
  `DOCKER.md` (6), `internal/gateway/build.go` (20),
  `internal/gateway/upstream.go` (11), `internal/rewriter/rewriter.go` (9),
  `llm-api-converter` `README.zh-CN.md` (21) and `README.md` (28). Nine are
  synthetic: six tool schemas and three emoji/mixed samples. Each file is
  chunked at 700 bytes, chunks shorter than 120 bytes are dropped, and runs of
  three or more newlines are collapsed first.
- Split: 70% fit / 30% held out, seed 7.
- Held-out results with the coefficients above and a 1.1 safety factor:
  MAPE 15.3%, 0% under-estimated. Without the safety factor: MAPE 7.1%,
  +4.9% bias.
- Full-corpus results with the coefficients above and a 1.1 safety factor:
  MAPE 14.1%, 4% under-estimated, +13.5% bias, maximum over-estimate 54% on a
  very short emoji-heavy sample. Without the safety factor: MAPE 6.4%, +3.2%
  bias, 32% under-estimated.
- The text-level gate is MAPE `<= 16%` and under-estimation `<= 8%` on the full
  corpus. A pessimistic body-level check that wraps every chunk as a one-message
  request measures MAPE 19.1%; real requests carry many messages, so the fixed
  overhead is proportionally smaller. The fixture is therefore text-level and
  the test calls the text estimator directly.
- The committed fixture contains the text and official counts only; the
  tokenizer file is never committed and the generator never runs in CI.

## Deferred

- `countTokens.mode` config switch and admin-console control.
- Gemini native `:countTokens` mapping (correct tokenizer and response shape).
- Runtime DeepSeek tokenizer if the heuristic proves insufficient in practice.
- Fallback to an estimate when an Anthropic-compatible upstream returns 404.
- Per-model-family calibration (GLM, Kimi, GPT) instead of DeepSeek V4 only.
- Image dimension parsing and DeepSeek's image token formula.
- Exact PDF/audio/file counting.

## Open questions

None.
