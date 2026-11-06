# Error aggregation P1 implementation plan

Spec: `docs/superpowers/specs/2026-11-06-error-aggregation-p1-design.md`
Date: 2026-11-06
Status: in progress

Each task is committed with its implementation record.

## Task 1: Error summary, alerts and class filter

- Add `stats.ErrorSummary` with totals, byClass, byProvider, byRoute,
  byClientModel, topMessages and hourly buckets.
- Add message fingerprinting and unit tests.
- Add `stats.Alerts` with the four P1 rules and unit tests.
- Add `errorClass` to `stats.Filter` and the SQL where clause.
- Add `GET /api/stats/errors`, `GET /api/alerts`, and `errorClass` parsing in
  `GET /api/requests`.
- Tests: store aggregation, fingerprint, alert thresholds, admin handlers.

### Implementation record

- Date/status: 2026-11-06, complete.
- Repository + commit: `llm-gw` `a3eca549`.
- Files changed: `internal/stats/errorsummary.go` (new), `internal/stats/errorsummary_test.go` (new), `internal/stats/alerts.go` (new), `internal/stats/store.go`, `internal/admin/handlers_stats.go`, `internal/admin/server.go`, `internal/admin/errors_test.go` (new).
- Tests/commands and results: `gofmt`; `GOWORK=off go test ./internal/stats ./internal/admin -count=1` passed.
- Review findings and fixes: aggregation reads raw retained rows and caps every list (50 groups, 20 client models, 20 fingerprints); fingerprints replace UUIDs/IPs/request IDs/hex before grouping. Alerts are computed on demand over 15 minutes; no background evaluator.
- Deferred items: windows beyond `retainRequests` need error rollups (P2); alert thresholds are constants.

## Task 2: Capture headers and error-mode deployment

- Extend `capture.Capture` with request/response header maps.
- Add JSON columns `request_headers` / `response_headers` to
  `request_captures` with migration.
- In `stats.Recorder`, attach the allowlisted headers to a captured row.
- Never store `authorization`, `cookie`, `set-cookie`, `x-api-key`,
  `x-goog-api-key` or `api-key`.
- Update README capture section.
- Tests: allowlist, redaction, store round-trip.
- Deployment note: set `stats.capture.mode: errors`, `maxBytes: 16384`,
  `retain: 24h` in the live config after the image is live.

### Implementation record

- Date/status:
- Repository + commit:
- Files changed:
- Tests/commands and results:
- Review findings and fixes:
- Deferred items:

## Task 3: Frontend error panel

- Add API types and client functions for the error summary and alerts.
- Requests page: error summary card, alert banner, class filter.
- Capture detail: render request/response headers.
- Tests: summary formatting/alert rendering helpers, `npm test`, `tsc`, build.

### Implementation record

- Date/status:
- Repository + commit:
- Files changed:
- Tests/commands and results:
- Review findings and fixes:
- Deferred items:

## Task 4: Documentation and memory

- Update README API and capture docs.
- Update `.memory` notes with P1 fields, endpoints and deployment.
- Final full test run.

### Implementation record

- Date/status:
- Repository + commit:
- Files changed:
- Tests/commands and results:
- Review findings and fixes:
- Deferred items:
