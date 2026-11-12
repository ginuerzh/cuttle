# Balance curve — design

Date: 2026-11-12
Repository: `ginuerzh/cuttle`
Status: proposed

## Intent

The Usage page answers "how much is left" only as a single number, read when
someone opens it: `GET /api/provider-usage` queries each provider once (60 s
cache) and throws the number away. The missing view is the *trajectory* — when
the money left, how fast, and whether a top-up happened between two visits.
magpie draws exactly this on each key's card (`internal/provider/balance_history.go`
plus a curve), and its rules for turning occasional readings into a usable line
are the reference here.

Chosen approach: persist every balance reading in `stats.sqlite`, sample on a
background timer (default every 10 minutes) *and* on every fresh page read, and
draw one line per provider key inside its provider card.

## Goals

- `balance_points` in `stats.sqlite` keeps per provider key and currency the
  readings (time + amount), deduplicated, cleaned up with the rollups.
- A background sampler records readings without anyone watching; the page's own
  fresh reads record too. Both follow the same dedupe rules.
- `GET /api/provider-usage` carries, per account, its readings for the
  requested window, bounded to a few hundred points.
- Each provider card on the Usage page draws a smooth monotone line (one line
  per key and currency) with a soft area fill, a legend and per-point tooltips.
- Fewer than two readings renders a muted hint instead of a chart.

## Non-goals

- No quota-window curves (OpenCode Go / CommandCode `usedPercent`) in v1.
- No runs-out projection, no low-balance alerts, no spend accounting.
- No new chart library and no bars/line toggle.
- No backfill: history starts at the first reading after this ships.

## Anti-goals

- Never add amounts of different currencies together.
- Never fabricate a line: no interpolation stored as a point, no point from a
  failed or stale read, no point for a provider that reports no balance.
- Never let the sampler bypass, poison or invalidate the 60 s upstream cache.
- The curve must not disagree with the number beside it on the same card.

## Constraints from the repository

- `provider.UsageProvider.Usage(ctx)` yields `[]AccountUsage{KeyIndex, Balance,
  Windows, Note, Error, CheckedAt}`; `buildProviderUsage` turns those into
  `accountUsageView` and falls back to the previous card with `Stale` on error.
- The usage cache is per provider id, 60 s, failures included, and is dropped on
  reload through `usageGeneration`.
- Periodic admin work already exists as `admin.Server.StartProbes(ctx)`, started
  from `main.go` only when the admin server is enabled; the sampler follows that
  shape.
- Stats schema changes are additive (`CREATE TABLE IF NOT EXISTS` statements in
  `internal/stats/store.go`); `PRAGMA user_version` stays 2, which belongs to the
  identity migration.
- `Store.Cleanup(ctx, requestsBefore, rollupsBefore, capturesBefore)` is the only
  retention sweep; balance points go with `rollupsBefore`.
- Charts are inline SVG with no dependency (so they render in SSR tests); console
  source strings are Chinese and translated in `web/src/locales/en-US.ts`.
- `usage.sampleInterval` is a new optional key: `yaml.KnownFields(true)` accepts
  it, and `applyDefaults`/`Validate` must handle it like the other durations.

## Design

### Storage (`internal/stats`)

```sql
CREATE TABLE IF NOT EXISTS balance_points (
    provider TEXT NOT NULL,    -- provider id
    key      TEXT NOT NULL,    -- provider key id
    currency TEXT NOT NULL,    -- as reported ("USD", "CNY"); "" when absent
    at       INTEGER NOT NULL, -- unix ms of the reading
    amount   REAL NOT NULL,    -- remaining total for that currency
    PRIMARY KEY (provider, key, currency, at)
);
CREATE INDEX IF NOT EXISTS idx_balance_points_at ON balance_points(at);
```

- `stats.BalancePoint{Provider, Key, Currency string; At time.Time; Amount float64}`.
- `Store.RecordBalancePoints(ctx, points []BalancePoint) error` — one transaction:
  for each `(provider, key, currency)`, read the last two points, then
  - drop the new point when it is not after the last (clock skew, repeat read);
  - replace the last point when the new one is within `balancePointGap` (5 min)
    **of the second-to-last** and its amount did not rise, or when the new
    amount equals the last two (a flat run keeps one sliding point). Comparing
    with the second-to-last, not the last, is what keeps a page refreshed every
    minute leaving points about 5 minutes apart instead of collapsing to one;
    a top-up (a rise) is always appended, so it is never lost;
  - otherwise insert.
- Retention is `Cleanup`'s alone (see below); `RecordBalancePoints` prunes
  nothing, so a raised `stats.retainRollups` really extends the history.
- `Store.BalanceSeries(ctx, from, to time.Time, maxPoints int) ([]BalanceGroup, error)`
  with `BalanceGroup{Provider, Key, Currency string; Points []BalancePoint}`:
  grouped, ordered by `at` ascending, and downsampled to at most `maxPoints`
  per group by keeping the last reading of each bucket of
  `width = max(1ms, ceil((to-from)/maxPoints))`. A group with no readings is
  absent. Groups are keyed by id only; labels are resolved in the admin layer.
- `Cleanup` adds `DELETE FROM balance_points WHERE at < rollupsBefore`. Points
  therefore live as long as rollups (2160 h by default, 168 h for requests).

### Config

```yaml
usage:
  sampleInterval: 10m   # 0 disables the background sampler
```

- `config.Usage{SampleInterval *Duration}` on `config.Config`: absent (`nil`)
  means the 10 m default, while an explicit `0` disables the background
  sampler. A plain `Duration` cannot tell those apart in `applyDefaults`
  (decoded zero vs. set to zero), which is why the field is a pointer like
  `enabled` and `capture.sampleRate`.
- A positive value is clamped to `[5m, 24h]` with a warning. The 5 m floor
  matches the store's dedupe gap: a shorter interval would spend vendor calls
  and still resolve at 5 minutes.
- `0` still records on page reads. `example.yaml` stays unchanged (the block is
  optional); `README.md` documents it.

### Sampler (`internal/admin/sampler.go`)

- `func (s *Server) StartUsageSampler(ctx context.Context)` — sample once
  immediately, then loop with a `time.Timer` whose duration is re-read from
  `s.cfg` every round, so a SIGHUP interval change applies without a restart.
  When the interval is `0` the loop idles on a 30 s poll of the config (no
  upstream calls), so enabling it later also works live.
- `func (s *Server) SampleUsageOnce(ctx context.Context)` — snapshot
  `s.cfg.Providers` under `s.mu`; for each enabled provider whose type reports
  usage (`provider.UsageSupport`), with at least one enabled key: build it,
  call `Usage(ctx)`, and record every account with `Error == ""` and a
  non-empty `Balance`. `KeyIndex` maps to `cfg.Keys[i].ID`. Every reading of one
  pass carries the caller's `now`, so a sampling pass or a page render stamps
  them all with one instant.
- Extraction: the upstream call moves into one helper used by both
  `buildProviderUsage` and the sampler, returning accounts that already carry
  their key id, so the view and the sampler cannot drift. `buildProviderUsage`
  keeps the cache, the stale fallback and the note handling.
- Recording from the page path: `buildProviderUsage` records points for its
  fresh results only — never for a cache hit, never for a `Stale` fallback.
  Every reading of one pass carries the handler's `now`.
- Both paths call one `s.recordBalancePoints(ctx, providerID, accounts, now)`
  helper. A stats write failure is logged at warn and swallowed: it must never
  fail the page.
- Runs are sequential: the loop waits out the interval, samples, then waits
  again, so two passes can never overlap. A slow pass only delays the next one.

### API

`GET /api/provider-usage` gains, per account:

```json
{
  "keyId": "pk_0123456789",
  "trends": [
    {
      "currency": "USD",
      "points": [
        { "at": "2026-11-12T03:00:00Z", "amount": 11.12 }
      ]
    }
  ]
}
```

- `accountUsageView` gains `KeyID string \`json:"keyId,omitempty"\`` and
  `Trends []balanceTrendView \`json:"trends,omitempty"\``.
  `balanceTrendView{Currency string; Points []balancePointView}`, points
  `{at RFC3339, amount}`.
- The handler calls `statsStore.BalanceSeries(ctx, from, to, 180)` once per
  request, beside `ProviderUsage`, and attaches groups by (provider id, key id),
  matching currency text case-insensitively.
- Order matters: a fresh page read records its points first, then queries the
  series with `to2 = max(to, now)`, because the reading just taken is later than
  the browser's `to` and would otherwise fall outside its own response. The
  response echoes `From`/`To2`, and the client draws its x-domain from those, so
  the newest reading and the number beside it cannot disagree. The card totals
  keep using the requested `to`; the difference is the milliseconds of the read.
- Trends are attached *after* the per-provider cache decision, so a cached card
  still gets fresh points and a range change needs no vendor call.
- An account without a key id (an upstream account with no configured key) gets
  no trends.

### Frontend

- `web/src/charts/balanceLine.ts` (pure, unit-tested):
  - `monotonePath(points)` — Fritsch–Carlson monotone cubic interpolation (the
    same construction as magpie's `ledCurve`), so the curve passes through each
    point and never overshoots between them.
  - `trendGeometry(points, from, to, maxY, width, height)` — x from the window
    (the response's `from`/`to`), y from 0 to a **shared** `maxY` with headroom;
    a single point sits at the right edge. The maximum is computed once per card
    across all its trends: a $0.50 line beside a $500 line must not look the
    same.
  - Currency formatting reuses the page's `currencySymbol` (moved to a shared,
    tested module).
- `web/src/components/BalanceChart.tsx` — inline SVG, one `<path>` per trend
  (line plus soft area fill) on a shared y-scale, a `<title>` per point with
  local time and amount, a legend row per trend (`key name · $`), `aria-label`
  summary, fixed height. Fewer than two points in every trend renders the muted
  「余额历史不足，采样中」 line.
- `web/src/pages/Usage.tsx` — `ProviderCard` renders the chart under the
  `Descriptions` and above the account rows, only while 统计 is on (the range
  presets are the chart's window).
- `web/src/api/types.ts` — `AccountUsage.keyId`, `AccountUsage.trends`,
  `BalanceTrend`, `BalanceTrendPoint`; `api/client.ts` unchanged.
- New i18n keys (Chinese source + `en-US.ts`): `余额曲线`,
  `余额历史不足，采样中`, `{name} 余额`.

## Edge cases and failure behaviour

- First run: one point, no line, the muted hint; the curve appears with the
  second reading. No padded or invented history.
- A window longer than the stored history: the line starts at its first point;
  nothing is drawn to the window's left edge.
- Multi-currency accounts: one line per currency, never summed; the legend shows
  each sign.
- Providers that only report windows: no chart at all (v1 draws money only).
- Failed reads: nothing recorded; the card keeps today's error/stale alert.
- A deleted key keeps its points but is not drawn. Re-adding the same key id
  resumes the line, because points are keyed by immutable ids.
- Stats disabled (`s.stats == nil`): no trends, no chart, the sampler is a no-op.
- Reload: each tick reads the current config, so removed providers stop being
  sampled and renames keep their line.
- A reading whose time is not after the stored last point is dropped.
- A run that takes longer than the interval simply delays the next sampling;
  runs never overlap because they are sequential.
- A very low balance (0 or negative, as some vendors report a minus) draws on
  the same 0-based scale; the tooltip shows the real value.

## Testing

- Go `internal/stats`: merge rules (near gap replaces, equal run collapses,
  out-of-order dropped, a top-up after a close reading is kept), series
  downsampling (last per bucket, ≤ `maxPoints`, ordered), no cross-key or
  cross-currency mixing, cleanup deletes old points.
- Go `internal/admin`: `SampleUsageOnce` against an `httptest` DeepSeek balance
  upstream records one point per key and currency; a failed read records
  nothing; a disabled provider is skipped; `KeyIndex` → key id mapping; an
  interval of 0 samples nothing; a sub-5 m interval is clamped; the handler
  attaches trends for the requested window and also on a cache hit; the
  response's newest point is the reading that request itself took; stats-disabled
  returns no trends; a stats write failure does not fail the request.
- Web: `monotonePath` passes through its points and stays within their range;
  geometry for one/two/N points, for a flat line, and for a shared maximum
  across two trends; one path per trend; empty state; both locales carry the new
  keys.
- Gates: `GOWORK=off go build ./...`, `go vet ./...`, `go test ./... -count=1`,
  `npm test`, `npx tsc --noEmit`, `npm run build` (then rebuild the binary).

## Decisions taken here (check these while reading)

1. Background sampler plus record-on-read (approach ②); read-only sampling was
   rejected because a curve that only exists while someone watches is useless.
2. Points live in `stats.sqlite`, not a JSON file: the store already owns
   retention, atomicity and concurrency.
3. At most one point per 5 minutes per (key, currency), compared with the
   second-to-last reading; equal runs collapse; a rise (top-up) is always
   appended. No backfill.
4. The trend travels in `GET /api/provider-usage` per account rather than a
   separate endpoint, so the chart and the number cannot disagree and the page
   keeps one request. Bounded to 180 points per trend.
5. One line per key and currency. No provider total and no currency summing.
6. Inline SVG smooth line with area fill on a shared y-scale per card; no
   toggle, no chart library.
7. The sampler runs only with the admin server; `usage.sampleInterval` is a
   pointer (`nil` = 10 m, `0` = off, clamped to ≥5 m) while page reads keep
   recording.
8. v1 draws money balances only; window percentages and runs-out projections are
   follow-ups.
9. Retention belongs to `Store.Cleanup` alone, tied to `rollupsBefore`; the
   store prunes nothing on write, so raising `retainRollups` extends the history.
10. Points keyed by immutable provider/key ids and the currency text exactly as
    the vendor reports it.
11. A fresh page read records first and queries the series up to `max(to, now)`;
    the response echoes that window so the newest reading is visible in the very
    response that produced it.
12. Trends are attached after the 60 s cache decision: they are stats reads, not
    vendor calls, so a cache hit still gets live points.

## Left to the builder

- Exact SVG height, padding, palette, area opacity, and whether the last point
  gets a dot.
- Legend wording, and whether a chart with a single trend hides its legend.
- Whether the chart sits directly under `Descriptions` or after the note/alert.
- Whether `BalanceSeries` downsamples in SQL or in Go; the contract is at most
  `maxPoints` ordered points per group.
