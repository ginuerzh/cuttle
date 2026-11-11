# Session Time-Series Chart Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans (native, chosen by the user) to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a 30-bucket sessions/errors bar chart to the Sessions page, backed by a new filtered `GET /api/stats/series` endpoint.

**Architecture:** A store method aggregates `requests` into fixed-width buckets reusing the existing `Filter.where()`; the admin handler shares `handleRequests`'s filter construction (including the entrypoint glob resolution) so the chart and the list can never disagree. The console draws inline SVG from a sparse payload that a pure function pads to one entry per bucket.

**Tech Stack:** Go 1.27 + `database/sql`/SQLite, React 18 + Ant Design, inline SVG, vitest.

**Spec:** `docs/superpowers/specs/2026-11-11-session-time-series-design.md`

## Global Constraints

- cuttle Go commands run with `GOWORK=off`; no `replace` directives.
- `requests`/`rollups` semantics unchanged; the failure definition stays `status >= 400 OR error != ''`.
- The chart and the list must use the same window and the same filters; the entrypoint filter stays glob-based with earlier-entrypoint shadowing.
- Admin endpoints under `/api/` require a session; list-shaped payloads return `[]`, never `null`.
- Console strings are Chinese with `en-US.ts` translations; no new npm dependency.
- Code comments English only. Commit after each task with its Implementation record.

## Review Focus

- A window whose buckets are all empty must render bars at zero height (not "no chart"), and a sparse payload must not shift bars left.
- An unknown `entrypoint` filter must yield an empty series with `200`, exactly like the list endpoint.
- A bad `bucket` value must be rejected (`400`), and absurd values clamped instead of producing millions of buckets.
- A failing series request must not blank the list or the page.
- Zooming to a single bucket must keep one bar and not divide by zero.

---

### Task 1: Backend series endpoint

**Files:**
- Create: `internal/stats/series.go`
- Create: `internal/stats/series_test.go`
- Modify: `internal/admin/handlers_stats.go` (`requestFilter` extraction, `handleSeries`)
- Modify: `internal/admin/server.go` (route)
- Test: `internal/admin/attempts_test.go`-style handler test in `internal/admin/series_test.go`

**Interfaces:**
- Consumes: `stats.Filter`, `Filter.where()`, `failedExpr`, `parseRange`.
- Produces: `stats.SeriesPoint{Bucket time.Time; Requests, Errors int64}`, `stats.Series{Points []SeriesPoint}`, `(*Store).Series(ctx, f Filter, bucket time.Duration) (Series, error)`, `(*Server).requestFilter(r *http.Request) (stats.Filter, bool)`, `GET /api/stats/series` returning `{"bucket":"<duration>","points":[…]}`.

- [ ] **Step 1: Write the failing store test**

`internal/stats/series_test.go`: insert rows at known millisecond timestamps across three buckets (one bucket empty), one failed row; assert `Series` returns ascending points with `requests`/`errors` per bucket, that a `Filter{OnlyErrors:true}` excludes the healthy row, and that a `Filter{Provider:…}` excludes other providers.

- [ ] **Step 2: Run it to verify it fails**

Run: `cd cuttle && GOWORK=off go test ./internal/stats/ -run TestSeries -count=1`
Expected: FAIL — `Series` undefined.

- [ ] **Step 3: Implement `Series`**

`internal/stats/series.go`: bucket width in ms; `SELECT (ts / ?) * ? AS b, COUNT(*), COALESCE(SUM(CASE WHEN <failedExpr> THEN 1 ELSE 0 END),0) FROM requests <where> GROUP BY b ORDER BY b`; scan into `SeriesPoint{Bucket: time.UnixMilli(b).UTC()}`; return `Series{Points: …}` (nil-safe empty slice).

- [ ] **Step 4: Write the failing handler test**

`internal/admin/series_test.go`: `GET /api/stats/series?from&to&bucket=1h&node=…` returns points and echoes `bucket`; an unknown `entrypoint` returns `200` with `"points":[]`; `bucket=abc` returns `400`; a huge `bucket` (e.g. `1000h`) is clamped to `24h`; the same node filter on `/api/requests` returns the same request total as the series sum.

- [ ] **Step 5: Run it to verify it fails**

Run: `cd cuttle && GOWORK=off go test ./internal/admin/ -run TestSeries -count=1`
Expected: FAIL — 404 on `/api/stats/series`.

- [ ] **Step 6: Extract `requestFilter` and add `handleSeries`**

Move the filter construction out of `handleRequests` into `(*Server).requestFilter(r) (stats.Filter, bool)` verbatim (all query params, entrypoint glob resolution and its early-return behaviour); `handleRequests` keeps `Limit`/`Offset` by reading them from the returned filter's query params. `handleSeries` parses `bucket` with `time.ParseDuration` (default `1h`, `400` when unparsable), clamps to `[1s, 24h]`, calls `Series`, and writes `{"bucket":d.String(),"points":points}`. Register `api.HandleFunc("GET /api/stats/series", s.handleSeries)`.

- [ ] **Step 7: Run the Go gate**

Run: `cd cuttle && GOWORK=off go build ./... && GOWORK=off go vet ./... && GOWORK=off go test ./internal/stats/ ./internal/admin/ -count=1`
Expected: PASS.

- [x] **Step 8: Commit** `feat(stats): add a filtered session time series endpoint` and append the Implementation record.

**Implementation record (Task 1)**
- Date/status: 2026-11-11 / complete
- Repo/commit: `ginuerzh/cuttle` (branch `feat/session-time-series`, base `cfcef685`)
- Files: created `internal/stats/series.go`, `internal/stats/series_test.go`, `internal/admin/series_test.go`; modified `internal/admin/handlers_stats.go` (`requestFilter` extraction, `handleSeries`, `parseBucket`, `nonNilPoints`), `internal/admin/server.go` (route).
- Tests: RED `GOWORK=off go test ./internal/stats/ -run TestSeries` → `s.Series undefined`; GREEN same command → ok. RED `./internal/admin/ -run TestSeries` → `GET series = 404`; GREEN → ok. Gate `GOWORK=off go build ./... && go vet ./... && go test ./internal/stats/ ./internal/admin/ -count=1` → both ok.
- Review findings: none (native execution, whole-branch review follows).
- Deferred: none.

---

### Task 2: Console chart

**Files:**
- Create: `web/src/charts/sessionBars.ts`, `web/src/charts/sessionBars.test.ts`
- Create: `web/src/components/SessionChart.tsx`, `web/src/components/SessionChart.test.tsx`
- Modify: `web/src/api/types.ts`, `web/src/api/client.ts`, `web/src/pages/Requests.tsx`, `web/src/locales/en-US.ts`

**Interfaces:**
- Consumes: `GET /api/stats/series`, `getRequests`, `RequestQuery`, `formatClock`, `translate`.
- Produces: `bucketFor(rangeMs: number): number`, `buildBars(points: SeriesPoint[], from: number, to: number, bucket: number): Bar[]` (`Bar{bucket: number; requests: number; errors: number}`), `getSeries(q: SeriesQuery): Promise<SeriesResponse>`, `<SessionChart points from to bucket onSelect selected />`.

- [ ] **Step 1: Write the failing pure-function tests**

`sessionBars.test.ts`: `bucketFor` → 15 m ⇒ 60 000, 1 h ⇒ 120 000, 24 h ⇒ 3 600 000, and a generic range yields 30 buckets rounded to whole minutes within [1 m, 1 h]; `buildBars` pads an empty bucket with zeros in the right position, clips points outside `[from,to)`, and returns exactly `ceil((to-from)/bucket)` entries (also for a single-bucket window).

- [ ] **Step 2: Run them to verify they fail**

Run: `cd cuttle/web && npx vitest run src/charts/sessionBars.test.ts`
Expected: FAIL — module not found.

- [ ] **Step 3: Implement the pure functions**

`bucketFor` and `buildBars` as specified; keep them free of React and of `Date` formatting so SSR and unit tests agree.

- [ ] **Step 4: Write the failing component test**

`SessionChart.test.tsx`: renders one `<rect>` per bar (including zero-height ones), stacks the error bar in `#dc2626` above the sessions bar, exposes `title` with `HH:MM · N 会话 / M 错误`, calls `onSelect` with the bucket start on click and again with the same value to clear (second click), renders 「窗口内没有会话」 for empty data, and 「趋势加载失败」 when `failed` is set.

- [ ] **Step 5: Run it to verify it fails**

Run: `cd cuttle/web && npx vitest run src/components/SessionChart.test.tsx`
Expected: FAIL — component not found.

- [ ] **Step 6: Implement the component and wire the page**

`SessionChart.tsx` as specified (inline SVG, `aria-label`, no dependency). `types.ts` + `client.ts`: `SeriesPoint`, `SeriesResponse`, `getSeries` reusing `getRequests`' param construction plus `bucket`. `Requests.tsx`: fetch the series in the existing `Promise.all`, add `zoom` state (`{from, to} | null`) used by both queries, render the chart above the list, reset with a 「清除」 control, keep the list working when the series fails. Add the Chinese keys and their `en-US.ts` entries.

- [ ] **Step 7: Run the web and Go gates**

Run: `cd cuttle/web && npx vitest run && npx tsc --noEmit && npm run build` then `cd cuttle && GOWORK=off go build ./... && GOWORK=off go vet ./... && GOWORK=off go test ./... -count=1`
Expected: PASS.

- [ ] **Step 8: Commit** `feat(web): chart sessions and errors over time` and append the Implementation record.
