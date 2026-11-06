# Error observability: production analysis and P0 design

Date: 2026-11-06
Status: accepted for P0 implementation

## 1. Why

Production `llm-gw` recorded 87 real error requests between 06:54 and 13:12 UTC
on 2026-11-06. 73 of them (84%) carried only an HTTP status code: the frontend
detail drawer had nothing to show but `401`, `403`, `404`, `429`, `550` or
`0`. Attribution required out-of-band reproduction and provider documentation.

This document records the analysis, the confirmed defects, and the P0 scope for
making every error explain itself without storing full request bodies.

## 2. Production analysis

Data source: `/data/stats.sqlite`, 1182 rows, 06:54–13:12 UTC. Eleven rows were
diagnostic probes issued during the investigation and are excluded. The
remaining 1171 production requests contain 87 errors (7.4%).

### 2.1 Distribution

| Status | Count | Main source | Attribution |
| --- | ---: | --- | --- |
| 401 | 20 | DeepSeek direct 11, OpenCode Zen 9 | gateway bug, fixed |
| 429 | 17 | OpenCode Go 14, Zen 3 | upstream quota/rate limit |
| 403 | 16 | CommandCode 12, OpenCode Go 4 | upstream refusal/entitlement |
| 0 | 10 | OpenCode Go 7, CommandCode 3 | gateway 15s header timeout |
| 404 | 10 | OpenCode free 7, DeepSeek 3 | unknown paths / probes |
| 400 | 7 | CommandCode 6, OpenCode Go 1 | large body/context or bad request |
| 200 + error | 4 | OpenCode Go 4 | upstream ~32s stream cut |
| 550 | 2 | DeepSeek 2 | upstream edge error |
| 503 | 1 | CommandCode 1 | upstream overloaded |

| Provider | Requests | Errors | Rate | Silent |
| --- | ---: | ---: | ---: | ---: |
| deepseek-dadax | 302 | 16 | 5.3% | 16 |
| commandcode | 672 | 22 | 3.3% | 19 |
| opencode-go | 185 | 37 | 20.0% | 26 |
| opencode-zen | 12 | 12 | 100% | 12 |

Error rate before the credential fix (before 10:19:45): 59/390 = 15.1%.
Error rate after it: 28/781 = 3.6%.

### 2.2 Confirmed gateway bug: inbound credential leak (fixed)

Twenty 401s (11 DeepSeek direct, 9 OpenCode Zen) happened when Claude Code sent
its own `Authorization` and `x-api-key`. Upstreams prefer `Authorization` over
`x-api-key`, so the client credential overrode the provider key.

Direct reproduction:

- DeepSeek Anthropic endpoint, valid `x-api-key` + fake `Authorization`:
  `401 Authentication Fails, Your api key: ****fake is invalid`.
- Same endpoint, valid `x-api-key` only: `200`.
- OpenCode Zen, valid key plus fake `Authorization`: `401 Invalid credential`.
- OpenCode Zen, valid `Authorization` plus fake `x-api-key`: key accepted.

Fix: `694deb53 fix(gateway): drop inbound credentials before upstream auth`,
which clears `Authorization`, `x-api-key`, `x-goog-api-key` and `api-key`
before applying provider auth. Post-fix 401 count is zero.

### 2.3 Confirmed gateway defect: 15s response-header timeout (open)

Ten rows recorded `status 0` with `read tcp ...: i/o timeout`, all
non-streaming, 15.4–19.5 seconds, request bodies 147KB–3.4MB. The gateway
hard-codes `readTimeout: "15s"` in `internal/gateway/gateway.go`; x uses it as
the deadline for reading upstream response headers. Only two non-streaming
requests ever exceeded 15s successfully, confirming a hard ceiling. The timeout
does not fail over to the next node; the request simply fails.

### 2.4 Upstream limitations (not gateway bugs)

- OpenCode Go cuts long generations at ~32s. Four rows: three
  `unexpected EOF`, one `unmarshal stream chunk: unexpected end of JSON input`
  at `event_index=397`. One 30.1s 200 response may be a silent cut.
- OpenCode Go/Zen `429`: quota or rate limit; Zen free usage now returns
  `429 FreeUsageLimitError`.
- OpenCode Go `403`: endpoint unavailable/region/entitlement.
- CommandCode `503`: `overloaded_error`.
- CommandCode `400` x6: 2.99–3.07MB bodies, consistent with a context budget
  overflow; the same body also timed out or 429ed on OpenCode Go.
- CommandCode `403` x12: paired `claude-opus-4-7`/`claude-sonnet-4-6`
  requests, 48–72KB, same target model as successful traffic. Likely
  content-policy or upstream refusal; the body is required to confirm.
- Ten 404s: seven tiny OpenCode free requests (probes or unknown paths) and
  three large DeepSeek requests (likely a wrong client path).

### 2.5 Observability gaps

1. 84% of errors have no message; the converter only recognizes `error` and
   misses `success:false`, `message`, `cause`, plain text and HTML.
2. No URI, method, upstream URL, client IP or user agent.
3. No upstream response headers (`Retry-After`, `x-request-id`, `cf-ray`,
   rate-limit headers).
4. Capture is off, covers only rewriter bodies, and cannot catch unknown-path
   404s or `status 0` timeouts.
5. No `finish_reason`/`[DONE]`/truncated marker.
6. `status 0` is displayed as `0` although the client received a 502.
7. No error classification, fingerprint, aggregation or alerting.

## 3. P0 scope

P0 makes errors explainable using scalars only. It does not store full bodies
by default.

### 3.1 Error summary and class

- Parse common error envelopes from response bodies and SSE error events:
  `error.message`, `error` (string), `message`, `cause`, `type`, `code`.
- Redact credentials, bound to 512 characters, store in the existing `error`
  column when `ro.Err` is empty.
- Add `error_class`: `upstream_auth`, `upstream_forbidden`, `upstream_quota`,
  `upstream_bad_request`, `upstream_unavailable`, `upstream_truncated`,
  `gateway_timeout`, `network`, `rewrite_error`, `client_error`, `unknown`.
- The rewriter reports the upstream summary through `stats.Sink`; the recorder
  combines it with the HTTP status.

### 3.2 Request context

Add and persist:

- `uri`: inbound URI (`OriginalURI` when a rewrite happened).
- `method`.
- `client_ip`: `X-Forwarded-For`/`X-Real-IP` first value, else client address.
- `user_agent`.
- `upstream`: node address plus the forwarded URI.

These are already present in x's recorder object; the stats recorder must keep
them and the store must add columns.

### 3.3 Configurable upstream header timeout

- Add `gateway.readTimeout` (duration, default 60s, 1s–300s).
- Use it instead of the hard-coded 15s.
- On `status 0` with an error, record `client_status = 502` and
  `error_class = gateway_timeout` (or `network` for non-timeout errors).

### 3.4 Stream termination

- Record `finish_reason` from Anthropic `delta.stop_reason` or OpenAI
  `choices[].finish_reason`.
- Record `saw_done` when an SSE `[DONE]` sentinel is observed.
- Mark `truncated` when a stream ends successfully without either.
- Display a "upstream truncated" badge in the request detail.

### 3.5 Frontend

- Show `error_class`, `uri`, `method`, `client_ip`, `user_agent`, `upstream`,
  `finish_reason` and `truncated`.
- Render `status 0` as `502 网关超时` and surface the error class in the list.

## 4. Non-goals

- Storing full bodies for every error (P1).
- Error alerting (P1).
- Injecting a fallback `x-opencode-session` (P2).
- Rejecting unknown paths locally (P2).

## 5. Decisions

- Error extraction lives in the rewriter because that is the only place that
  sees the raw upstream body without enabling recorder body capture.
- The stats recorder remains the single writer of request rows.
- New columns are additive and migrated with `ensureColumns`; existing rows
  keep empty values.
- `status` keeps its existing meaning (upstream HTTP status, 0 when none).
  `client_status` carries what the client actually received.
