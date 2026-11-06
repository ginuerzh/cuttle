# Provider usage and balance page

Date: 2026-11-06
Status: approved

## Intent

Operators need one page that answers two questions per provider:

1. **What did this gateway use locally?** Requests, errors and tokens over a
   selected period.
2. **What is left upstream?** Account balance or subscription quota windows, as
   the vendor reports them.

Today the Requests page shows individual requests and the Topology page shows
live counts, but there is no per-provider roll-up and no way to see a DeepSeek
balance, CommandCode credits or OpenCode Go plan windows without opening the
vendor's console. This page closes that gap without adding a second metrics
store.

## Goals

- Add a **Usage** page to the admin console, one card per configured provider.
- Show local usage for 24 hours, 7 days or 30 days: requests, errors, input,
  output and cached tokens. Totals come from the hourly `rollups` table, which
  is retained for 90 days by default, so the 30-day view does not depend on the
  7-day raw-request retention.
- Add `cached_tokens` to rollups. Rows written before this change count as 0
  cached tokens; the limitation is documented.
- Query upstream remaining amounts per API key:
  - DeepSeek: `GET https://api.deepseek.com/user/balance`.
  - CommandCode: `GET https://api.commandcode.ai/alpha/billing/credits`.
  - OpenCode Go: `GET https://opencode.ai/zen/go/v1/usage`.
- Show vendors that cannot be queried as an explicit note, not as an error:
  OpenCode Zen exposes credits only in its console; `generic` has no known
  contract.
- Query each configured key separately because balances belong to accounts, and
  isolate failures per account.
- Cache results for 60 seconds, refresh on demand, and keep the last successful
  values when a refresh fails.
- Never return, log or persist API keys; only masked previews reach the browser.

## Non-goals

- Cost estimation or price lookup. Token counts are shown; money spent is not.
- Generic providers with a configurable balance URL / JSON path.
- Background polling, scheduled refreshes or stored balance history.
- Quota enforcement, alerts or routing changes based on remaining balance.
- Per-key attribution of historical requests (stats do not record which key
  served a request).

## Provider support

| Type | Query | Displayed |
| --- | --- | --- |
| `deepseek` | `GET /user/balance` with the provider's chat auth | Each currency's total, granted and topped-up balance |
| `commandcode` | `GET /alpha/billing/credits` | Credits balance plus 5-hour and weekly windows |
| `opencode-go` | `GET <base>/usage` (base is `/zen/go/v1`) | Rolling (5-hour), weekly and monthly windows |
| `opencode-zen` | none | Note: credits are only in the OpenCode console |
| `generic` | none | Note: unsupported provider type |

URLs are derived from the first configured base URL in canonical order
(`chat`, `responses`, `anthropic`, `gemini`), so a provider that only configures
Anthropic still has a base. The scheme and host are authoritative:

- DeepSeek and CommandCode replace the base path with the vendor path.
- OpenCode Go appends `/usage` to the configured base; a base ending in
  `/zen/go` is normalized to `/zen/go/v1/usage`, while `/zen/go/v1` becomes
  `/zen/go/v1/usage`.

Balance queries always use the provider's **Chat** auth, even when the selected
base is Anthropic or Responses, because the three vendor endpoints expect the
key in the OpenAI-style headers. This gives DeepSeek and CommandCode
`Authorization: Bearer <key>`, and OpenCode Go both `Authorization: Bearer
<key>` and `x-api-key: <key>`. Configured `auth.extra` headers are applied last,
as on data-plane requests.

Exact shapes used by the parser:

```json
// DeepSeek
{"is_available":true,"balance_infos":[
  {"currency":"CNY","total_balance":"110.00","granted_balance":"10.00","topped_up_balance":"100.00"}
]}

// CommandCode
{"credits":{"monthlyCredits":5.2207933742,"purchasedCredits":0,"freeCredits":0},
 "windowLimits":{"limited":true,
   "fiveHour":{"used":0.0866816244,"cap":14,"resetAt":4102444800000},
   "weekly":{"used":0.8971969678,"cap":35,"resetAt":4102444800000}}}

// OpenCode Go
{"usage":{
  "rolling":{"status":"ok","percent":37,"resetsAt":"2026-08-26T14:12:03.000Z"},
  "weekly":{"status":"ok","percent":12,"resetsAt":"2026-08-30T00:00:00.000Z"},
  "monthly":{"status":"rate-limited","percent":100,"resetsAt":"2026-09-01T00:00:00.000Z"}}}
```

## User-facing behavior

### Page layout

- Navigation item **Usage** between Topology and Providers.
- Range selector: 24 hours, 7 days, 30 days.
- Header summary: total requests, errors, input/output/cached tokens for the
  selected range, and the last query time.
- One card per provider:
  - provider name, type and enabled/disabled state;
  - local usage totals for the range;
  - one row per configured key with masked preview, balance and/or quota
    windows;
  - last successful check time, cache tag and error state.

The page has one refresh button in its header; v1 has no per-card refresh.

### Balance and windows

- Money is shown with a currency symbol when known (`$`, `¥`, `€`, `£`) and the
  ISO code otherwise, two decimals.
- A DeepSeek reply with several `balance_infos` entries shows one line per
  currency.
- Window rows show a progress bar, the used percentage and the reset time. A
  window at 0% does not show a reset time when the vendor's reset value is only
  a synthetic "now + span".
- Multiple keys are separate account rows. Duplicate values are allowed: the
  gateway cannot prove two keys share an account.
- A supported provider with no keys returns an empty `accounts` array and the
  note "no API key configured"; it does not invent a synthetic account.
- A disabled provider is not queried upstream. It still shows local historical
  usage, an `enabled: false` tag and the note "provider is disabled".
- An unsupported provider shows its note once, not once per key.

### Caching and failure

- The server caches each provider's upstream result for 60 seconds. The cache
  entry is updated after every attempt, including a failed one, so a failing
  vendor is not retried on every page load.
- Opening the page uses a fresh cache entry when available.
- The header refresh button forces a refresh of every enabled provider.
- If a refresh fails for an account, the previous balance/windows are kept, the
  new error is attached and the row is marked stale. If there was no previous
  success, the row is marked failed and contains no amounts. Subsequent reads
  within 60 seconds return the cached failure without another upstream call.
- Changing the configuration (admin edit or `SIGHUP`) clears the cache.
- No background polling: upstream calls happen only while someone is using the
  page or presses refresh.

## Architecture

### 1. `internal/provider/usage.go`

Replace the unused reserved `UsageProvider` / `UsageReport` placeholder with the
implemented contract:

```go
type BalancePart struct {
    Currency string  `json:"currency,omitempty"`
    Total    float64 `json:"total"`
    Granted  float64 `json:"granted,omitempty"`
    ToppedUp float64 `json:"toppedUp,omitempty"`
}

type QuotaWindow struct {
    Name        string     `json:"name"` // "5h", "weekly", "monthly"
    UsedPercent float64    `json:"usedPercent"`
    ResetsAt    *time.Time `json:"resetsAt,omitempty"`
}

type AccountUsage struct {
    KeyIndex  int           `json:"-"`
    Balance   []BalancePart `json:"balance,omitempty"`
    Windows   []QuotaWindow `json:"windows,omitempty"`
    Note      string        `json:"note,omitempty"`
    Error     string        `json:"error,omitempty"`
    CheckedAt time.Time     `json:"checkedAt"`
}

type UsageProvider interface {
    Usage(ctx context.Context) ([]AccountUsage, error)
}
```

`baseProvider` implements `Usage`:

- dispatch on the configured provider type;
- return `UsageUnsupportedError` when no base URL is configured;
- return an empty slice when no key is configured (the admin layer adds the
  "no API key configured" note);
- run one request per configured key, in parallel, preserving key order;
- use a 5-second timeout per key, the provider's TLS settings and a 1 MiB
  response cap;
- parse string or numeric JSON values;
- clamp percentages to 0–100;
- redact the key from every error string before it leaves the provider package;
- return an ordered `[]AccountUsage`, one entry per key.

An unsupported type returns a typed `UsageUnsupportedError` carrying a
user-facing note. OpenCode Zen's note links to
`https://opencode.ai/console`; `generic` says the type has no known balance
query.

Parsing rules:

- **DeepSeek**: `is_available` is advisory; `balance_infos[]` is authoritative.
  A reply without `balance_infos` is an error.
- **CommandCode**: balance is `monthlyCredits + purchasedCredits + freeCredits`
  (negative components clamp to zero); windows come from `windowLimits.fiveHour`
  and `windowLimits.weekly` with `used` / `cap` and the reset field `resetAt`
  (Unix milliseconds or seconds, or RFC 3339). A reply with neither credits nor
  windows is an error.
- **OpenCode Go**: `usage.rolling`, `usage.weekly`, `usage.monthly`, each with a
  `percent` and optional RFC 3339 `resetsAt`; at least one window must be
  present. A 0% window omits the vendor's synthetic `resetsAt` (a "now + span"
  value that marks no real reset).

### 2. Local usage

Aggregate the existing hourly `rollups` table and add one column:

```go
type ProviderUsage struct {
    Provider     string `json:"provider,omitempty"`
    Requests     int64  `json:"requests"`
    Errors       int64  `json:"errors"`
    InputTokens  int64  `json:"inputTokens"`
    OutputTokens int64  `json:"outputTokens"`
    CachedTokens int64  `json:"cachedTokens"`
}

func (s *Store) ProviderUsage(ctx context.Context, from, to time.Time) ([]ProviderUsage, error)
```

Migration:

- add `cached_tokens INTEGER NOT NULL DEFAULT 0` to `rollups` through
  `ensureColumns`;
- write it in `Insert` and add it on conflict;
- existing rows read as 0 cached tokens; this is a documented limitation, not a
  backfill.

Query:

```sql
SELECT provider,
       SUM(requests), SUM(errors),
       SUM(input_tokens), SUM(output_tokens), SUM(cached_tokens)
FROM rollups
WHERE bucket >= ? AND bucket < ?
GROUP BY provider
```

Buckets use the same RFC 3339, UTC, hour-truncated bounds as `Summary`, so
partial hours are included as whole buckets. This is consistent with the
existing stats page and keeps the 30-day option usable with the default 90-day
rollup retention.

The admin handler sums the rows into a `totals` object. Empty provider names
(unrouted requests) are included in totals and ignored by provider cards.

### 3. Admin API

`GET /api/provider-usage?from=<RFC3339>&to=<RFC3339>&refresh=1`

Authentication and CSRF behave like every other admin route. The response:

```json
{
  "from": "2026-11-06T00:00:00Z",
  "to": "2026-11-07T00:00:00Z",
  "checkedAt": "2026-11-06T12:34:56Z",
  "totals": {
    "requests": 123,
    "errors": 2,
    "inputTokens": 456789,
    "outputTokens": 12345,
    "cachedTokens": 6789
  },
  "localError": "",
  "providers": [
    {
      "name": "deepseek",
      "type": "deepseek",
      "enabled": true,
      "supported": true,
      "local": {
        "provider": "deepseek",
        "requests": 100,
        "errors": 1,
        "inputTokens": 400000,
        "outputTokens": 10000,
        "cachedTokens": 6000
      },
      "accounts": [
        {
          "keyPreview": "sk-...c487",
          "balance": [
            {
              "currency": "CNY",
              "total": 110,
              "granted": 10,
              "toppedUp": 100
            }
          ],
          "checkedAt": "2026-11-06T12:34:50Z"
        }
      ],
      "checkedAt": "2026-11-06T12:34:50Z",
      "cached": false
    },
    {
      "name": "opencode-zen",
      "type": "opencode-zen",
      "enabled": true,
      "supported": false,
      "note": "OpenCode Zen exposes credits only in the OpenCode console: https://opencode.ai/console",
      "accounts": [],
      "cached": false
    }
  ]
}
```

Rules:

- `supported` is false for OpenCode Zen and generic; `note` explains why.
- `enabled` reflects `provider.enabled`; disabled providers are not queried and
  carry the note "provider is disabled".
- `accounts` is always an array, ordered like the configured keys; `keyPreview`
  uses the existing admin mask function. A supported provider with no keys has
  `accounts: []` and the note "no API key configured".
- `cached` means the upstream values came from a cache entry that had not
  expired. It does not mean the data is wrong.
- `local` is omitted when stats are disabled; `localError` then explains why.
  Local stats failure never suppresses upstream balances.
- Provider queries run concurrently. Within a provider, keys run concurrently.
- Cache entries are keyed by provider name, are refreshed after every attempt
  (success or failure), and are cleared by `SetConfig` and admin save.
- On a failed refresh, merge the previous account values with the new error and
  set `stale: true`; retain the previous `checkedAt`. The failed result is
  cached for the same 60 seconds.
- The response never includes a raw key.

New files:

- `internal/admin/handlers_usage.go` — response types, handler, cache/merge.
- `internal/admin/usage_test.go` — API, cache, stale and redaction tests.
- `internal/provider/usage_test.go` — vendor parsing and redaction tests.
- `web/src/pages/Usage.tsx` — page.

Modified:

- `internal/admin/server.go` — route, cache fields, invalidation.
- `internal/admin/handlers.go` — no change except sharing `preview`.
- `internal/provider/usage.go` — implemented contract and parsers.
- `internal/provider/provider.go` — no interface change; `baseProvider` gains
  `Usage`.
- `internal/stats/store.go` — rollup `cached_tokens` column and
  `ProviderUsage`.
- `internal/stats/store_test.go` — rollup aggregation and migration tests.
- `web/src/api/types.ts`, `web/src/api/client.ts`,
  `web/src/api/client.test.ts`, `web/src/App.tsx`,
  `web/src/locales/en-US.ts`.

### 4. Web

New `web/src/pages/Usage.tsx`:

- `ListPage` shell.
- Range `Segmented`, refresh button, last query time.
- Summary `Statistic` row.
- Ant Design `Card` per provider (not `ListCard`, which has no body slot).
- `Progress` for each quota window; threshold colors green < 70, orange < 90,
  red otherwise.
- `Alert` per stale/failed account; keep amounts visible when stale.
- Empty and loading states; mobile widths.

API/types additions:

```ts
getProviderUsage({ from, to, refresh }): Promise<ProviderUsageResponse>
```

`App.tsx`: add the `usage` page, nav item and render branch. Add icons, locale
keys, and page type.

## Error handling

- Upstream non-2xx: include the status and a truncated body, with the key
  replaced by `***`.
- Malformed JSON or no expected field: a per-account error; other accounts keep
  working.
- Timeout or connection refused: per-account error; provider card remains.
- No keys: `accounts: []` plus the provider note "no API key configured".
- Disabled provider: no network call; `enabled: false` plus the note.
- Unsupported type: provider-level note; no network call.
- Local stats failure: `localError`, balances still returned.
- Unknown `from`/`to`: existing `parseRange` fallback (last 24 hours).

## Security and privacy

- The endpoint is behind the existing admin auth middleware.
- Keys never appear in JSON, logs, stats or errors; only the existing masked
  preview does.
- Outbound queries send the key only to the configured provider base host.
- Response bodies are capped at 1 MiB; error text is redacted first, then
  truncated to 200 bytes so a key that straddles the cut cannot leak a prefix.
- Balances are account metadata; they are not persisted, so they disappear on
  restart.

## Verification

Provider package:

- DeepSeek multi-currency parsing, string numbers.
- CommandCode credits and both windows, including reset formats.
- OpenCode Go rolling/weekly/monthly and 0% reset suppression.
- OpenCode Zen note and generic unsupported note.
- Multiple keys get separate reports and preserve order.
- HTTP 401/500, malformed JSON, timeout and key-echo redaction.
- URL derivation from base paths.

Stats:

- Provider aggregation from rollups over a window, error counting and cached
  tokens.
- `ensureColumns` adds `cached_tokens` to an old rollups table; pre-existing
  rows read as 0.
- Empty provider rows are included in totals and ignored by cards.

Admin:

- Auth required.
- Local usage and upstream balances join by provider.
- Cache prevents a second upstream call within 60 seconds; `refresh=1` bypasses.
- Failed refresh keeps the last success, marks stale, records the error and is
  itself cached for 60 seconds.
- Configuration reload clears the cache.
- Disabled providers make no upstream call; no-key providers return an empty
  accounts array and a note.
- JSON never contains a raw key.

Web:

- Client query parameters and null-array handling.
- `tsc --noEmit`, Vitest and `npm run build`.

Manual:

- Run the demo against real DeepSeek, CommandCode and OpenCode Go keys; confirm
  balance/window values against the vendor consoles, then refresh and confirm
  cache behavior.

## Rollout

No configuration change. The only database change is the additive
`rollups.cached_tokens` column; `ensureColumns` applies it in place and old rows
read 0. Existing databases and provider configs keep working; unsupported
providers simply show a note. The page is read-only and makes no data-plane
changes.

## Alternatives considered

- **Read the raw `requests` table.** Rejected: the default 7-day detail
  retention would silently truncate the 30-day view, while rollups cover 90
  days. The cost is that cached tokens are only exact for rows written after
  this change; older rollups read 0.
- **Background refresh loop.** Rejected for v1: vendors may rate-limit balance
  queries, and the page is admin-only and opened occasionally. Cache plus an
  explicit refresh is enough.
- **Per-card refresh.** Deferred: it needs a `provider` query parameter and
  extra cache tests. The header refresh covers the v1 use case.
- **A single `UsageReport` with aggregate usage.** Rejected: balances are per
  account/key, so the contract must return one report per key.
- **Configurable generic balance URL now.** Deferred: it needs a JSON-path
  evaluator and UI fields; the three providers in use have fixed contracts.
- **Persist balance history.** Deferred: it needs a retention policy and a new
  table, and is not required to answer "what is left now".
