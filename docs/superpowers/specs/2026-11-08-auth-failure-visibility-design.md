# Auth failure visibility in the Overview monitor

## Intent

A client rejected by gateway-key authentication currently leaves only a server
log line. The admin console shows nothing, so an operator cannot tell whether a
client is failing because its key is missing, disabled or wrong. Surface recent
failures in the Overview (topology) monitor.

## Goals

- Keep a bounded, in-memory record of recent 401 failures: time, reason
  (`missing` / `disabled` / `invalid`), method, path, client IP and user agent.
- Show a warning panel on Overview when failures fall inside the current monitor
  window, with the latest entries.
- Never store credential values. Bound every client-controlled field.

## Non-goals

- Persisting failures or providing historical queries.
- Attributing an invalid key to a configured key name.
- Recording 401s as request rows, rollups or key usage.
- Changing the Gateway Keys page or the authentication response.

## Constraints

- The data plane may be reachable from the internet. An unauthenticated client
  must not be able to grow memory without bound or write to SQLite.
- `/api/events` already requires an admin session; the new data must not be
  exposed on a public route.
- The WebSocket protocol is additive: older payloads without the field must
  keep working.
- Existing log lines stay unchanged; credentials are never logged or stored.

## Approach

1. New package `internal/authfail` with a mutex-guarded ring buffer (50 entries)
   and a `Snapshot` carrying `recent` (oldest first) plus `truncated` when older
   entries were dropped.
2. `gateway.Gateway` owns one tracker for the process lifetime. The auth handler
   records a failure after `authenticate` rejects, using the already-sanitized
   method/path, the socket IP and a truncated user agent.
3. `admin.Server.SetAuthFailures` receives the tracker from `main`; every
   `/api/events` snapshot carries an optional `authFailures` object.
4. The web client stores the latest snapshot, and `Topology` renders an
   `AuthFailureAlert` panel filtered to the snapshot's `since`. The panel is
   hidden when no failure falls in the window.

## Alternatives considered

- **Record 401s as request rows.** Rejected: an unauthenticated client could
  flood SQLite and distort request/error/usage statistics.
- **Parse server logs from the admin side.** Rejected: fragile and couples the
  console to log formatting.
- **Persist a dedicated failures table.** Rejected for this minimal version;
  it adds retention and migration work without helping live debugging.

## Decisions left to the builder

- Exact panel placement and copy; the plan fixes a React Flow `Panel` at the
  top-left and Chinese source strings with `en-US` translations.
- Ring size (50) and the number of entries shown (5) are constants chosen for
  legibility, not requirements.
