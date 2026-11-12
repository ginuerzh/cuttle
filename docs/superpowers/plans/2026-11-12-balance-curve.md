# Balance Curve Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Record every provider balance reading cuttle sees, sample them in the
background, and draw a smooth balance curve inside each provider card on the
Usage page.

**Architecture:** Readings land in a new additive `balance_points` table in
`stats.sqlite` (keyed by provider id, provider-key id, currency). Two writers
feed it: a background sampler owned by the admin server (`usage.sampleInterval`,
default 10 m) and every fresh `/api/provider-usage` read. The same endpoint
returns a downsampled trend per account for the requested window, and the
console draws one smooth monotone SVG line per key and currency.

**Tech Stack:** Go 1.27 (`GOWORK=off`), SQLite, React 18 + Ant Design + Vite,
Vitest, inline SVG (no chart library).

**Spec:** `docs/superpowers/specs/2026-11-12-balance-curve-design.md`

## Global Constraints

- Run every Go command inside `cuttle/` with `GOWORK=off`; the parent workspace
  `go.work` would otherwise mask the tagged module graph.
- Do not edit sibling repositories (`../core`, `../x`, `../llm-api-converter`).
- No new Go or npm dependencies.
- Storage holds ids only: provider id, provider-key id, currency text. Never a
  display name, never a key value.
- UI source strings are Chinese (`web/src/**`) and must have an `en-US` entry in
  `web/src/locales/en-US.ts`; the English panel tests fail on any CJK character
  that reaches an English render.
- The console bundle (`web/`) builds into `internal/admin/dist`; run
  `npm run build` before the final Go build when web files changed.
- Commit after each task, including its Implementation record, in the same
  commit. Never push.

## Review Focus

1. **Multi-currency balances** (`[{USD,…},{CNY,…}]` on one key): two lines, same
   key, never summed, no crash. Task 5 and Task 6 tests.
2. **A top-up (amount rises) moments after a reading**: the rise must never be
   swallowed by the 5-minute merge. Task 2 test.
3. **A page read a second after the last one, with the browser clock slightly
   behind the server**: the newest reading still appears in the response that
   produced it, and no duplicate point is stored. Task 2 and Task 5 tests.
4. **A key deleted from the config while its history exists**: no orphan line,
   no panic; re-adding the same key id resumes the curve. Task 5 test.
5. **Stats store disabled or a failing store write**: the Usage page still
   renders today's cards, the sampler logs and keeps going. Task 3, Task 4 and
   Task 5 tests.

---

### Task 1: `usage.sampleInterval` config key

**Files:**
- Modify: `internal/config/config.go` (Config struct, `Usage` type, `applyDefaults`)
- Modify: `internal/admin/handlers.go` (`handlePutConfig`: preserve an absent `usage`)
- Test: `internal/config/config_test.go`, `internal/admin/usage_test.go`
- Modify: `README.md` (Usage section)

**Interfaces:**
- Consumes: nothing.
- Produces: `config.Usage{SampleInterval *Duration}`,
  `config.Config.Usage Usage` (`yaml:"usage,omitempty" json:"usage,omitempty"`),
  and constants `config.DefaultUsageSampleInterval = 10 * time.Minute`,
  `config.UsageSampleMin = 5 * time.Minute`, `config.UsageSampleMax = 24 * time.Hour`.
  After `ApplyDefaults`: `nil` never remains; `<= 0` becomes `0`; a positive
  value is clamped into `[UsageSampleMin, UsageSampleMax]`.
  `PUT /api/config` keeps the stored `usage` when the body omits it (the console
  never sends it), while an explicit `{"usage":{"sampleInterval":"0"}}` wins.

- [x] **Step 1: Write the failing test**

```go
func TestApplyDefaultsUsageSampleInterval(t *testing.T) {
	d := func(v time.Duration) *Duration { x := Duration(v); return &x }
	cases := []struct {
		name string
		in   *Duration
		want time.Duration
	}{
		{"absent defaults to 10m", nil, 10 * time.Minute},
		{"zero disables sampling", d(0), 0},
		{"negative is off", d(-time.Minute), 0},
		{"below the floor clamps to 5m", d(time.Minute), 5 * time.Minute},
		{"above the ceiling clamps to 24h", d(48 * time.Hour), 24 * time.Hour},
		{"in range is kept", d(30 * time.Minute), 30 * time.Minute},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{Usage: Usage{SampleInterval: tc.in}}
			ApplyDefaults(cfg)
			if cfg.Usage.SampleInterval == nil {
				t.Fatal("sampleInterval is nil after defaults")
			}
			if got := time.Duration(*cfg.Usage.SampleInterval); got != tc.want {
				t.Fatalf("sampleInterval = %v, want %v", got, tc.want)
			}
		})
	}
}
```

- [x] **Step 2: Run test to verify it fails**

Run: `GOWORK=off go test ./internal/config -run TestApplyDefaultsUsageSampleInterval -count=1`
Expected: FAIL with `undefined: Usage`.

- [x] **Step 3: Add the field, the type and the defaulting**

Add to `Config` after `Stats`:

```go
Usage Usage `yaml:"usage,omitempty" json:"usage,omitempty"`
```

New type with a doc comment saying absent means 10 minutes and an explicit `0`
disables the sampler (a pointer is required to tell the two apart), and the
exported constants. In `applyDefaults`, after the capture defaults:

```go
if c.Usage.SampleInterval == nil {
	v := Duration(DefaultUsageSampleInterval)
	c.Usage.SampleInterval = &v
}
switch v := time.Duration(*c.Usage.SampleInterval); {
case v < 0:
	*c.Usage.SampleInterval = 0
case v > 0 && v < UsageSampleMin:
	*c.Usage.SampleInterval = Duration(UsageSampleMin)
case v > UsageSampleMax:
	*c.Usage.SampleInterval = Duration(UsageSampleMax)
}
```

- [x] **Step 4: Run the test to verify it passes**

Run: `GOWORK=off go test ./internal/config -run TestApplyDefaultsUsageSampleInterval -count=1`
Expected: PASS.

- [x] **Step 5: Preserve the setting across an admin save**

`handlePutConfig` decodes a fresh `config.Config`, so a console save would drop
`usage` and reset the interval to the 10 m default (turning an explicit `0` back
into sampling). Beside the existing gateway-key/provider-key preservation add:

```go
if c.Usage.SampleInterval == nil {
	if dst.Usage.SampleInterval != nil {
		v := *dst.Usage.SampleInterval
		c.Usage.SampleInterval = &v
	}
}
```

Test in `internal/admin/usage_test.go` (`TestPutConfigPreservesUsageSampling`):
set `srv.cfg.Usage.SampleInterval` to `0`, log in, GET `/api/config`, PUT that
body back with the CSRF token, expect `204`, and assert the stored interval is
still `0`. Then PUT a body carrying `{"usage":{"sampleInterval":"20m"}}` and
assert it became 20 m.

- [x] **Step 6: Run the config and admin tests**

Run: `GOWORK=off go test ./internal/config ./internal/admin -count=1`
Expected: PASS, including `example.yaml` still loading.

- [x] **Step 7: Document the knob in `README.md`**

Under the provider-usage / Usage page section, one paragraph: what the curve is,
that readings come from a background sampler every `usage.sampleInterval`
(default `10m`, `0` disables it, accepted range 5m–24h), that page visits record
too, and that history starts when the feature is first run.

- [x] **Step 8: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go \
  internal/admin/handlers.go internal/admin/usage_test.go README.md
git commit -m "feat(config): add usage.sampleInterval"
```

**Implementation record — Task 1**
- Date/status: 2026-11-12 / complete
- Repo/commit: `ginuerzh/cuttle` (branch `feat/balance-curve`, base `f50a3633`)
- Files changed: `internal/config/config.go` (`Usage` type, `Usage` field on `Config`, three bound constants, `applyDefaults` normalization), `internal/config/config_test.go` (`TestApplyDefaultsUsageSampleInterval`), `internal/admin/handlers.go` (`handlePutConfig` preserves an absent `usage`), `internal/admin/usage_test.go` (`TestPutConfigPreservesUsageSampling`), `README.md` (Usage section: the curve, the sampler and the knob; dropped the "not persisted" claim).
- Tests run: RED `GOWORK=off go test ./internal/config -run TestApplyDefaultsUsageSampleInterval -count=1` → `undefined: Usage`; GREEN same → ok. RED `GOWORK=off go test ./internal/admin -run TestPutConfigPreservesUsageSampling -count=1` → `sampleInterval = 0x…, want the stored 0`; GREEN same → ok. Gate `GOWORK=off go test ./internal/config ./internal/admin -count=1` → both ok.
- Review findings/fixes: none in-task (native execution; whole-branch review follows). Ruling: the README documents the finished behaviour while the sampler lands in Task 4 — the branch ships as a whole; cost if wrong: one intermediate commit reads slightly ahead of the code.
- Deferred: none.

---

### Task 2: `balance_points` store, merge rules and series

**Files:**
- Create: `internal/stats/balance.go`
- Test: `internal/stats/balance_test.go`
- Modify: `internal/stats/store.go` (schema statements, `Cleanup`)

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces:
  - `stats.BalancePoint{Provider, Key, Currency string; At time.Time; Amount float64}`
  - `stats.BalanceGroup{Provider, Key, Currency string; Points []BalancePoint}`
  - `(*Store).RecordBalancePoints(ctx context.Context, points []BalancePoint) error`
  - `(*Store).BalanceSeries(ctx context.Context, from, to time.Time, maxPoints int) ([]BalanceGroup, error)`
  - `const stats.DefaultBalanceSeriesPoints = 180`
  - unexported `mergeBalancePoint(pts []BalancePoint, next BalancePoint) []BalancePoint`
  - table `balance_points(provider, key, currency, at INTEGER PRIMARY KEY, amount REAL)` with `idx_balance_points_at(at)`.

- [x] **Step 1: Write the failing tests**

```go
func TestMergeBalancePointRules(t *testing.T) {
	base := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	p := func(at time.Time, amount float64) BalancePoint {
		return BalancePoint{Provider: "p_1", Key: "pk_1", Currency: "USD", At: at, Amount: amount}
	}

	// an out-of-order reading is dropped
	got := mergeBalancePoint([]BalancePoint{p(base, 10)}, p(base.Add(-time.Minute), 9))
	if len(got) != 1 || got[0].Amount != 10 {
		t.Fatalf("out-of-order = %+v", got)
	}

	// an equal run keeps one sliding point
	pts := []BalancePoint{p(base, 10), p(base.Add(time.Minute), 10)}
	got = mergeBalancePoint(pts, p(base.Add(2*time.Minute), 10))
	if len(got) != 2 || !got[1].At.Equal(base.Add(2*time.Minute)) {
		t.Fatalf("flat run = %+v", got)
	}

	// a closer reading that did not rise moves the last point
	pts = []BalancePoint{p(base, 10), p(base.Add(time.Minute), 9)}
	got = mergeBalancePoint(pts, p(base.Add(2*time.Minute), 8))
	if len(got) != 2 || !got[1].At.Equal(base.Add(2*time.Minute)) || got[1].Amount != 8 {
		t.Fatalf("close reading = %+v", got)
	}

	// a top-up is always appended, even two minutes later
	pts = []BalancePoint{p(base, 10), p(base.Add(time.Minute), 9)}
	got = mergeBalancePoint(pts, p(base.Add(2*time.Minute), 49))
	if len(got) != 3 || got[2].Amount != 49 {
		t.Fatalf("top-up = %+v", got)
	}
}

func TestBalanceSeriesDownsamples(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	from := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
	var pts []BalancePoint
	for i := 0; i < 10; i++ {
		pts = append(pts, BalancePoint{Provider: "p_1", Key: "pk_1", Currency: "USD",
			At: from.Add(time.Duration(i) * 10 * time.Minute), Amount: float64(100 - i)})
	}
	pts = append(pts, BalancePoint{Provider: "p_1", Key: "pk_2", Currency: "CNY",
		At: from.Add(time.Minute), Amount: 7})
	if err := s.RecordBalancePoints(ctx, pts); err != nil {
		t.Fatal(err)
	}
	// 10-minute spacing is wider than the merge gap, so all ten readings stay;
	// 100 minutes over 3 points keeps the last reading of each third.
	got, err := s.BalanceSeries(ctx, from, from.Add(100*time.Minute), 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("groups = %+v", got)
	}
	usd := got[0]
	if usd.Key != "pk_1" || len(usd.Points) != 3 {
		t.Fatalf("usd group = %+v, want 3 points of pk_1", usd)
	}
	if usd.Points[2].Amount != 91 {
		t.Fatalf("last usd amount = %v, want the reading at +90m", usd.Points[2].Amount)
	}
	for i := 1; i < len(usd.Points); i++ {
		if !usd.Points[i].At.After(usd.Points[i-1].At) {
			t.Fatalf("points not ascending: %+v", usd.Points)
		}
	}
}
```

Add one more test in the same file, `TestCleanupDeletesBalancePoints`, that
records a point at `now-48h` and one at `now`, runs
`s.Cleanup(ctx, cutoff, cutoff, cutoff)`, and asserts only the recent point
survives `BalanceSeries(now-72h, now+time.Minute, 10)`.

- [x] **Step 2: Run tests to verify they fail**

Run: `GOWORK=off go test ./internal/stats -run 'TestMergeBalancePointRules|TestBalanceSeriesDownsamples|TestCleanupDeletesBalancePoints' -count=1`
Expected: FAIL with `undefined: BalancePoint`.

- [x] **Step 3: Implement the table, the merge and the series**

In `internal/stats/store.go` add to the schema statement list:

```sql
CREATE TABLE IF NOT EXISTS balance_points (
    provider TEXT NOT NULL,
    key      TEXT NOT NULL,
    currency TEXT NOT NULL,
    at       INTEGER NOT NULL,
    amount   REAL NOT NULL,
    PRIMARY KEY (provider, key, currency, at)
);
CREATE INDEX IF NOT EXISTS idx_balance_points_at ON balance_points(at);
```

and to `Cleanup`, before the return:

```go
if _, err := s.db.ExecContext(ctx, `DELETE FROM balance_points WHERE at < ?`,
	rollupsBefore.UnixMilli()); err != nil {
	return err
}
```

`mergeBalancePoint` semantics: two points with the same amount before the new one
make it a flat run (replace the last); otherwise a new reading closer than
`balancePointGap` (5 min) **to the second-to-last** point that did not rise above
the last replaces the last; everything else is appended. Points never go
backwards in time.

The reading time is the caller's `now`, not `acc.CheckedAt`: one sampling pass or
one page request stamps all its readings with the same instant, and tests can
place points anywhere.

`RecordBalancePoints` opens one transaction, groups the input by
`(Provider, Key, Currency)` preserving order, loads the last two rows of each
group ascending, applies `mergeBalancePoint`, and writes the tail with
`DELETE` of the replaced `at` (when the last point moved) plus
`INSERT OR IGNORE`. `BalanceSeries` selects `[from, to)` ordered by
provider/key/currency/at, groups, and keeps the last reading per bucket of
`width = max(1ms, (to-from)/maxPoints)` with `maxPoints` defaulting to
`DefaultBalanceSeriesPoints` when `<= 0`.

- [x] **Step 4: Run the stats package tests**

Run: `GOWORK=off go test ./internal/stats -count=1`
Expected: PASS.

- [x] **Step 5: Commit**

```bash
git add internal/stats/balance.go internal/stats/balance_test.go internal/stats/store.go
git commit -m "feat(stats): store provider balance readings"
```

**Implementation record — Task 2**
- Date/status: 2026-11-12 / complete
- Repo/commit: `ginuerzh/cuttle` (branch `feat/balance-curve`, base `5c873e8e`)
- Files changed: created `internal/stats/balance.go` (`BalancePoint`, `BalanceGroup`, `mergeBalancePoint`, `RecordBalancePoints`, `BalanceSeries`, `balancePointGap`, `DefaultBalanceSeriesPoints`), `internal/stats/balance_test.go`; modified `internal/stats/store.go` (schema: `balance_points` + `idx_balance_points_at`; `Cleanup` deletes points older than `rollupsBefore`).
- Tests run: RED `GOWORK=off go test ./internal/stats -run 'TestMergeBalancePointRules|TestBalanceSeriesDownsamples|TestCleanupDeletesBalancePoints' -count=1` → `undefined: BalancePoint`; GREEN same → ok. Gate `GOWORK=off go test ./internal/stats -count=1` → ok (23.0s), `go vet ./internal/stats` → clean.
- Review findings/fixes: none in-task. Merge rule follows the spec (gap measured against the second-to-last point, rises always appended, flat runs keep one sliding point); retention only in `Cleanup`.
- Deferred: none.

---

### Task 3: share the usage query and record fresh readings

**Files:**
- Modify: `internal/admin/handlers_usage.go`
- Test: `internal/admin/usage_test.go`

**Interfaces:**
- Consumes: `stats.BalancePoint`, `(*stats.Store).RecordBalancePoints`, `BalanceSeries`.
- Produces:
  - `queryUsage(ctx context.Context, cfg config.Provider) (accounts []accountUsageView, note string, unsupported bool)` — the single upstream read used by both the page and the sampler; accounts carry `KeyID`.
  - `(*Server).recordBalancePoints(ctx context.Context, providerID string, accounts []accountUsageView, now time.Time)`
  - `accountUsageView.KeyID string` (`json:"keyId,omitempty"`).

- [x] **Step 1: Write the failing test**

```go
func TestProviderUsageRecordsBalancePoints(t *testing.T) {
	srv, _, _, ts := newTestAdmin(t)
	vendor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"balance_infos":[{"currency":"USD","total_balance":"11.12"}]}`))
	}))
	defer vendor.Close()
	setUsageProviders(t, srv, config.Provider{
		Name: "deepseek", Type: "deepseek", Chat: vendor.URL + "/v1",
		Keys: []config.ProviderKey{{Name: "key-1", Key: "sk-secret-1234"}},
	})
	srv.mu.Lock()
	p := srv.cfg.Providers[0]
	srv.mu.Unlock()

	client, _ := login(t, ts.URL)
	if resp := doJSON(t, client, http.MethodGet, ts.URL+"/api/provider-usage", "", ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	groups, err := srv.stats.BalanceSeries(context.Background(), time.Now().Add(-time.Minute), time.Now().Add(time.Minute), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 {
		t.Fatalf("groups = %+v", groups)
	}
	g := groups[0]
	if g.Provider != p.ID || g.Key != p.Keys[0].ID || g.Currency != "USD" {
		t.Fatalf("group = %+v, want provider id %q and key id %q", g, p.ID, p.Keys[0].ID)
	}
	if len(g.Points) != 1 || g.Points[0].Amount != 11.12 {
		t.Fatalf("points = %+v", g.Points)
	}
}
```

Add `TestProviderUsageRecordsNothingForFailedAccount`: a vendor replying `500`
must leave `BalanceSeries` empty, while the card keeps today's error note.

- [x] **Step 2: Run tests to verify they fail**

Run: `GOWORK=off go test ./internal/admin -run 'TestProviderUsageRecords' -count=1`
Expected: FAIL (`srv.stats` has no readings — `len(groups) == 0`).

- [x] **Step 3: Extract `queryUsage` and record on the fresh path**

Move the body of `buildProviderUsage` from `provider.New` through account
mapping into `queryUsage`, keeping the existing note/supported semantics exactly
(the extraction must not change any current `usage_test.go` assertion):

- `provider.New` error → `(nil, err.Error(), true)`
- not a `UsageProvider` → `(nil, "provider does not support usage queries", true)`
- `UsageUnsupportedError` → `(nil, ue.Note, true)`; any other error →
  `(nil, err.Error(), false)`
- success → the account views, `unsupported=false`, with `KeyID` set from
  `cfg.Keys[acc.KeyIndex].ID` when the index is in range.

`buildProviderUsage` keeps `UsageSupport`, enabled and no-key checks, calls
`queryUsage`, sets `Supported`/`Note`, and keeps the per-account stale fallback.
It takes the request's instant as a parameter — `buildProviderUsage(ctx, cfg,
old, now)` — so the readings it records share the handler's `now`; Task 5's
series window steps one millisecond past that same value.

Call `s.recordBalancePoints(r.Context(), cfg.ID, accounts, now)` where the view
is built from fresh accounts — after `queryUsage`, before the stale fallback
(only `Error == ""` accounts contribute, so error accounts record nothing).
`recordBalancePoints` maps `Balance` parts to `stats.BalancePoint` using the
account's `KeyID`, skips accounts with an empty `KeyID`, and logs a store error
at warn without failing the request.

- [x] **Step 4: Run the admin package tests**

Run: `GOWORK=off go test ./internal/admin -count=1`
Expected: PASS, including every pre-existing `TestProviderUsage*` case.

- [x] **Step 5: Commit**

```bash
git add internal/admin/handlers_usage.go internal/admin/usage_test.go
git commit -m "refactor(admin): share the usage query and record fresh balances"
```

**Implementation record — Task 3**
- Date/status: 2026-11-12 / complete
- Repo/commit: `ginuerzh/cuttle` (branch `feat/balance-curve`, base `7d014006`)
- Files changed: `internal/admin/handlers_usage.go` (`accountUsageView.KeyID`; `queryUsage` extraction; `buildProviderUsage` now takes the request's `now` and records fresh readings; new `(*Server).recordBalancePoints`), `internal/admin/usage_test.go` (`TestProviderUsageRecordsBalancePoints`, `TestProviderUsageRecordsNothingForFailedAccount`).
- Tests run: RED `GOWORK=off go test ./internal/admin -run TestProviderUsageRecords -count=1` → `groups = []`; GREEN same → ok. Gate `GOWORK=off go test ./internal/admin -count=1` → ok (16.9s), `go vet ./internal/admin` → clean.
- Review findings/fixes: preserved the old `CheckedAt` semantics — a provider-level unsupported reason still sets no timestamp while a transient read failure does (`note == "" || !unsupported`). Stale fallback now indexes positionally (`oldAccount(old, i)`), which equals the upstream `KeyIndex` order every provider adapter emits; the pre-existing `TestProviderUsageFailureKeepsLastGood` passes.
- Deferred: none.

---

### Task 4: background balance sampler

**Files:**
- Create: `internal/admin/sampler.go`
- Test: `internal/admin/sampler_test.go`
- Modify: `main.go` (start beside `StartProbes`)

**Interfaces:**
- Consumes: `queryUsage`, `(*Server).recordBalancePoints`, `config.Usage.SampleInterval`.
- Produces:
  - `(*Server).StartUsageSampler(ctx context.Context)`
  - `(*Server).SampleUsageOnce(ctx context.Context, now time.Time)`
  - `(*Server).usageSampleInterval() time.Duration` (0 when sampling is off)

- [x] **Step 1: Write the failing tests**

```go
func TestSampleUsageOnceRecordsPoints(t *testing.T) {
	srv, _, _, _ := newTestAdmin(t)
	var amount atomic.Value
	amount.Store("11.12")
	vendor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"balance_infos":[{"currency":"USD","total_balance":%q}]}`, amount.Load())
	}))
	defer vendor.Close()
	setUsageProviders(t, srv, config.Provider{
		Name: "deepseek", Type: "deepseek", Chat: vendor.URL + "/v1",
		Keys: []config.ProviderKey{{Name: "key-1", Key: "sk-1"}},
	})

	base := time.Now().UTC()
	srv.SampleUsageOnce(context.Background(), base)
	amount.Store("10.50")
	srv.SampleUsageOnce(context.Background(), base.Add(10*time.Minute))

	groups, err := srv.stats.BalanceSeries(context.Background(), base.Add(-time.Minute), base.Add(11*time.Minute), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 || len(groups[0].Points) != 2 {
		t.Fatalf("groups = %+v, want two points", groups)
	}
	if got := groups[0].Points[1].Amount; got != 10.50 {
		t.Fatalf("second amount = %v", got)
	}
}
```

Add: `TestSampleUsageOnceSkipsDisabledProvider` (disabled provider → zero vendor
calls), `TestUsageSamplerOffWhenIntervalZero` (interval 0, run
`StartUsageSampler` for ~50 ms, assert zero vendor calls), and
`TestSampleUsageOnceIgnoresFailedRead` (vendor 500 → no points).

- [x] **Step 2: Run tests to verify they fail**

Run: `GOWORK=off go test ./internal/admin -run 'TestSampleUsageOnce|TestUsageSampler' -count=1`
Expected: FAIL with `srv.SampleUsageOnce undefined`.

- [x] **Step 3: Implement the sampler and wire it in `main.go`**

`usageSampleInterval` reads `srv.cfg` under `srv.mu` and returns `0` for a nil
pointer or a non-positive value.

`SampleUsageOnce` snapshots `cfg.Providers`, skips disabled providers and those
whose type fails `provider.UsageSupport`, calls `queryUsage`, and records each
account with `recordBalancePoints(ctx, p.ID, accounts, now)`. Sequential by
construction: it is only called by the loop, never concurrently.

`StartUsageSampler` loops: read the interval; when it is `0`, wait 30 s (or ctx)
and re-read; otherwise wait the interval, then `SampleUsageOnce(ctx, time.Now())`.
Start it in `main.go` beside `adm.StartProbes(probeCtx)` with the same context.
Log the effective interval once at startup (`info`) and each time it changes.

- [x] **Step 4: Run the admin package tests**

Run: `GOWORK=off go test ./internal/admin -count=1`
Expected: PASS.

- [x] **Step 5: Build the binary to prove the wiring compiles**

Run: `GOWORK=off go build ./...`
Expected: success.

- [x] **Step 6: Commit**

```bash
git add internal/admin/sampler.go internal/admin/sampler_test.go main.go
git commit -m "feat(admin): sample provider balances in the background"
```

**Implementation record — Task 4**
- Date/status: 2026-11-12 / complete
- Repo/commit: `ginuerzh/cuttle` (branch `feat/balance-curve`, base `6158e115`)
- Files changed: created `internal/admin/sampler.go` (`StartUsageSampler`, `SampleUsageOnce`, `usageSampleInterval`, `usageSampleIdle`), `internal/admin/sampler_test.go` (4 tests); modified `main.go` (starts the sampler on the probes' context).
- Tests run: RED `GOWORK=off go test ./internal/admin -run 'TestSampleUsageOnce|TestUsageSampler' -count=1` → `srv.SampleUsageOnce undefined`; GREEN same → ok. Gate `GOWORK=off go test ./internal/admin -count=1` → ok (19.8s); `GOWORK=off go build ./...` → ok.
- Review findings/fixes: `SampleUsageOnce` returns early when the stats store is nil and when `s.cfg` is nil; the interval loop re-reads the config every round (SIGHUP applies), logs once per effective value, and idles 30 s while off without touching a vendor.
- Deferred: none.

---

### Task 5: balance trends in `GET /api/provider-usage`

**Files:**
- Modify: `internal/admin/handlers_usage.go`
- Test: `internal/admin/usage_test.go`

**Interfaces:**
- Consumes: `(*stats.Store).BalanceSeries`, `stats.DefaultBalanceSeriesPoints`, `accountUsageView.KeyID`.
- Produces:
  - `accountUsageView.Trends []balanceTrendView` (`json:"trends,omitempty"`)
  - `balanceTrendView{Currency string; Points []balancePointView}`
  - `balancePointView{At string; Amount float64}` — `at` is RFC3339 UTC, points ascending.

- [ ] **Step 1: Write the failing tests**

```go
func TestProviderUsageTrends(t *testing.T) {
	srv, _, _, ts := newTestAdmin(t)
	vendor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"balance_infos":[{"currency":"USD","total_balance":"11.12"},{"currency":"CNY","total_balance":"3.5"}]}`))
	}))
	defer vendor.Close()
	setUsageProviders(t, srv, config.Provider{
		Name: "deepseek", Type: "deepseek", Chat: vendor.URL + "/v1",
		Keys: []config.ProviderKey{{Name: "key-1", Key: "sk-1"}},
	})
	srv.mu.Lock()
	p := srv.cfg.Providers[0]
	srv.mu.Unlock()

	base := time.Now().UTC().Add(-2 * time.Hour)
	err := srv.stats.RecordBalancePoints(context.Background(), []stats.BalancePoint{
		{Provider: p.ID, Key: p.Keys[0].ID, Currency: "USD", At: base, Amount: 20},
		{Provider: p.ID, Key: p.Keys[0].ID, Currency: "USD", At: base.Add(time.Hour), Amount: 11.12},
	})
	if err != nil {
		t.Fatal(err)
	}

	client, _ := login(t, ts.URL)
	got, raw := decodeUsage(t, doJSON(t, client, http.MethodGet, ts.URL+"/api/provider-usage", "", ""))
	if len(got.Providers) != 1 || len(got.Providers[0].Accounts) != 1 {
		t.Fatalf("providers = %+v", got.Providers)
	}
	a := got.Providers[0].Accounts[0]
	if a.KeyID != p.Keys[0].ID {
		t.Fatalf("keyId = %q, want %q", a.KeyID, p.Keys[0].ID)
	}
	if len(a.Trends) != 2 {
		t.Fatalf("trends = %+v, want one per currency", a.Trends)
	}
	if a.Trends[0].Currency != "CNY" || a.Trends[1].Currency != "USD" {
		t.Fatalf("trend currency order = %+v", a.Trends)
	}
	// The seeded USD line plus the reading this request took from the vendor.
	pts := a.Trends[1].Points
	if len(pts) != 3 || pts[0].Amount != 20 || pts[2].Amount != 11.12 {
		t.Fatalf("usd points = %+v", pts)
	}
	if strings.Contains(raw, "sk-1") {
		t.Fatalf("response leaked the key: %s", raw)
	}
}
```

Also add:

- `TestProviderUsageTrendsOnCacheHit` — two requests inside 60 s; assert the
  second is `Cached` and still carries trends.
- `TestProviderUsageTrendIncludesFreshReading` — one request on an empty store;
  assert the reading it took appears as the only point of the returned trend
  (the window extension).
- `TestProviderUsageTrendSkipsUnknownKey` — a trend stored under a key id that
  is not configured produces no trends on any account.
- `TestProviderUsageNoTrendsWithoutStats` — on the `newTestAdmin` server (same
  package) set `srv.stats = nil`, request the endpoint, assert `200`, the cards
  still present, and no `trends` key anywhere in the body.

- [ ] **Step 2: Run tests to verify they fail**

Run: `GOWORK=off go test ./internal/admin -run 'TestProviderUsageTrend' -count=1`
Expected: FAIL (`KeyID`/`Trends` undefined or empty).

- [ ] **Step 3: Implement the trend attachment**

In `handleProviderUsage`, after `wg.Wait()` and with the same `statsStore`
snapshot:

```go
if statsStore != nil {
	seriesTo := to
	if now.After(seriesTo) {
		seriesTo = now
	}
	// The series bound is exclusive, and a reading taken by this very request
	// lands at now: step one millisecond past it so it is not filtered out.
	seriesTo = seriesTo.Add(time.Millisecond)
	groups, err := statsStore.BalanceSeries(r.Context(), from, seriesTo, stats.DefaultBalanceSeriesPoints)
	// group by provider id -> key id -> currency, sort currencies, attach
}
```

Attach with a helper `attachBalanceTrends(views []providerUsageView, groups []stats.BalanceGroup)`:
match `group.Provider == view.ID` and `group.Key == account.KeyID`, currency
compared case-insensitively but the emitted `Currency` is the stored text,
accounts with an empty `KeyID` and groups with no matching account are skipped.
A series error is logged at warn and leaves trends empty; it never fails the
request. Trend attachment happens for cached and fresh views alike (the cache
holds only the card).

- [ ] **Step 4: Run the admin package tests**

Run: `GOWORK=off go test ./internal/admin -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/admin/handlers_usage.go internal/admin/usage_test.go
git commit -m "feat(admin): return balance trends with provider usage"
```

**Implementation record — Task 5**
- Date/status: pending
- Repo/commit: `ginuerzh/cuttle` —
- Files changed:
- Tests run:
- Review findings/fixes:
- Deferred:

---

### Task 6: Web types, curve math and the chart component

**Files:**
- Modify: `web/src/api/types.ts` (`AccountUsage.keyId`, `AccountUsage.trends`,
  `BalanceTrend`, `BalanceTrendPoint`)
- Modify: `web/src/format.ts` and `web/src/format.test.ts` (move
  `currencySymbol` out of `Usage.tsx`)
- Modify: `web/src/pages/Usage.tsx` (drop the local `currencySymbol`, import it)
- Create: `web/src/charts/balanceLine.ts`, `web/src/charts/balanceLine.test.ts`
- Create: `web/src/components/BalanceChart.tsx`, `web/src/components/BalanceChart.test.tsx`

**Interfaces:**
- Consumes: `BalanceTrend`/`BalanceTrendPoint` from `api/types.ts`.
- Produces:
  - `format.ts`: `export function currencySymbol(currency?: string): string`
  - `balanceLine.ts`:
    `export interface TrendPoint { at: number; amount: number }`,
    `export function trendPoints(points: BalanceTrendPoint[]): TrendPoint[]` (parse, drop
    unparsable, sort ascending),
    `export function trendMax(trends: BalanceTrend[], from: number, to: number): number`
    (largest amount inside the window, at least 1),
    `export function trendGeometry(points: TrendPoint[], from: number, to: number, maxY: number, width: number, height: number): [number, number][]`
    (x clamped into `[0, width]`, y from the bottom),
    `export function monotonePath(points: [number, number][]): string`
  - `BalanceChart.tsx`: `export interface ChartLine { name: string; trend: BalanceTrend }`
    and `export function BalanceChart({ lines, from, to }: { lines: ChartLine[]; from: string; to: string })`

- [ ] **Step 1: Write the failing math and component tests**

`balanceLine.test.ts`:

```ts
it('passes through every point and never overshoots', () => {
  const pts: [number, number][] = [[0, 40], [10, 10], [20, 30]];
  const d = monotonePath(pts);
  expect(d.startsWith('M0.0 40.0')).toBe(true);
  expect(d.trim().endsWith('20.0 30.0')).toBe(true);
  // every control point and midpoint stays inside the points' y range
  // (parse the C segments; midpoint y must be within [10, 40])
});

it('scales every trend of a card against one shared maximum', () => {
  const trends: BalanceTrend[] = [
    { currency: 'USD', points: [{ at: '2026-11-12T00:00:00Z', amount: 1 }] },
    { currency: 'CNY', points: [{ at: '2026-11-12T00:00:00Z', amount: 100 }] },
  ];
  expect(trendMax(trends, from, to)).toBe(100);
});
```

`BalanceChart.test.tsx` (jsdom, like `SessionChart.test.tsx`): asserts one
`path[data-series="balance"]` per line, one `circle[data-point]` per reading with
a `<title>` containing the amount and currency, a shared scale (a reading of 1
against a peak of 100 sits in the bottom tenth of the SVG), the empty state
「余额历史不足，采样中」 when every line has fewer than two points, and an
`en-US` render with no CJK.

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd web && npx vitest run src/charts/balanceLine.test.ts src/components/BalanceChart.test.tsx`
Expected: FAIL (modules not found).

- [ ] **Step 3: Implement the types, the math and the component**

`monotonePath` is Fritsch–Carlson: slopes per segment, tangents clamped so the
curve never leaves the local interval (`m[i] = 0` at a sign change, otherwise
`sign * min(|s1|, |s2|, |p|/2)`), emitted as `C` segments; one point emits `M`.
Coordinates are formatted with `toFixed(1)`.

`BalanceChart` renders a fixed-height inline SVG (72 px, 4 px padding) with one
`<path>` per line (`fill` of the same colour at `fill-opacity="0.12"` for the
area, plus the stroked line), an invisible `circle` per reading carrying the
`<title>`, a legend row built from `line.name` and
`currencySymbol(line.trend.currency)`, and `aria-label` 余额曲线. Palette:
`['#0ea5e9', '#16a34a', '#d97706', '#7c3aed', '#dc2626', '#0891b2']` by line
index.

Types:

```ts
export interface BalanceTrendPoint { at: string; amount: number }
export interface BalanceTrend { currency: string; points: BalanceTrendPoint[] }
```

`AccountUsage` gains `keyId?: string` and `trends?: BalanceTrend[]`.

- [ ] **Step 4: Run the web tests and type check**

Run: `cd web && npx vitest run src/charts/balanceLine.test.ts src/components/BalanceChart.test.tsx && npx tsc --noEmit`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/api/types.ts web/src/format.ts web/src/format.test.ts \
  web/src/pages/Usage.tsx web/src/charts web/src/components/BalanceChart.tsx \
  web/src/components/BalanceChart.test.tsx
git commit -m "feat(web): balance curve math and chart component"
```

**Implementation record — Task 6**
- Date/status: pending
- Repo/commit: `ginuerzh/cuttle` —
- Files changed:
- Tests run:
- Review findings/fixes:
- Deferred:

---

### Task 7: draw the curve on each provider card

**Files:**
- Modify: `web/src/pages/Usage.tsx` (`ProviderCard`, `UsagePage`)
- Test: `web/src/pages/Usage.test.ts`
- Modify: `web/src/locales/en-US.ts`

**Interfaces:**
- Consumes: `BalanceChart`, `ChartLine`, `AccountUsage.trends`.
- Produces: no new exports; `ProviderCard` gains `from` and `to` props (RFC3339
  from the response) and renders the chart only while 统计 is on.

- [ ] **Step 1: Write the failing page test**

```ts
it('draws a balance curve per account trend', () => {
  const html = renderToStaticMarkup(
    createElement(ProviderCard, {
      isMobile: false,
      showStats: true,
      from: '2026-11-12T00:00:00Z',
      to: '2026-11-13T00:00:00Z',
      provider: {
        id: 'p_1', name: 'deepseek', type: 'deepseek', enabled: true, supported: true,
        accounts: [{
          name: 'key-1', keyId: 'pk_1', keyPreview: 'sk-...1', enabled: true,
          balance: [{ currency: 'USD', total: 11.12 }],
          trends: [{ currency: 'USD', points: [
            { at: '2026-11-12T01:00:00Z', amount: 20 },
            { at: '2026-11-12T02:00:00Z', amount: 11.12 },
          ] }],
        }],
      },
    }),
  );
  expect(html).toContain('data-series="balance"');
  expect(html).toContain('key-1');
});
```

Plus: a `showStats: false` render contains no chart, and an `en-US` render of
the same card matches no CJK.

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd web && npx vitest run src/pages/Usage.test.ts`
Expected: FAIL (`ProviderCard` does not accept `from`/`to`, no chart in the HTML).

- [ ] **Step 3: Wire the chart and translate the strings**

`ProviderCard` builds `lines` from
`provider.accounts.flatMap((a) => (a.trends ?? []).map((trend) => ({ name: a.name || a.keyPreview || '', trend })))`
and renders `<BalanceChart lines={lines} from={from} to={to} />` under the
`Descriptions` block, above the account rows, when `showStats && lines.length > 0`.
Export it (`export function ProviderCard`) so the page test can render it in
isolation, as `AccountRow` already is. `UsagePage` passes `data.from`/`data.to`
down. Add the `en-US` entries for `余额曲线` and `余额历史不足，采样中` (and any
other new source string).

- [ ] **Step 4: Run the full web gate and rebuild the bundle**

Run: `cd web && npm test && npx tsc --noEmit && npm run build`
Expected: PASS; `internal/admin/dist` refreshed.

- [ ] **Step 5: Run the full Go gate**

Run: `GOWORK=off go build ./... && GOWORK=off go vet ./... && GOWORK=off go test ./... -count=1`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add web/src/pages/Usage.tsx web/src/pages/Usage.test.ts web/src/locales/en-US.ts
git commit -m "feat(web): draw a balance curve on each provider card"
```

**Implementation record — Task 7**
- Date/status: pending
- Repo/commit: `ginuerzh/cuttle` —
- Files changed:
- Tests run:
- Review findings/fixes:
- Deferred:
