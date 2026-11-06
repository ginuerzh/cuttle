# Provider Usage Page Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an admin Usage page that shows local per-provider request/token totals and each provider key's upstream balance or quota windows.

**Architecture:** `internal/provider` gains a per-key `Usage` query with vendor parsers. `internal/stats` adds `cached_tokens` to hourly rollups and a provider aggregation query. `internal/admin` exposes one cached `GET /api/provider-usage` endpoint that joins local totals with upstream reports and masks keys. The React console renders one card per provider.

**Tech Stack:** Go 1.26, SQLite (modernc), Ant Design + React + Vite, Vitest.

**Spec:** `docs/superpowers/specs/2026-11-06-provider-usage-page-design.md`

## Global Constraints

- Run every Go command with `GOWORK=off` from `llm-gw/`.
- Do not edit sibling repositories (`../core`, `../x`, `../llm-api-converter`).
- Upstream query timeout: 5 seconds per key; response body cap: 1 MiB; error snippet cap: 200 bytes.
- Admin cache TTL: 60 seconds; cache failures too; `refresh=1` bypasses the cache.
- Never return, log or persist an API key; only the existing masked preview may reach the browser.
- Supported types only: `deepseek`, `commandcode`, `opencode-go`. `opencode-zen` and `generic` return a note and make no network call.
- Disabled providers are not queried. Providers with no keys return an empty `accounts` array and a note.
- Local usage comes from `rollups`, with `cached_tokens` added additively. Old rows read 0.
- UI source strings are Chinese and must have English entries in `web/src/locales/en-US.ts`.
- Commit after every task, including that task's filled Implementation record in the same commit. Never push.

## Review Focus

1. A provider with multiple keys must keep separate balances in configured key order and never mix accounts.
2. A failed refresh must keep the last successful amounts, mark the account stale, and not erase its last-good timestamp.
3. Disabled, keyless and unsupported providers must make no upstream call and must not display misleading amounts.
4. The 30-day local view must use rollups and remain correct when an old database is opened.
5. No admin response may contain a raw key, including text echoed back by a vendor error.

---

### Task 1: Provider usage queries

**Files:**
- Modify: `internal/provider/usage.go` (replace the reserved placeholder)
- Create: `internal/provider/usage_test.go`
- Modify: `docs/superpowers/specs/2026-11-06-provider-usage-page-design.md` (only if implementation reveals a spec error)
- Modify: `docs/superpowers/plans/2026-11-06-provider-usage-page.md` (Implementation record)

**Interfaces:**
- Produces:
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

  type UsageUnsupportedError struct {
      Provider string
      Type     string
      Note     string
  }
  func (e *UsageUnsupportedError) Error() string
  ```
- `baseProvider` implements `Usage(ctx context.Context) ([]AccountUsage, error)`.
- Consumers use `provider.New(cfg)`, then `p.(provider.UsageProvider)`.
- `UsageUnsupportedError.Note` is user-facing English.

- [ ] **Step 1: Write the failing provider tests**

Create `internal/provider/usage_test.go`. Import `context`, `errors`, `fmt`, `net/http`, `net/http/httptest`, `strings`, `sync/atomic`, `testing`, and `github.com/go-gost/llm-gw/internal/config`.

```go
func usageProvider(t *testing.T, cfg config.Provider) UsageProvider {
    t.Helper()
    p, err := New(cfg)
    if err != nil {
        t.Fatal(err)
    }
    up, ok := p.(UsageProvider)
    if !ok {
        t.Fatal("provider does not implement UsageProvider")
    }
    return up
}

func TestUsageDeepSeek(t *testing.T) {
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if r.URL.Path != "/user/balance" {
            t.Errorf("path = %q", r.URL.Path)
        }
        if got := r.Header.Get("Authorization"); got != "Bearer sk-test" {
            t.Errorf("auth = %q", got)
        }
        w.Write([]byte(`{"is_available":true,"balance_infos":[
            {"currency":"CNY","total_balance":"110.00","granted_balance":"10.00","topped_up_balance":"100.00"},
            {"currency":"USD","total_balance":"2.50","granted_balance":"0","topped_up_balance":"2.50"}]}`))
    }))
    defer srv.Close()

    accounts, err := usageProvider(t, config.Provider{
        Name: "deepseek", Type: "deepseek", Chat: srv.URL + "/v1", Keys: []string{"sk-test"},
    }).Usage(context.Background())
    if err != nil {
        t.Fatal(err)
    }
    if len(accounts) != 1 || len(accounts[0].Balance) != 2 {
        t.Fatalf("accounts = %+v", accounts)
    }
    if accounts[0].Balance[0].Currency != "CNY" || accounts[0].Balance[0].Total != 110 {
        t.Fatalf("balance = %+v", accounts[0].Balance)
    }
    if accounts[0].CheckedAt.IsZero() || accounts[0].Error != "" {
        t.Fatalf("account = %+v", accounts[0])
    }
}
```

Add these tests with the assertions below:

- `TestUsageCommandCode`: handler requires path `/alpha/billing/credits` and `Authorization: Bearer sk-test`; return
  `{"credits":{"monthlyCredits":12.3,"purchasedCredits":2,"freeCredits":0},"windowLimits":{"limited":true,"fiveHour":{"used":4.2,"cap":10,"resetAt":4102444800000},"weekly":{"used":9,"cap":50,"resetAt":"2030-01-01T00:00:00Z"}}}`.
  Assert one `USD` balance of `14.3` within `1e-9`, windows `5h` at `42` and `weekly` at `18`, and both `ResetsAt` non-nil.
- `TestUsageOpenCodeGo`: config `Chat: srv.URL + "/zen/go/v1"`; handler path `/zen/go/v1/usage` and both `Authorization: Bearer sk-test` and `x-api-key: sk-test`. Return
  `{"usage":{"rolling":{"percent":37,"resetsAt":"2026-08-26T14:12:03.000Z"},"weekly":{"percent":12,"resetsAt":"2026-08-30T00:00:00.000Z"},"monthly":{"percent":0,"resetsAt":"2026-09-01T00:00:00.000Z"}}}`.
  Assert three windows in order `5h, weekly, monthly`, percentages `37, 12, 0`, the first two resets non-nil, and the 0% monthly reset nil.
- `TestUsageOpenCodeGoBasePaths`: table with bases `srv.URL+"/zen/go"`, `srv.URL+"/zen/go/v1"`, `srv.URL+"/zen/go/v1/"`; all must request `/zen/go/v1/usage`. A fourth base `srv.URL+"/proxy/v1"` must request `/proxy/v1/usage`.
- `TestUsageUnsupported`: for `opencode-zen` and `generic`, use `var ue *UsageUnsupportedError; errors.As(err, &ue)` and assert it is true; the Zen note contains `https://opencode.ai/console`.
- `TestUsageNoKeys`: config with `Keys: nil`; `Usage` returns an empty slice and nil error.
- `TestUsageMultipleKeys`: two keys; handler returns `total_balance` `"1"` for key `sk-one` and `"2"` for key `sk-two`. Assert `len(accounts)==2`, `accounts[0].KeyIndex==0`, `accounts[1].KeyIndex==1`, and balances `1` then `2`.
- `TestUsageBaseFallback`: config with only `Anthropic: srv.URL+"/anthropic"`, type `deepseek`, key `sk-test`; handler expects path `/user/balance` and `Authorization: Bearer sk-test`.
- `TestUsageHTTPErrorRedactsKey`: handler returns 500 with body `bad key sk-secret-1234`; assert the account error contains `500`, does not contain `sk-secret-1234`, contains `***`.
- `TestUsageMalformedJSON`: handler returns `not-json`; assert a per-account error and no panic.
- `TestUsageCanceledContext`: canceled context; assert an error string is set on the account.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `GOWORK=off go test ./internal/provider -run 'TestUsage' -count=1`
Expected: FAIL because `UsageProvider`, `AccountUsage`, `UsageUnsupportedError` and `baseProvider.Usage` are undefined.

- [ ] **Step 3: Implement `internal/provider/usage.go`**

Replace the placeholder file with the types above and the implementation. Required helpers and exact behavior:

```go
func (p *baseProvider) Usage(ctx context.Context) ([]AccountUsage, error)
func (p *baseProvider) usageBaseURL() string
func (p *baseProvider) usageURL(kind string) (string, bool)
func (p *baseProvider) usageAccount(ctx context.Context, kind, key string, index int) AccountUsage
func parseDeepSeekUsage(body []byte) ([]BalancePart, []QuotaWindow, error)
func parseCommandCodeUsage(body []byte) ([]BalancePart, []QuotaWindow, error)
func parseOpenCodeGoUsage(body []byte) ([]BalancePart, []QuotaWindow, error)
func usageNumber(v any) (float64, bool)
func usageTime(v any) *time.Time
func usageClampPercent(v float64) float64
```

Implementation rules:

- `Usage` dispatches by `strings.ToLower(strings.TrimSpace(p.cfg.Type))`:
  - `deepseek`: URL root path `/user/balance`.
  - `commandcode`: URL root path `/alpha/billing/credits`.
  - `opencode-go`: path normalization described above.
  - `opencode-zen`: return `nil, &UsageUnsupportedError{Provider: p.cfg.Name, Type: p.cfg.Type, Note: "OpenCode Zen exposes credits only in the OpenCode console: https://opencode.ai/console"}`.
  - default: return `nil, &UsageUnsupportedError{..., Note: "provider type has no known balance query"}`.
- `usageBaseURL` uses `p.BaseURL` in order `Chat, Responses, Anthropic, Gemini` and returns the first non-empty.
- If there is no base URL, return `nil, &UsageUnsupportedError{Provider: p.cfg.Name, Type: p.cfg.Type, Note: "no API base URL configured"}`.
- If there is no key, return `[]AccountUsage{}, nil` (the admin layer adds the "no API key configured" note).
- For each key, run `usageAccount` in a goroutine; preserve key order; wait for all. Each account uses `context.WithTimeout(ctx, 5*time.Second)`.
- `usageAccount`:
  - `client := NewHTTPClient(p.cfg.TLS, 5*time.Second)`.
  - Build `GET` request with `context`.
  - `req.Header.Set("User-Agent", "llm-gw")`.
  - `applyAuth(req, p.Auth(Chat), key)`.
  - On non-2xx, read at most 1 MiB, replace `key` with `***`, then take at most 200 bytes, and set `Error: fmt.Sprintf("status %d: %s", code, snippet)`.
  - On success, read at most 1 MiB, parse, set `Balance`, `Windows`, `CheckedAt: time.Now().UTC()`.
  - Any error is redacted with `strings.ReplaceAll(msg, key, "***")`.
- `parseDeepSeekUsage`: decode `balance_infos`; accept string or number fields; error when the list is empty.
- `parseCommandCodeUsage`: decode `credits.monthlyCredits`, `purchasedCredits`, `freeCredits`; sum clamped non-negative values into one `USD` `BalancePart`; decode `windowLimits.fiveHour` and `.weekly` with `used`, `cap`, `resetAt`; percentage = `clamp(100*used/cap)`; error only when neither credits nor windows are readable.
- `parseOpenCodeGoUsage`: decode `usage.rolling`, `.weekly`, `.monthly`; each `percent` is a pointer, `resetsAt` is RFC3339Nano; include a window only when `percent` is present; omit `ResetsAt` when percent is 0; error when no window is present.
- `usageNumber` accepts `json.Number`, `float64`, `int`, `int64`, and numeric strings.
- `usageTime` accepts RFC3339/RFC3339Nano strings and numbers interpreted as Unix seconds when `< 1e12`, else Unix milliseconds; values `<= 0` return nil.
- `usageClampPercent` clamps to `[0,100]`.
- Remove the old `UsageQuery`/`UsageReport` placeholder types and interface.

- [ ] **Step 4: Run the focused tests to verify they pass**

Run: `GOWORK=off go test ./internal/provider -run 'TestUsage' -count=1`
Expected: PASS.

- [ ] **Step 5: Run the provider package and build**

Run: `GOWORK=off go test ./internal/provider -count=1 && GOWORK=off go build ./...`
Expected: PASS.

- [ ] **Step 6: Fill the Implementation record and commit**

Fill this task's record below, then:

```bash
git add internal/provider/usage.go internal/provider/usage_test.go \
  docs/superpowers/specs/2026-11-06-provider-usage-page-design.md \
  docs/superpowers/plans/2026-11-06-provider-usage-page.md
git commit -m "feat(provider): query upstream usage and balances"
```

**Implementation record**
- Date/status: 2026-11-06 / complete
- Repo/commit: llm-gw / this commit
- Files changed: `internal/provider/usage.go` (implemented types, URL derivation, per-key parallel queries, parsers and redaction), `internal/provider/usage_test.go`, design status approved, plan record.
- Tests/commands and results: `GOWORK=off go test ./internal/provider -run 'TestUsage' -count=1` PASS; `GOWORK=off go test ./internal/provider -count=1` PASS; `GOWORK=off go build ./...` PASS.
- Review findings and fixes: Self-review confirmed that unsupported types and missing bases return a typed note without network I/O; an empty key list returns an empty slice; errors are redacted before leaving the provider package; each key is queried concurrently and written to its own ordered slot. No fixes were needed after the focused tests.
- Deferred items/limitations: Balance history, generic custom balance URLs, and per-card refresh are deferred. The demo has real DeepSeek credentials only; CommandCode and OpenCode Go live values were not verified against vendor consoles.

---

### Task 2: Rollup cached tokens and provider aggregation

**Files:**
- Modify: `internal/stats/store.go`
- Modify: `internal/stats/store_test.go`
- Modify: `docs/superpowers/plans/2026-11-06-provider-usage-page.md` (Implementation record)

**Interfaces:**
- Consumes: existing `Store.Insert`, `rollups` table.
- Produces:
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

- [ ] **Step 1: Write the failing stats tests**

Add to `internal/stats/store_test.go`:

```go
func TestProviderUsageFromRollups(t *testing.T) {
    s := newStore(t)
    ctx := context.Background()
    base := time.Now().UTC().Truncate(time.Hour)
    rows := []Request{
        {ID: "a1", SID: "a1", TS: base.Add(time.Minute), Provider: "a", Status: 200, InputTokens: 10, OutputTokens: 2, CachedTokens: 3},
        {ID: "a2", SID: "a2", TS: base.Add(2 * time.Minute), Provider: "a", Status: 500, Error: "boom", InputTokens: 20, OutputTokens: 4, CachedTokens: 5},
        {ID: "b1", SID: "b1", TS: base.Add(3 * time.Minute), Provider: "b", Status: 200, InputTokens: 7, OutputTokens: 1, CachedTokens: 2},
    }
    for _, r := range rows {
        if err := s.Insert(ctx, r); err != nil {
            t.Fatal(err)
        }
    }
    got, err := s.ProviderUsage(ctx, base, base.Add(time.Hour))
    if err != nil {
        t.Fatal(err)
    }
    if len(got) != 2 {
        t.Fatalf("rows = %+v", got)
    }
    a := got[0]
    if a.Provider != "a" || a.Requests != 2 || a.Errors != 1 ||
        a.InputTokens != 30 || a.OutputTokens != 6 || a.CachedTokens != 8 {
        t.Fatalf("a = %+v", a)
    }
    if got[1].Provider != "b" || got[1].Requests != 1 || got[1].CachedTokens != 2 {
        t.Fatalf("b = %+v", got[1])
    }
}
```

Add `TestProviderUsageMigratesOldRollups`:

```go
func TestProviderUsageMigratesOldRollups(t *testing.T) {
    path := filepath.Join(t.TempDir(), "stats.sqlite")
    db, err := sql.Open("sqlite", path)
    if err != nil {
        t.Fatal(err)
    }
    if _, err := db.Exec(`CREATE TABLE rollups (
        bucket TEXT NOT NULL,
        provider TEXT NOT NULL DEFAULT '',
        model TEXT NOT NULL DEFAULT '',
        requests INTEGER NOT NULL DEFAULT 0,
        errors INTEGER NOT NULL DEFAULT 0,
        input_tokens INTEGER NOT NULL DEFAULT 0,
        output_tokens INTEGER NOT NULL DEFAULT 0,
        PRIMARY KEY (bucket, provider, model))`); err != nil {
        t.Fatal(err)
    }
    bucket := time.Now().UTC().Truncate(time.Hour).Format(time.RFC3339)
    if _, err := db.Exec(`INSERT INTO rollups
        (bucket, provider, model, requests, errors, input_tokens, output_tokens)
        VALUES (?, 'old', 'm', 1, 0, 10, 2)`, bucket); err != nil {
        t.Fatal(err)
    }
    db.Close()

    s, err := Open(path)
    if err != nil {
        t.Fatal(err)
    }
    defer s.Close()
    got, err := s.ProviderUsage(context.Background(),
        time.Now().UTC().Truncate(time.Hour), time.Now().UTC().Add(time.Hour))
    if err != nil {
        t.Fatal(err)
    }
    if len(got) != 1 || got[0].Provider != "old" || got[0].CachedTokens != 0 {
        t.Fatalf("old rollup = %+v", got)
    }
}
```

The test file already imports `context`, `path/filepath`, `testing`, `time`; add `database/sql` and the sqlite driver is already imported by the package.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `GOWORK=off go test ./internal/stats -run 'TestProviderUsage' -count=1`
Expected: FAIL because `ProviderUsage` and `rollups.cached_tokens` are missing.

- [ ] **Step 3: Implement the rollup column and query**

In `internal/stats/store.go`:

- Add `cached_tokens INTEGER NOT NULL DEFAULT 0` to the `CREATE TABLE IF NOT EXISTS rollups` statement.
- After the existing `ensureColumns("requests", ...)` call, add:
  ```go
  if err := s.ensureColumns("rollups", map[string]string{
      "cached_tokens": "INTEGER NOT NULL DEFAULT 0",
  }); err != nil {
      return err
  }
  ```
- Extend the rollup insert:
  ```sql
  INSERT INTO rollups
  (bucket, provider, model, requests, errors, input_tokens, output_tokens, cached_tokens)
  VALUES (?,?,?,1,?,?,?,?)
  ON CONFLICT(bucket, provider, model) DO UPDATE SET
      requests = requests + 1,
      errors = errors + excluded.errors,
      input_tokens = input_tokens + excluded.input_tokens,
      output_tokens = output_tokens + excluded.output_tokens,
      cached_tokens = cached_tokens + excluded.cached_tokens
  ```
  Pass `r.CachedTokens` as the last argument.
- Add `ProviderUsage`:
  ```go
  func (s *Store) ProviderUsage(ctx context.Context, from, to time.Time) ([]ProviderUsage, error) {
      fromBucket := from.UTC().Truncate(time.Hour).Format(time.RFC3339)
      toBucket := to.UTC().Format(time.RFC3339)
      rows, err := s.db.QueryContext(ctx, `SELECT provider,
          COALESCE(SUM(requests),0), COALESCE(SUM(errors),0),
          COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0),
          COALESCE(SUM(cached_tokens),0)
          FROM rollups WHERE bucket >= ? AND bucket < ? GROUP BY provider ORDER BY provider`,
          fromBucket, toBucket)
      if err != nil {
          return nil, err
      }
      defer rows.Close()
      out := make([]ProviderUsage, 0)
      for rows.Next() {
          var r ProviderUsage
          if err := rows.Scan(&r.Provider, &r.Requests, &r.Errors,
              &r.InputTokens, &r.OutputTokens, &r.CachedTokens); err != nil {
              return nil, err
          }
          out = append(out, r)
      }
      return out, rows.Err()
  }
  ```

- [ ] **Step 4: Run the focused tests to verify they pass**

Run: `GOWORK=off go test ./internal/stats -run 'TestProviderUsage' -count=1`
Expected: PASS.

- [ ] **Step 5: Run the whole stats package**

Run: `GOWORK=off go test ./internal/stats -count=1`
Expected: PASS.

- [ ] **Step 6: Fill the Implementation record and commit**

```bash
git add internal/stats/store.go internal/stats/store_test.go \
  docs/superpowers/plans/2026-11-06-provider-usage-page.md
git commit -m "feat(stats): aggregate provider usage from rollups"
```

**Implementation record**
- Date/status: 2026-11-06 / complete
- Repo/commit: llm-gw / this commit
- Files changed: `internal/stats/store.go` (rollup `cached_tokens`, `ProviderUsage`), `internal/stats/store_test.go`, plan record.
- Tests/commands and results: `GOWORK=off go test ./internal/stats -run 'TestProviderUsage' -count=1` PASS; `GOWORK=off go test ./internal/stats -count=1` PASS.
- Review findings and fixes: Self-review confirmed the additive `ensureColumns("rollups", ...)` migration works on an old table, the rollup insert adds cached tokens on conflict, and the aggregation uses the same hour-truncated bounds as `Summary`. No fixes were needed after the focused tests.
- Deferred items/limitations: Rollups written before this change have `cached_tokens = 0`; no backfill is attempted. The 30-day view remains bounded by the configured rollup retention (90 days by default).

---

### Task 3: Admin provider usage API with cache and stale merge

**Files:**
- Create: `internal/admin/handlers_usage.go`
- Create: `internal/admin/usage_test.go`
- Modify: `internal/admin/server.go`
- Modify: `docs/superpowers/plans/2026-11-06-provider-usage-page.md` (Implementation record)

**Interfaces:**
- Consumes: `provider.UsageProvider`, `provider.AccountUsage`, `provider.BalancePart`, `provider.QuotaWindow`, `stats.ProviderUsage`.
- Produces:
  ```go
  type accountUsageView struct {
      KeyPreview string                   `json:"keyPreview,omitempty"`
      Balance    []provider.BalancePart   `json:"balance,omitempty"`
      Windows    []provider.QuotaWindow   `json:"windows,omitempty"`
      Error      string                   `json:"error,omitempty"`
      Stale      bool                     `json:"stale,omitempty"`
      CheckedAt  string                   `json:"checkedAt,omitempty"`
  }

  type providerUsageView struct {
      Name      string                  `json:"name"`
      Type      string                  `json:"type"`
      Enabled   bool                    `json:"enabled"`
      Supported bool                    `json:"supported"`
      Note      string                  `json:"note,omitempty"`
      Local     *stats.ProviderUsage    `json:"local,omitempty"`
      Accounts  []accountUsageView      `json:"accounts"`
      CheckedAt string                  `json:"checkedAt,omitempty"`
      Cached    bool                    `json:"cached,omitempty"`
  }

  type providerUsageResponse struct {
      From       string               `json:"from"`
      To         string               `json:"to"`
      CheckedAt  string               `json:"checkedAt"`
      Totals     *stats.ProviderUsage `json:"totals,omitempty"`
      LocalError string               `json:"localError,omitempty"`
      Providers  []providerUsageView  `json:"providers"`
  }
  ```
- Route: `GET /api/provider-usage?from=<RFC3339>&to=<RFC3339>&refresh=1`.

- [ ] **Step 1: Write the failing admin tests**

Create `internal/admin/usage_test.go`. Use the existing `newTestAdmin`, `login`, `doJSON` helpers. Add a helper:

```go
func setUsageProviders(t *testing.T, srv *Server, providers ...config.Provider) {
    t.Helper()
    srv.mu.Lock()
    srv.cfg.Providers = providers
    srv.mu.Unlock()
}
```

Add tests:

- `TestProviderUsageAPI`:
  - vendor `httptest.Server` returns DeepSeek balance `{"balance_infos":[{"currency":"USD","total_balance":"11.12","granted_balance":"1","topped_up_balance":"10.12"}]}`.
  - config provider `{Name:"deepseek", Type:"deepseek", Chat: vendor.URL+"/v1", Keys: []string{"sk-secret-1234"}}`.
  - insert a request with `Provider:"deepseek", Status:200, InputTokens:10, OutputTokens:2, CachedTokens:3`.
  - login, GET `/api/provider-usage`.
  - assert 200; `totals.Requests==1`; provider local cached 3; one account balance 11.12; key preview `sk-...1234`; raw body does not contain `sk-secret-1234`.
- `TestProviderUsageCache`:
  - vendor counter; config one provider.
  - login; GET `?refresh=1`; GET again without refresh; assert counter 1.
  - GET `?refresh=1`; assert counter 2.
- `TestProviderUsageMultipleKeys`:
  - provider keys `[]string{"sk-secret-1111", "sk-secret-2222"}`; vendor returns balance `1` for the first and `2` for the second, keyed by `Authorization`.
  - GET `?refresh=1`; assert two accounts in configured order, previews `sk-...1111` and `sk-...2222`, balances `1` and `2`, and no raw key in the body.
- `TestProviderUsageFailureKeepsLastGood`:
  - vendor mode atomic: first success, then 500 body `bad sk-secret-1234`.
  - GET `?refresh=1`; GET `?refresh=1`; assert account balance still 11.12, `Error` contains `500`, `Stale` true, `CheckedAt` unchanged.
  - GET again without refresh; assert vendor counter is still 2 (failure cached) and error remains.
- `TestProviderUsageDisabledAndNoKey`:
  - disabled provider with vendor counter; assert 0 calls, `enabled:false`, note `provider is disabled`, `accounts` empty.
  - keyless enabled provider; assert note `no API key configured`, `accounts` empty.
- `TestProviderUsageUnsupported`:
  - `opencode-zen` provider; assert `supported:false`, note contains `https://opencode.ai/console`, no vendor call.
- `TestProviderUsageUnauthenticated`: GET with `&http.Client{}`; assert 401.
- `TestProviderUsageReloadClearsCache`:
  - GET `?refresh=1`; build `next` as a copy of the current config (keep
    `admin.passwordHash` and the same provider) and call `srv.SetConfig(next)`;
    GET without refresh; assert vendor counter increments.
- `TestProviderUsageLocalError`: set `srv.stats = nil`; GET; assert 200, `localError` non-empty, balance still present.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `GOWORK=off go test ./internal/admin -run 'TestProviderUsage' -count=1`
Expected: FAIL with 404 because the route and handler are missing.

- [ ] **Step 3: Implement the admin handler and cache**

In `internal/admin/server.go`:

- Add to `Server`:
  ```go
  usageMu    sync.Mutex
  usageCache map[string]usageCacheEntry
  ```
- Initialize `usageCache: make(map[string]usageCacheEntry)` in `New`.
- Register `api.HandleFunc("GET /api/provider-usage", s.handleProviderUsage)`.
- Add:
  ```go
  func (s *Server) invalidateUsageLocked() {
      s.usageMu.Lock()
      defer s.usageMu.Unlock()
      s.usageCache = make(map[string]usageCacheEntry)
  }
  ```
  Call it in `SetConfig` and at the end of a successful `replaceLocked` (after `s.cfg = c`).

Create `internal/admin/handlers_usage.go`:

```go
const usageCacheTTL = 60 * time.Second

type usageCacheEntry struct {
    view      providerUsageView
    fetchedAt time.Time
}
```

Handler behavior:

- `from, to := parseRange(r)`; `refresh := r.URL.Query().Get("refresh") == "1"`.
- Snapshot providers under `s.mu` into a slice.
- Local stats:
  ```go
  var (
      localRows []stats.ProviderUsage
      localMap  = map[string]stats.ProviderUsage{}
      totals    *stats.ProviderUsage
      localErr  string
  )
  if s.stats != nil {
      localRows, err = s.stats.ProviderUsage(r.Context(), from, to)
      if err != nil {
          localErr = err.Error()
      } else {
          sum := stats.ProviderUsage{}
          for _, row := range localRows {
              localMap[row.Provider] = row
              sum.Requests += row.Requests
              sum.Errors += row.Errors
              sum.InputTokens += row.InputTokens
              sum.OutputTokens += row.OutputTokens
              sum.CachedTokens += row.CachedTokens
          }
          totals = &sum
      }
  } else {
      localErr = "stats disabled"
  }
  ```
- Build `views := make([]providerUsageView, len(providers))`; run one goroutine per provider:
  - read `old, cached := s.cachedUsage(name)` under `usageMu`; if `!refresh && cached`, set `old.Cached = true`, attach local for the response, return.
  - else `view := s.buildProviderUsage(ctx, providers[i], old)`, store it without local, attach local for the response.
- Store upstream views without `Local`; attach local totals only when assembling the response.
- Wait for all; write `providerUsageResponse{From: from.UTC().Format(time.RFC3339), To: to..., CheckedAt: now..., Totals: totals, LocalError: localErr, Providers: views}`.
- `buildProviderUsage`:
  - start from a fresh `providerUsageView{Name, Type, Enabled, Supported: true, CheckedAt: now}`; keep `old` only for merging account data.
  - if disabled: `Note:"provider is disabled"`, `Accounts: []`, `CheckedAt: now`.
  - if type not supported: `Supported:false`, note, `Accounts: []`.
  - if `len(cfg.Keys)==0`: `Note:"no API key configured"`, `Accounts: []`.
  - else `p, err := provider.New(cfg)`; on error set `Note` and empty accounts.
  - type-assert `UsageProvider`; call `Usage(ctx)`.
  - on `UsageUnsupportedError`: `Supported:false`, `Note: e.Note`, empty accounts.
  - on other error: `Note: err.Error()`, empty accounts.
  - on success: `Supported:true`, `Note:""`, and build accounts.
    ```go
    for _, acc := range accounts {
        preview := ""
        if acc.KeyIndex >= 0 && acc.KeyIndex < len(cfg.Keys) {
            preview = preview(cfg.Keys[acc.KeyIndex])
        }
        viewAcc := accountUsageView{
            KeyPreview: preview,
            Balance: acc.Balance,
            Windows: acc.Windows,
            Error: acc.Error,
            CheckedAt: formatCheckedAt(acc.CheckedAt),
        }
        if acc.Error != "" {
            if oldAcc, ok := oldAccount(old, acc.KeyIndex); ok &&
                (len(oldAcc.Balance) > 0 || len(oldAcc.Windows) > 0) {
                viewAcc.Balance = oldAcc.Balance
                viewAcc.Windows = oldAcc.Windows
                viewAcc.CheckedAt = oldAcc.CheckedAt
                viewAcc.Stale = true
            }
        }
        view.Accounts = append(view.Accounts, viewAcc)
    }
    ```
  - `CheckedAt: now` (last attempt), `Cached:false`.
- Helpers:
  ```go
  func formatCheckedAt(t time.Time) string {
      if t.IsZero() { return "" }
      return t.UTC().Format(time.RFC3339)
  }
  func oldAccount(v *providerUsageView, index int) (accountUsageView, bool)
  ```
  Match old accounts by position, not preview, because the config order is stable.
- `s.cachedUsage`:
  ```go
  func (s *Server) cachedUsage(name string) (providerUsageView, bool) {
      s.usageMu.Lock()
      defer s.usageMu.Unlock()
      e, ok := s.usageCache[name]
      return e.view, ok && time.Since(e.fetchedAt) < usageCacheTTL
  }
  ```
- `s.storeUsage(name, view, now)`.
- Attach local:
  ```go
  if s.stats != nil {
      v := localMap[p.Name]
      v.Provider = p.Name
      view.Local = &v
  }
  ```
- Ensure `Accounts` is non-nil before encoding (`[]accountUsageView{}`).
- `writeJSON(w, http.StatusOK, resp)`.

- [ ] **Step 4: Run the focused tests to verify they pass**

Run: `GOWORK=off go test ./internal/admin -run 'TestProviderUsage' -count=1`
Expected: PASS.

- [ ] **Step 5: Run the admin package**

Run: `GOWORK=off go test ./internal/admin -count=1`
Expected: PASS.

- [ ] **Step 6: Fill the Implementation record and commit**

```bash
git add internal/admin/handlers_usage.go internal/admin/usage_test.go \
  internal/admin/server.go docs/superpowers/plans/2026-11-06-provider-usage-page.md
git commit -m "feat(admin): expose provider usage and balances"
```

**Implementation record**
- Date/status: 2026-11-06 / complete
- Repo/commit: llm-gw / this commit
- Files changed: `internal/admin/handlers_usage.go`, `internal/admin/usage_test.go`, `internal/admin/server.go` (route, cache fields, invalidation), `internal/admin/handlers.go` (cache invalidation on save), plan record.
- Tests/commands and results: `GOWORK=off go test ./internal/admin -run 'TestProviderUsage' -count=1` PASS; `GOWORK=off go test ./internal/admin -count=1` PASS. Tests cover local/upstream join, cache hit and forced refresh, multiple keys and previews, stale-on-error with cached failure, disabled/no-key/unsupported no-network paths, unauthenticated 401, config-reload cache clearing, and local-stats failure.
- Review findings and fixes: Self-review confirmed the handler snapshots providers under `s.mu` but never holds it across network I/O; per-provider queries run concurrently; cache reads/writes use `usageMu`; upstream views are cached without local totals; the raw key is never placed in the response. No fixes were needed after the focused tests.
- Deferred items/limitations: No per-card refresh parameter, no balance history and no generic custom balance URL. Upstream values depend on live vendor endpoints; CommandCode and OpenCode Go were not verified with real keys in the demo.

---

### Task 4: Usage page, client and navigation

**Files:**
- Modify: `web/src/api/types.ts`
- Modify: `web/src/api/client.ts`
- Modify: `web/src/api/client.test.ts`
- Create: `web/src/pages/Usage.tsx`
- Modify: `web/src/App.tsx`
- Modify: `web/src/locales/en-US.ts`
- Modify: `docs/superpowers/plans/2026-11-06-provider-usage-page.md` (Implementation record)

**Interfaces:**
- Consumes: `GET /api/provider-usage`.
- Produces:
  ```ts
  export interface BalancePart { currency?: string; total: number; granted?: number; toppedUp?: number }
  export interface QuotaWindow { name: string; usedPercent: number; resetsAt?: string }
  export interface AccountUsage {
    keyPreview?: string;
    balance?: BalancePart[];
    windows?: QuotaWindow[];
    error?: string;
    stale?: boolean;
    checkedAt?: string;
  }
  export interface ProviderUsageLocal {
    provider?: string;
    requests: number;
    errors: number;
    inputTokens: number;
    outputTokens: number;
    cachedTokens: number;
  }
  export interface ProviderUsage {
    name: string;
    type: string;
    enabled: boolean;
    supported: boolean;
    note?: string;
    local?: ProviderUsageLocal;
    accounts: AccountUsage[];
    checkedAt?: string;
    cached?: boolean;
  }
  export interface ProviderUsageResponse {
    from: string;
    to: string;
    checkedAt: string;
    totals?: ProviderUsageLocal;
    localError?: string;
    providers: ProviderUsage[];
  }

  export function getProviderUsage(q: {
    from: Date;
    to: Date;
    refresh?: boolean;
  }): Promise<ProviderUsageResponse>
  ```
- `getProviderUsage` normalizes `providers` and each `accounts` to `[]` when the server returns null.

- [ ] **Step 1: Write the failing web client tests**

In `web/src/api/client.test.ts`, import `getProviderUsage` and add:

```ts
it('loads provider usage and normalizes null arrays', async () => {
  vi.stubGlobal('document', { cookie: '' });
  vi.stubGlobal(
    'fetch',
    vi.fn(async () =>
      new Response(JSON.stringify({ providers: null, totals: null }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    ),
  );
  await expect(getProviderUsage({ from: new Date(0), to: new Date() })).resolves.toMatchObject({
    providers: [],
  });
});

it('passes refresh to provider usage', async () => {
  vi.stubGlobal('document', { cookie: '' });
  const fetchMock = vi.fn(async () =>
    new Response(JSON.stringify({ providers: [] }), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    }),
  );
  vi.stubGlobal('fetch', fetchMock);
  await getProviderUsage({ from: new Date(0), to: new Date(), refresh: true });
  const url = String((fetchMock.mock.calls[0] as unknown[])[0]);
  expect(url).toContain('refresh=1');
});
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd web && npm test -- --run src/api/client.test.ts`
Expected: FAIL because `getProviderUsage` is not exported.

- [ ] **Step 3: Implement types and client**

Add the interfaces to `web/src/api/types.ts` and:

```ts
export function getProviderUsage(q: { from: Date; to: Date; refresh?: boolean }): Promise<ProviderUsageResponse> {
  const params = new URLSearchParams({
    from: q.from.toISOString(),
    to: q.to.toISOString(),
  });
  if (q.refresh) params.set('refresh', '1');
  return request<ProviderUsageResponse>('GET', `/api/provider-usage?${params.toString()}`).then((r) => ({
    ...r,
    providers: (r.providers ?? []).map((p) => ({ ...p, accounts: p.accounts ?? [] })),
  }));
}
```

- [ ] **Step 4: Run the client tests to verify they pass**

Run: `cd web && npm test -- --run src/api/client.test.ts`
Expected: PASS.

- [ ] **Step 5: Implement the Usage page**

Create `web/src/pages/Usage.tsx` with:

- `RANGES = [{label:'24 小时', ms: 24*3600_000}, {label:'7 天', ms: 7*24*3600_000}, {label:'30 天', ms: 30*24*3600_000}]`.
- State: `rangeMs` (default 24h), `data`, `loading`, `error`.
- `load(refresh = false)` computes `to = new Date()`, `from = new Date(to.getTime()-rangeMs)`, calls `getProviderUsage`, catches into `error`.
- `useEffect(() => { void load(); }, [rangeMs])`.
- Header: `Typography.Title level={4}` "用量", `Segmented`, refresh `Button` with `loading`, last update text `更新于 {time}`.
- `localError` alert.
- Summary row with `Statistic` for requests, errors, input, output, cached tokens. Use `data?.totals` and `—` when absent.
- Provider list:
  ```tsx
  {data?.providers.map((p) => (
    <Card key={p.name} size="small" style={{ marginBottom: 12 }}
      title={<Space wrap>{p.name}<Tag>{p.type}</Tag>{!p.enabled ? <Tag color="default">已禁用</Tag> : null}{p.cached ? <Tag color="blue">缓存</Tag> : null}</Space>}
      extra={p.checkedAt ? <Typography.Text type="secondary">{new Date(p.checkedAt).toLocaleTimeString()}</Typography.Text> : null}>
      ...
    </Card>
  ))}
  ```
  Card body:
  - Local `Descriptions` with `column={isMobile ? 1 : 5}`: requests, errors, input, output, cached.
  - If `p.note`, `<Alert type="info" showIcon message={p.note} />`.
  - If `p.supported` and `p.accounts.length === 0` and no note, text "没有可显示的账户".
  - Accounts:
    ```tsx
    {p.accounts.map((a, i) => (
      <div key={i} style={{ borderTop: i ? '1px solid var(--gw-border)' : undefined, paddingTop: i ? 12 : 0, marginTop: i ? 12 : 0 }}>
        <Space wrap>
          <Typography.Text code>{a.keyPreview || '—'}</Typography.Text>
          {a.stale ? <Tag color="orange">旧数据</Tag> : null}
          {a.checkedAt ? <Typography.Text type="secondary">{new Date(a.checkedAt).toLocaleString()}</Typography.Text> : null}
        </Space>
        {a.balance?.map((b, j) => (
          <div key={j}>{formatMoney(b)}</div>
        ))}
        {a.windows?.map((w) => (
          <div key={w.name} style={{ marginTop: 8 }}>
            <Space style={{ width: '100%', justifyContent: 'space-between' }}>
              <span>{windowLabel(w.name)}</span>
              <Typography.Text type="secondary">
                {w.usedPercent.toFixed(0)}%{w.resetsAt ? ` · ${t('重置')} ${new Date(w.resetsAt).toLocaleString()}` : ''}
              </Typography.Text>
            </Space>
            <Progress percent={Math.round(w.usedPercent)} showInfo={false}
              strokeColor={w.usedPercent >= 90 ? '#dc2626' : w.usedPercent >= 70 ? '#d97706' : '#16a34a'} />
          </div>
        ))}
        {a.error ? <Alert style={{ marginTop: 8 }} type="warning" showIcon message={a.error} /> : null}
      </div>
    ))}
    ```
  - `Empty` when `providers.length===0`.
  - `Skeleton`/loading state.
- Helpers:
  ```ts
  function formatTokens(n?: number): string { ... }
  function currencySymbol(c?: string): string { USD '$', CNY '¥', EUR '€', GBP '£' }
  function formatMoney(b: BalancePart): string {
    const symbol = currencySymbol(b.currency) || b.currency || '';
    let out = `${symbol}${b.total.toFixed(2)}`;
    const parts = [];
    if (b.granted) parts.push(`${t('赠金')} ${symbol}${b.granted.toFixed(2)}`);
    if (b.toppedUp) parts.push(`${t('充值')} ${symbol}${b.toppedUp.toFixed(2)}`);
    return parts.length ? `${out} · ${parts.join(' · ')}` : out;
  }
  function windowLabel(name: string): string {
    if (name === '5h') return t('5 小时');
    if (name === 'weekly') return t('每周');
    if (name === 'monthly') return t('每月');
    return name;
  }
  ```
  The `t` import is `import { translate as t } from '../i18n';`.

- [ ] **Step 6: Wire the page into navigation**

In `web/src/App.tsx`:

- import `BarChartOutlined` from `@ant-design/icons`.
- import `UsagePage` from `./pages/Usage`.
- add nav item after topology:
  ```ts
  { key: 'usage', icon: <BarChartOutlined />, label: '用量' },
  ```
- add `'usage'` to the `Page` union.
- render branch:
  ```tsx
  ) : h === 'usage' ? (
    <UsagePage />
  ) : h === 'providers' ? (
  ```

- [ ] **Step 7: Add translations**

Add Chinese keys and English values to `web/src/locales/en-US.ts`, at minimum:

```ts
'用量': 'Usage',
'24 小时': '24 hours',
'7 天': '7 days',
'30 天': '30 days',
'更新于 {time}': 'Updated {time}',
'请求': 'Requests',
'错误': 'Errors',
'输入 tokens': 'Input tokens',
'输出 tokens': 'Output tokens',
'缓存 tokens': 'Cached tokens',
'余额': 'Balance',
'赠金': 'Granted',
'充值': 'Topped up',
'5 小时': '5 hours',
'每周': 'Weekly',
'每月': 'Monthly',
'重置': 'Resets',
'已禁用': 'Disabled',
'缓存': 'Cached',
'旧数据': 'Stale',
'没有可显示的账户': 'No account to show',
'加载失败': 'Load failed',
```

Do not duplicate keys already present; reuse existing translations for `请求`, `错误`, etc., if they exist.

- [ ] **Step 8: Run the web gates**

Run: `cd web && npx tsc --noEmit && npm test -- --run && npm run build`
Expected: PASS; the build may print the existing chunk-size warning only.

- [ ] **Step 9: Fill the Implementation record and commit**

```bash
git add web/src/api/types.ts web/src/api/client.ts web/src/api/client.test.ts \
  web/src/pages/Usage.tsx web/src/App.tsx web/src/locales/en-US.ts \
  docs/superpowers/plans/2026-11-06-provider-usage-page.md
git commit -m "feat(web): add provider usage page"
```

**Implementation record**
- Date/status: 2026-11-06 / complete
- Repo/commit: llm-gw / this commit
- Files changed: `web/src/api/types.ts`, `web/src/api/client.ts`, `web/src/api/client.test.ts`, `web/src/pages/Usage.tsx`, `web/src/App.tsx`, `web/src/locales/en-US.ts`, plan record.
- Tests/commands and results: `cd web && npm test -- --run` PASS (88 tests, including the two new client tests); `npx tsc --noEmit` PASS; `npm run build` PASS with the existing chunk-size warning only.
- Review findings and fixes: Self-review confirmed the client normalizes null `providers`/`accounts`, the page handles disabled/unsupported/no-key notes, stale errors keep amounts visible, all new UI strings have English entries, and the nav branch uses the existing Drawer. No fixes were needed after the web gates.
- Deferred items/limitations: No browser visual regression test and no per-card refresh. The page uses a single global refresh and a 60-second server cache.

---

### Task 5: Documentation and final verification

**Files:**
- Modify: `README.md`
- Modify: `AGENTS.md`
- Modify: `docs/superpowers/plans/2026-11-06-provider-usage-page.md` (Implementation record)

**Interfaces:**
- No new code interfaces. Documents the page, endpoint, support matrix, cache, and the `cached_tokens` rollup column.

- [ ] **Step 1: Update README**

- Add a "Usage" bullet to "What it does".
- Add a Usage row to the admin console table:
  `| Usage | Per-provider local requests/tokens and upstream balance or quota windows |`
- Add `GET /api/provider-usage` to the Admin API list.
- Add a section after "Request correlation and capture" (or beside it) titled
  "Provider usage and balances" covering:
  - local totals from rollups and the 24h/7d/30d ranges;
  - supported providers and exactly what is shown:
    DeepSeek balance, CommandCode credits/5h/weekly, OpenCode Go
    rolling/weekly/monthly;
  - OpenCode Zen and generic are not queried;
  - disabled providers are not queried; no-key providers show a note;
  - 60-second cache, manual refresh, stale-on-error;
  - keys are never returned, only masked previews; balances are not persisted;
  - `cached_tokens` is exact only for rollups written after this change.
- Mention `cached_tokens` in the stats/rollups description if the README has
  one.

- [ ] **Step 2: Update AGENTS.md**

- Admin API invariants: add the route, auth requirement, 60-second cache,
  `refresh=1`, per-key ordering, masked keys, and the disabled/no-key/
  unsupported rules.
- Stats invariants: add `rollups.cached_tokens` and `ProviderUsage`.
- Gotchas: OpenCode Zen has no per-key balance API; OpenCode Go usage is
  `/zen/go/v1/usage`; CommandCode uses `resetAt` while OpenCode Go uses
  `resetsAt`.
- Keep code/comments/docs English.

- [ ] **Step 3: Run the full Go gate**

Run:

```bash
GOWORK=off go build ./...
GOWORK=off go vet ./...
GOWORK=off go test ./... -count=1
```

Expected: all pass.

- [ ] **Step 4: Run the web gate and rebuild the bundle**

Run:

```bash
cd web
npm test -- --run
npx tsc --noEmit
npm run build
```

Expected: all pass; Vitest count includes the new client tests.

Then from the repository root:

```bash
GOWORK=off go build -o /tmp/llm-gw .
```

Expected: PASS and the embedded bundle includes the Usage page.

- [ ] **Step 5: Manual demo verification**

Run the new binary against a temporary copy of `/tmp/llm-gw-demo.yaml` on
alternate ports (do not print or copy the real keys):

```bash
cp /tmp/llm-gw-demo.yaml /tmp/llm-gw-usage-test.yaml
# change gateway.listen to 127.0.0.1:19787 and admin.listen to 127.0.0.1:19788
/tmp/llm-gw serve -c /tmp/llm-gw-usage-test.yaml
```

- Open the admin console, log in, and open Usage.
- Confirm one card per provider and that DeepSeek shows a real balance.
- The demo config has placeholder keys for CommandCode and OpenCode Go; use
  another config with real keys if available. Otherwise confirm those cards show
  a clear per-account upstream error and never a silent `0`.
- Confirm OpenCode Zen/generic (if present) show a note and make no request.
- Press refresh; confirm the timestamp changes and a second immediate page load
  reports cached data.
- Confirm the browser network response contains only masked key previews.

Record what was verified and any live-vendor limitation in the Implementation
record.

- [ ] **Step 6: Fill the Implementation record and commit**

```bash
git add README.md AGENTS.md docs/superpowers/plans/2026-11-06-provider-usage-page.md
git commit -m "docs: document provider usage and balances"
```

**Implementation record**
- Date/status: 2026-11-06 / complete
- Repo/commit: llm-gw / this commit
- Files changed: `README.md`, `AGENTS.md`, plan record.
- Tests/commands and results: `GOWORK=off go build ./...`, `GOWORK=off go vet ./...`, `GOWORK=off go test ./... -count=1` PASS; web `npm test -- --run` PASS (88 tests), `npx tsc --noEmit` PASS, `npm run build` PASS; `GOWORK=off go build -o /tmp/llm-gw .` PASS.
- Manual verification: started a temporary config on ports 19787/19788 with a known test password and a temp stats database. `GET /api/provider-usage?refresh=1` returned 200, one card per provider, a real DeepSeek CNY balance (`103.04`), clear 401 errors for the demo's placeholder CommandCode/OpenCode Go keys (not silent zeros), and no raw key in the JSON. A second request returned `cached: true` for all providers with the balance preserved. The temporary process and files were removed; the original demo process was left running.
- Review findings and fixes: Self-review confirmed the docs match the implemented endpoint, cache, support matrix and additive `cached_tokens` migration. No fixes were needed after the gates.
- Deferred items/limitations: CommandCode and OpenCode Go live values were not verified because the demo uses placeholder keys; their error paths were. No balance history, generic custom balance URL, per-card refresh or browser visual regression test.


## Final review fix pass (2026-11-06)

- Important: a `refresh=1` that started before an admin save could write its pre-save view back after `invalidateUsageLocked`, leaking the old key's balance onto the new key. Added a usage generation captured with the provider snapshot and checked in `storeUsage`; `TestProviderUsageCacheInvalidationRace` pins it.
- Important: a canceled admin request was cached as a 60-second vendor failure. The handler now stores only when `r.Context().Err() == nil`; `TestProviderUsageCanceledRequestNotCached` pins it.
- Important: keyless or disabled OpenCode Zen / generic cards said `supported:true` and "no API key configured". Type support is resolved before the disabled/keyless early returns; `TestProviderUsageUnsupportedKeyless` pins the Zen console note and generic note with zero calls.
- Minor promoted to fix (security): a vendor error body was truncated to 200 bytes before key redaction, leaking a key prefix when the key straddled the cut. Redaction now runs first; `TestUsageErrorRedactsKeyBeforeTruncating` pins it.
- Minor: provider-level `checkedAt` is now set only when a query is attempted; the disabled, keyless and unsupported tests assert it stays empty.
- Deferred minor: overlapping range loads on the Usage page have no sequence guard; this is recorded in the ledger.
- Fix commit: this commit. Verification: `go test ./internal/admin -run 'TestProviderUsage' -count=1`, `go test ./internal/provider -run 'TestUsage' -count=1`, `go test -race ./internal/admin ./internal/provider`, and the full Go/web gates with the parent workspace.
