# Gateway keys and per-key usage

Date: 2026-11-07
Status: draft

## Intent

The llm-gw data plane currently has no client authentication. Every request
that reaches the gateway port is forwarded with the operator's provider keys;
inbound credentials are deliberately discarded. Anyone who can reach the port
can spend the upstream quota.

Operators need to:

1. hand a client (Claude Code, an SDK, a teammate) a gateway-owned key instead
   of exposing provider keys or opening the gateway to everyone;
2. revoke one client without affecting the others;
3. see how much each client used over 24 hours, 7 days and 30 days.

The gateway key is an **access credential for the data plane**, separate from
the admin password. It is not an upstream provider key and is never forwarded
upstream.

## Goals

- Add named gateway keys to the configuration and the admin console.
- Require a valid gateway key on every data-plane request when at least one key
  is configured.
- Accept the credential shapes real clients already send:
  `Authorization: Bearer`, `x-api-key`, `x-goog-api-key` and `api-key`.
- Keep the current open behavior when no keys are configured.
- Attribute every recorded request to the key that authenticated it.
- Show per-key usage (requests, errors, input/output/cached tokens) for 24h,
  7d and 30d, and let operators drill into that key's recent sessions.
- Make rotation and disable take effect on the next request without a restart.
- Preserve the existing routing chain, request rewriting and provider auth.

## Non-goals

- Per-key entrypoint, route, model or provider restrictions. The config shape
  keeps a key entry extensible, but v1 keys can use every entrypoint. This is
  the agreed follow-up.
- Per-key rate limits, quotas, budgets or billing.
- Read-only or scoped admin keys. Gateway keys never authenticate the admin API.
- Key expiry, scheduled rotation or automatic revocation.
- Query-string credentials (for example Gemini's `?key=`). Query strings are
  recorded in stats and proxy logs, so v1 accepts headers only.
- Recording rejected (401) requests as usage rows. They are logged, not
  persisted.
- Per-key historical backfill. Rows written before this change have no key.

## Constraints

- The routing chain is fixed: entrypoint (body `model` glob) → route → node →
  provider model. Entrypoint selection happens inside x's forward handler.
  Authenticating before that stage means the auth layer cannot know which
  entrypoint a request will match.
- `gateway.keys` must not leak through `GET /api/config`, `GET /api/gateway-keys`
  or the web bundle.
- `internal/admin.Server.clone()` round-trips the config through JSON. A field
  tagged `json:"-"` on a gateway key would be lost on every admin mutation.
  `GatewayKey.Key` therefore needs a JSON tag for the internal clone, while
  every public view must construct an explicit masked shape.
- `config.Load` uses `yaml.KnownFields(true)`, expands the environment and then
  validates. The admin API must not re-expand `$` in a literal key.
- Admin mutations apply the new config to the data plane before writing the
  file; a failed write can leave memory ahead of disk. Gateway key updates
  follow the existing behavior.
- The stats store is additive: old SQLite files must keep working without a
  destructive migration, and old rows read as "no key".
- Request detail retention defaults to 168h; 30-day usage must come from
  hourly rollups, not from the `requests` table.

## Approaches considered

### A. Global named keys plus key-dimension rollups (chosen)

`gateway.keys` is a list of `{name, key, enabled}`. A new auth handler in front
of the existing forward handler authenticates the request and stores the key
name in the request context; the stats recorder persists it. A separate
`key_rollups` table keeps 30-day usage accurate. The admin console gets a keys
page and a usage endpoint.

Trade-off: every key has the same data-plane permissions, and one more rollup
table must be maintained. No routing, body or upstream-header logic changes.

### B. Entrypoint-scoped keys

Same as A, but each key lists the entrypoints it may use. Because the
entrypoint is chosen inside the forward handler after the body's `model` is
parsed, enforcement would require a second body parse and a copy of the
entrypoint matcher before forwarding. That duplicates routing logic, interacts
with the count_tokens body handling and can drift from x. Deferred until
multi-tenant isolation is a real requirement; the context-based key identity
added by A is the extension seam.

### C. YAML-only keys with request-detail stats

`gateway.keys` plus a `gateway_key` column and a filter on the Requests page.
No admin CRUD and no key rollups. Smallest change, but rotation needs a file
edit plus SIGHUP, and 30-day usage is impossible because request rows are
retained for 7 days. Rejected: it does not meet the usage requirement.

## Configuration

```yaml
gateway:
  listen: :8787
  keys:
    - name: claude-code
      key: ${CLAUDE_GATEWAY_KEY}
      enabled: true
    - name: laptop-sdk
      key: sk-gw-...
      enabled: false
```

Schema:

```go
type Gateway struct {
    Listen      string       `yaml:"listen" json:"listen"`
    ReadTimeout Duration     `yaml:"readTimeout" json:"readTimeout"`
    UserAgent   string       `yaml:"userAgent,omitempty" json:"userAgent,omitempty"`
    Keys        []GatewayKey `yaml:"keys,omitempty" json:"keys,omitempty"`
}

type GatewayKey struct {
    Name    string `yaml:"name" json:"name"`
    Key     string `yaml:"key" json:"key,omitempty"`
    Enabled *bool  `yaml:"enabled,omitempty" json:"enabled,omitempty"`
}
```

Rules:

- `name` is required, trimmed, at most 64 characters, must not contain `/` or
  control characters, and must be unique in the list.
- `key` is required, trimmed, 8–512 characters, and must be unique across the
  list. Duplicate values are rejected without echoing the value.
- `enabled` defaults to true. A disabled entry stays configured but never
  authenticates.
- No entries (absent or `[]`) means authentication is **off**, matching current
  behavior.
- At least one entry means authentication is **on**. If every entry is
  disabled, every data-plane request is rejected with 401; the gateway logs a
  warning at apply time.
- Key values participate in `config.Load` environment expansion. Admin writes
  use `ApplyDefaults` and do not expand literal `$`.
- The generated first-run config does not add keys, so a fresh install keeps
  the current open behavior until the operator adds one.

## Data plane

### Handler chain

```text
hello handler (public)
  -> gateway auth handler
    -> count_tokens handler
      -> x forward handler (existing routing and upstream auth)
```

- `GET|HEAD /api/hello` stays public: it is Claude Code's connection probe and
  never reaches an upstream.
- Everything else, including Anthropic `count_tokens`, requires a valid key
  when keys are configured.
- When no keys are configured the auth handler is a pass-through; it does not
  parse the request.

### Request authentication

The auth handler wraps the connection and reads only the HTTP request line and
headers (raw bytes, bounded to 1 MiB, 15s read deadline), then replays the
exact bytes plus the remaining buffered stream into the next handler. It never
buffers the body.

Candidate secrets, collected from all of these headers:

1. `Authorization: Bearer <value>` (case-insensitive scheme, trimmed)
2. `x-api-key`
3. `x-goog-api-key`
4. `api-key`

The rule is **any candidate may authenticate**: a request is accepted when at
least one candidate matches an enabled key. A junk value in one header does not
veto a valid value in another, which is the normal Claude Code / SDK case.
When several candidates match, attribution goes to the earliest matching key in
configuration order.

The compiled policy retains SHA-256 digests for disabled entries as well, with
an `enabled` flag. Every candidate is compared with
`crypto/subtle.ConstantTimeCompare` against every entry (enabled and disabled)
without early exit, so timing does not reveal the matching position. Only an
enabled match authenticates. The failure reason is:

- `missing` when no supported credential header is present;
- `disabled` when at least one candidate matches a disabled entry and none
  matches an enabled entry;
- `invalid` otherwise.

On success the handler calls the next handler with
`stats.WithGatewayKey(ctx, name)`. The recorder reads that value from the
handler context; no internal header is added to the request, so there is no
risk of forwarding it upstream.

On failure:

- status 401, `Content-Type: application/json`,
  `WWW-Authenticate: Bearer realm="llm-gw"`, `Connection: close`;
- body `{"error":{"message":"invalid gateway key","type":"authentication_error"}}`;
- client IP, method and path are logged at warn level with the reason
  above — the credential value is never logged;
- the upstream is not called.

A malformed HTTP head while authentication is required returns 400 and closes
the connection. There is no unauthenticated fallback.

### Credential handling upstream

Unchanged: `buildNodes` still clears `Authorization`, `x-api-key`,
`x-goog-api-key` and `api-key` before applying provider auth. The gateway key
is not added to any request header.

### Reload

`Gateway` holds an `atomic.Pointer[authPolicy]`, like the count_tokens policy.
`Gateway.apply` compiles the policy from `cfg.Gateway.Keys` and stores it after
the new hop group builds successfully. SIGHUP and admin mutations therefore
update keys with the same "old config keeps serving on build failure"
semantics. In-flight requests keep the identity validated at admission.

## Statistics

### Request rows

- Add `gateway_key TEXT NOT NULL DEFAULT ''` to `requests`, via `ensureColumns`.
- Add index `idx_requests_gateway_key_ts ON requests(gateway_key, ts)`.
- Add `GatewayKey string json:"gatewayKey,omitempty"` to `stats.Request`.
- `Insert`, `Query`, `Get` and `scanRequests` carry the column.
- `Filter` gains `GatewayKey`; `where()` adds `gateway_key = ?`.
- `GET /api/requests` accepts `key=<name>` and maps it to the filter.
- The recorder sets the value from the context. When the context has no key
  (open mode), the field stays empty and is omitted from JSON.

### Key rollups

```sql
CREATE TABLE IF NOT EXISTS key_rollups (
    bucket TEXT NOT NULL,
    gateway_key TEXT NOT NULL DEFAULT '',
    requests INTEGER NOT NULL DEFAULT 0,
    errors INTEGER NOT NULL DEFAULT 0,
    input_tokens INTEGER NOT NULL DEFAULT 0,
    output_tokens INTEGER NOT NULL DEFAULT 0,
    cached_tokens INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (bucket, gateway_key)
);
```

- `Insert` upserts one row in the same transaction as the existing `rollups`
  write, only when `GatewayKey != ""`.
- Bucket format is the existing hourly RFC3339 UTC string.
- `Cleanup` deletes `key_rollups` using the same cutoff as `rollups`.
- No backfill. Pre-upgrade usage appears as zero per key.
- A `stats.retainRollups` value shorter than the selected range under-reports,
  exactly like the existing provider Usage page. There is no new minimum or
  clamp; the default `2160h` covers the 30-day view.
- Rejected 401 requests are not inserted.
- Locally estimated `count_tokens` responses are not recorded today, so they
  produce no key usage either.

### Key usage query

`Store.KeyUsage(ctx, from, to)` aggregates `key_rollups` by `gateway_key` and
returns the five counters. The admin layer merges that with the current key
list:

- configured keys appear in configuration order, including zero usage;
- key names with usage but no current configuration are appended
  alphabetically and marked `configured:false`, so deleting a key does not
  erase its history;
- totals sum every returned row.

## Admin API

All routes are under the existing authenticated `/api/` mux.

### `GET /api/gateway-keys`

Returns the configured keys in configuration order:

```json
[
  {"name":"claude-code","keyPreview":"sk-...abcd","enabled":true}
]
```

The raw key is never returned. Empty results are `[]`.

### `POST /api/gateway-keys`

Request: `{"name":"claude-code","enabled":true}`.

The server generates 32 random bytes, encodes them with base64url and prefixes
`sk-gw-`. Response: `201 Created` with the only occurrence of the secret:

```json
{"name":"claude-code","key":"sk-gw-..."}
```

Name and key validation failures return 400 with a message that does not
include the key. Duplicate names are rejected. If the generated value collides
with an existing key, regenerate (bounded retries, then 500).

### `PUT /api/gateway-keys/{name}`

Request: `{"enabled":false}`. Only the enabled flag is mutable; a body `name`
that differs from the path is rejected. Returns `204 No Content`. Key values
cannot be replaced here, so a GET-then-PUT client can never overwrite a secret.

### `POST /api/gateway-keys/{name}/rotate`

Generates and stores a new secret in one mutation. Returns `200 OK` with
`{"name":"claude-code","key":"sk-gw-..."}`. The old value stops working as
soon as the reload applies. Used when the one-time create response was lost or
a credential leaked.

### `DELETE /api/gateway-keys/{name}`

Returns `204 No Content`. Removes the key from the config; historical
`key_rollups` rows stay until their normal retention deadline.

### `GET /api/gateway-key-usage?from=&to=`

```json
{
  "from": "2026-11-06T00:00:00Z",
  "to": "2026-11-07T00:00:00Z",
  "totals": {"requests":12,"errors":1,"inputTokens":1000,"outputTokens":2000,"cachedTokens":500},
  "keys": [
    {"name":"claude-code","configured":true,"enabled":true,"keyPreview":"sk-...abcd",
     "requests":12,"errors":1,"inputTokens":1000,"outputTokens":2000,"cachedTokens":500},
    {"name":"old-key","configured":false,"enabled":false,
     "requests":3,"errors":0,"inputTokens":10,"outputTokens":20,"cachedTokens":0}
  ]
}
```

Missing `from`/`to` use the existing 24-hour range default. Returns 503 when
stats are disabled, matching the other stats endpoints. The list is never
`null`.

### `GET /api/config`

`gateway` gains `keysSet` (number) and `authRequired` (boolean, true when the
list is non-empty). It still exposes no key values. `PUT /api/config` preserves
the existing `gateway.keys` when the decoded list is nil (field absent) and
clears it only when the request explicitly sends `[]`. A non-empty list
replaces the whole key set: this is an advanced escape hatch, consistent with
the existing ability to set provider keys through the same endpoint. The
values are validated like a file load, and error messages never echo a key
value. Because `GET /api/config` never returns values, a read-modify-write
client must omit `gateway.keys` to preserve them.

## Admin console

- New navigation page `网关密钥` (`gateway-keys`) between Entrypoints and
  Sessions.
- Header: title, range selector (24h / 7d / 30d), refresh, Add.
- When no keys are configured, an info alert explains that the data plane is
  currently unauthenticated and shows the Add action.
- When keys exist but all are disabled, a warning alert explains that every
  request will be rejected.
- One `ListCard` per key:
  - name, masked preview, enabled switch;
  - requests, errors, input/output/cached tokens for the range;
  - actions: Sessions, Rotate, Delete.
- Sessions opens a drawer with up to 100 recent sessions for that key, using
  `GET /api/requests?key=<name>`.
- Add asks for a name and enabled state, then shows a one-time secret dialog
  with a copy button and a clear "this value is shown once" warning.
- Rotate confirms, then shows the same one-time secret dialog.
- Delete confirms; history remains visible as an unconfigured key.
- Requests page gains a `网关密钥` filter input, includes `gatewayKey` in list
  subtitles, and shows it in the request detail.
- All new strings go through `t()` with both `zh-CN` and `en-US` entries.
- `api/types.ts` gains `GatewayKey`, `GatewayKeyUsage` and `gatewayKey` fields.

## Security

- Keys are stored in plaintext in YAML, like provider keys, so the masked view
  and rotation remain usable. Config files are written with mode `0600`.
- Raw keys never appear in admin responses except the create/rotate one-time
  body, never in logs, stats, captures or the web bundle.
- SHA-256 plus constant-time comparison avoids bcrypt's per-request cost and
  length-dependent comparison. Keys must have at least 8 characters; generated
  keys have 256 bits of entropy.
- The 401 body does not distinguish missing, invalid and disabled keys, and is
  the same for every protocol. `WWW-Authenticate` is a generic Bearer realm.
- Query-string credentials are rejected by omission, avoiding key leakage into
  `stats.requests.uri` and reverse-proxy logs.
- No per-IP rate limiting in v1. Failed admissions are logged, and the admin
  can rotate a leaked key.
- Admin session cookies and gateway keys are independent credential systems.

## Compatibility and migration

- Existing configs load unchanged; no keys means no behavior change.
- Existing SQLite databases gain a column, an index and a table in place.
  Old rows have an empty key and no key rollups; no destructive migration.
- Existing admin clients keep working. `PUT /api/config` without a `gateway.keys`
  field preserves keys.
- `example.yaml` gains a commented keys block so the default behavior stays
  open.
- README and AGENTS.md document the new config, API, stats and UI behavior.

## Future extension: entrypoint binding

`GatewayKey` is a struct, so adding `Entrypoints []string` later is additive
and old configs keep loading. The auth handler already resolves a key identity
before routing and places it in the context, so enforcement can be added at the
stage that knows the matched entrypoint without re-checking the credential.
That work is explicitly out of v1.

## Testing

- Config: load with keys, env expansion, defaults, duplicate name/value,
  empty/too-short/too-long key, disabled entries, `KnownFields`, example.yaml.
- Gateway auth: open mode, each accepted header, Bearer scheme handling,
  invalid/missing/disabled keys, 401 body and header, `/api/hello` exemption,
  count_tokens protection, upstream never sees the gateway key, multiple keys
  attribute correctly, reload/rotation takes effect, no-body buffering.
- Stats: migration from an old schema, insert writes key and key rollups,
  filter by key, `KeyUsage` aggregation and range boundaries, cleanup.
- Admin: list masks, create returns a secret once and it authenticates,
  create without a key is generated, duplicate rejection, enable/disable,
  rotate invalidates the old value, delete keeps history, config GET does not
  leak, config PUT preserves keys when absent, usage union with deleted keys,
  stats-disabled behavior.
- Web: API client/type tests, navigation, page rendering with empty/all-disabled
  alerts, create/rotate one-time dialog, usage numbers, requests filter.
- Full gate: `GOWORK=off go build ./...`, `go vet ./...`, `go test ./... -count=1`,
  `npm test`, `npx tsc --noEmit`, `npm run build`.

## Documentation

- README: data-plane authentication section, `gateway.keys` table row, header
  forms, open-vs-closed behavior, 401 behavior, one-time secret and rotation,
  per-key usage, migration note.
- AGENTS.md: configuration invariant (empty keys = open; any entry = auth on),
  admin API invariant (write-only, create/rotate one-time response, PUT
  preserves absent keys), stats invariant (key rollups, no backfill).
- `example.yaml`: commented keys block.

## Decisions left to the builder

- Exact wording of Chinese UI strings and the one-time-secret dialog.
- Whether the Sessions drawer is read-only or links into the full detail
  drawer; read-only is sufficient for v1.
- Whether the auth handler's header-size cap and read deadline live in one
  constants block or reuse `gateway.readTimeout`; the design requires a bound,
  not a particular value above 1 MiB / 15s.
