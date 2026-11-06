# Error aggregation, alerts and automatic capture (P1) design

Date: 2026-11-06
Status: accepted for P1 implementation
Builds on: `2026-11-06-error-observability-design.md` (P0)

## 1. Why

P0 made individual errors explainable. The next gap is seeing the shape of
failures over time and keeping the full forensic context when an unfamiliar
upstream error appears. Production still runs `capture.mode: off`.

## 2. Goals

- Aggregate errors by class, provider, route and client model over a time
  window.
- Show the top error messages with a stable fingerprint so repeats collapse.
- Surface current-window alerts (auth failures, error-rate spike, gateway
  timeouts, truncation rate) without an external push channel.
- Capture full bodies for failed requests automatically, with a header
  allowlist and credential redaction.
- Keep all new queries bounded by the existing request retention window.

## 3. Non-goals

- External webhook/email/PagerDuty delivery. P1 exposes alerts through the
  admin API and UI only.
- Per-attempt failover history. The stats row still describes the final node.
- Capturing every successful request body.

## 4. Aggregation

New `stats.ErrorSummary(ctx, from, to)` returns:

- `total`, `errors`, `errorRate`.
- `byClass`: `{key, requests, errors}` for every non-empty `error_class`.
- `byProvider`: `{key, requests, errors}`.
- `byRoute`: `{key, requests, errors}`.
- `byClientModel`: top 20 client models by errors.
- `topMessages`: top 20 `(error_class, message)` fingerprints with counts and
  last-seen time.
- `hourly`: requests/errors per hour in the window.

Fingerprint: lowercase, collapse whitespace, replace UUIDs, IPs, request IDs
and long hex tokens with placeholders. This keeps `request_id: ...` variants
from splitting the list.

API: `GET /api/stats/errors?from=&to=` returns the payload.
`GET /api/requests` gains `errorClass` filtering.

## 5. Alerts

New `stats.Alerts(ctx, now)` evaluates the last 15 minutes and returns a list
of `{id, severity, title, detail, count, since}`:

- `auth`: any `upstream_auth` error in the window (critical).
- `error_rate`: error rate > 20% with at least 10 requests (warning).
- `gateway_timeout`: any `gateway_timeout` error (warning).
- `truncation`: `upstream_truncated` errors > 3 (warning).

API: `GET /api/alerts`. The Requests page shows an Alert banner when the list
is non-empty. Thresholds are constants in P1; configurable rules are P2.

## 6. Automatic capture and headers

- Capture remains opt-in globally via `stats.capture.mode: errors`.
- The deployment will set `mode: errors`, `maxBytes: 16384`, `retain: 24h`.
- Extend `capture.Capture` with `requestHeaders` and `responseHeaders` maps.
- The stats recorder extracts only an allowlist from the recorded HTTP
  headers: `content-type`, `user-agent`, `retry-after`, `x-request-id`,
  `x-opencode-session`, `cf-ray`, `anthropic-ratelimit-*`, `x-ratelimit-*`.
  Authorization, cookie and API-key headers are never stored.
- Persist the maps as JSON text in `request_captures`; existing rows get empty
  objects.

## 7. UI

- Requests page: an error summary card with total/error rate, by-class tags,
  top messages, and an alert banner.
- Error class filter reuses the existing filter row and refreshes the list.
- Capture detail shows the allowlisted request/response headers.

## 8. Decisions

- Alerts are computed on demand from raw request rows; no background evaluator
  and no new storage. This keeps P1 deterministic and avoids a delivery
  channel decision.
- Aggregation uses raw rows (7-day retention) rather than new rollups. If the
  window grows beyond retention, add error rollups in P2.
- Header capture lives in the stats recorder because it already receives the
  full recorder object after the response.
