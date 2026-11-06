# Error observability P0 implementation plan

Spec: `docs/superpowers/specs/2026-11-06-error-observability-design.md`
Date: 2026-11-06
Status: in progress

Each task is committed on its own with the implementation record below it.
Tests are run from the `llm-gw` directory unless stated otherwise.

## Task 1: Error summary and class

- Add `UpstreamError` and `UpstreamErrorType` to `stats.Fact`.
- Extend `rewriter.Sink` with `AddUpstreamError`.
- Add `internal/rewriter/errorbody.go`: parse common JSON error envelopes and
  SSE `data:` events, redact credentials, cap at 512 characters.
- Call it from `Rewriter.Rewrite` on response direction, only when the payload
  looks like it may contain an error.
- In `stats.Recorder`, use the fact summary when `ro.Err` is empty, compute
  `error_class`, and persist it.
- Add the `error_class` column and JSON/API fields.
- Tests: extraction table (Anthropic, OpenAI, CommandCode `success:false`,
  plain `message`, SSE error, redaction), recorder class table, store round-trip.

### Implementation record

- Date/status: 2026-11-06, complete.
- Repository + commit: `llm-gw` `be783983`.
- Files changed: `internal/rewriter/errorbody.go` (new), `internal/rewriter/errorbody_test.go` (new), `internal/rewriter/rewriter.go`, `internal/rewriter/rewriter_test.go`, `internal/stats/facts.go`, `internal/stats/errorclass.go` (new), `internal/stats/errorclass_test.go` (new), `internal/stats/recorder.go`, `internal/stats/recorder_test.go`, `internal/stats/store.go`, `internal/stats/store_test.go`.
- Tests/commands and results: `gofmt`; `GOWORK=off go test ./internal/rewriter ./internal/stats -count=1` passed.
- Review findings and fixes: redaction replacement initially dropped the `:` separator; changed the pattern to capture it. `error_class` is derived from status/stage/message; no message means the class still explains 4xx/5xx.
- Deferred items: the rewriter does not see the HTTP status, so a plain `{"message":"..."}` error envelope without `error`, `success:false`, `cause` or `type:error` is not recognized. Response headers are not captured (P1).

## Task 2: Request context

- Persist `uri`, `method`, `client_ip`, `user_agent`, `upstream` from the x
  recorder object.
- Prefer `OriginalURI` for the inbound URI and `ro.DstAddr + ro.HTTP.URI` for
  the upstream.
- Prefer `X-Forwarded-For`/`X-Real-IP` for the client IP; fall back to
  `ro.ClientIP`/`ro.ClientAddr`.
- Add columns, select/scan paths, JSON fields and admin query fields.
- Tests: recorder extraction table, store round-trip, header preference.

### Implementation record

- Date/status: 2026-11-06, complete.
- Repository + commit: `llm-gw` `04035152`.
- Files changed: `internal/stats/requestcontext.go` (new), `internal/stats/requestcontext_test.go` (new), `internal/stats/recorder.go`, `internal/stats/recorder_test.go`, `internal/stats/store.go`, `internal/stats/store_test.go`.
- Tests/commands and results: `gofmt`; `GOWORK=off go test ./internal/stats -count=1` passed.
- Review findings and fixes: the recorder object's client address JSON key is `client`, not `clientAddr`; the test was corrected. IPv6 addresses are handled through `net.SplitHostPort` and a raw fallback.
- Deferred items: `upstream` stores the node IP:port plus forwarded path, not the configured hostname or provider base URL. Response headers remain out of scope (P1).

## Task 3: Configurable upstream header timeout and client status

- Add `gateway.readTimeout`, default 60s, validated to 1s–300s.
- Replace the hard-coded 15s in `internal/gateway/gateway.go`.
- Add `client_status`: 502 when the upstream produced no status and the
  request failed; otherwise the upstream status.
- Classify `gateway_timeout` vs `network`.
- Update README configuration table and example config if present.
- Tests: default/validation, recorder status mapping.

### Implementation record

- Date/status: 2026-11-06, complete.
- Repository + commit: `llm-gw` `ad59c531`.
- Files changed: `internal/config/config.go`, `internal/config/config_test.go`, `internal/gateway/gateway.go`, `internal/stats/store.go`, `internal/stats/recorder.go`, `internal/stats/recorder_test.go`, `internal/stats/store_test.go`, `README.md`.
- Tests/commands and results: `gofmt`; `GOWORK=off go test ./internal/config ./internal/gateway ./internal/stats -count=1` passed.
- Review findings and fixes: `Validate` accepts an unset `readTimeout` so configs validated before `Normalize` keep working; `gateway.New` also falls back to 60s defensively. `status` still means the upstream status; the new `client_status` carries the 502 the client saw.
- Deferred items: read timeouts still do not fail over to the next node; retrying after the request body was sent is a P1/P2 decision.

## Task 4: Stream termination

- Extend `stats.Fact` with `FinishReason`, `SawDone`, `Truncated`.
- In the rewriter, observe response events:
  - Anthropic `message_delta.delta.stop_reason`.
  - OpenAI `choices[].finish_reason`.
  - `[DONE]` sentinel.
- Mark `truncated` when a stream ends without either.
- Add columns, JSON fields and frontend display.
- Tests: event parsing table, recorder truncation rule, store round-trip.

### Implementation record

- Date/status: 2026-11-06, complete.
- Repository + commit: `llm-gw` `de5012e2`.
- Files changed: `internal/rewriter/stream.go` (new), `internal/rewriter/stream_test.go` (new), `internal/rewriter/rewriter.go`, `internal/rewriter/rewriter_test.go`, `internal/stats/facts.go`, `internal/stats/store.go`, `internal/stats/recorder.go`, `internal/stats/recorder_test.go`, `internal/stats/store_test.go`.
- Tests/commands and results: `gofmt`; `GOWORK=off go test ./internal/rewriter ./internal/stats -count=1` passed.
- Review findings and fixes: the parser is only invoked for SSE phases so large non-streaming bodies are not parsed twice. A provider that never sends `stop_reason`/`finish_reason`/`[DONE]` will be marked truncated; documented as a heuristic.
- Deferred items: Gemini and Responses-specific terminal events beyond `stop_reason` are not covered yet.

## Task 5: Frontend

- Extend `web/src/api/types.ts`.
- Show the new fields in the request detail drawer.
- Render `status 0` as `502 网关超时`; show the error class in the list.
- Add a truncated badge.
- Tests: helper/rendering tests where practical, `npm test`, `tsc`, build.

### Implementation record

- Date/status: 2026-11-06, complete.
- Repository + commit: `llm-gw` `7c7421b9`.
- Files changed: `web/src/api/types.ts`, `web/src/locales/en-US.ts`, `web/src/pages/Requests.tsx`, `web/src/pages/Requests.test.ts` (new).
- Tests/commands and results: `npm test` 118 tests passed; `npx tsc --noEmit` passed; `npm run build` passed.
- Review findings and fixes: `errorClassLabel` initially returned raw Chinese labels and bypassed the language switch; it now calls `t()` and the labels have English translations. The test pins `setActiveLang('zh-CN')`.
- Deferred items: the request list still has no filter by error class; that belongs to the P1 aggregation work.

## Task 6: Documentation and memory

- Update README for new request fields and `gateway.readTimeout`.
- Update `.memory/MEMORY.md` and the relevant llm-gw notes with the analysis
  outcome and P0 fields.
- Final full test run.

### Implementation record

- Date/status: 2026-11-06, complete.
- Repository + commit: `llm-gw` `34c932d0`, workspace memory `168845e`.
- Files changed: `llm-gw/README.md`, `llm-gw/docs/superpowers/plans/2026-11-06-error-observability-p0.md`, `.memory/MEMORY.md`, `.memory/notes/llm-gw-error-observability.md`.
- Tests/commands and results: `GOWORK=off go test ./... -count=1` passed; `GOWORK=off go vet ./...` passed; `npm test` 118 tests passed; `npx tsc --noEmit` passed; `npm run build` passed.
- Review findings and fixes: README now documents the diagnostic fields and error classes; memory records the analysis, fixes and the unresolved CommandCode 403 cluster.
- Deferred items: P1 capture/aggregation/alerting; the CommandCode 403 error body is still uncaptured.
