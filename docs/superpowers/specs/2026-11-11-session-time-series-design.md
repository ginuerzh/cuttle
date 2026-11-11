# Session time-series chart — design

Date: 2026-11-11
Repository: `ginuerzh/cuttle`
Status: proposed

## Intent

The Sessions page answers "what happened in this window" only as a list of up to
200 rows. During the `oc-go-free` 429 investigation the missing view was the
*shape* of the window: when the failures started, whether they clustered, and
whether traffic dropped. A bar chart of sessions and errors per time bucket,
sharing the page's filters, closes that gap.

Chosen approach (C in the evaluation): a real backend series, not client-side
bucketing of the loaded rows (which is biased by the 200-row limit) and not the
hourly `ErrorSummary.Hourly` series (which ignores the page filters).

## Goals

- `GET /api/stats/series` returns `{bucket, points: [{bucket, requests, errors}]}`
  for a window, honouring exactly the same filters as `GET /api/requests`.
- The Sessions page draws a 30-bucket stacked bar chart above the list: sessions
  solid, errors red, empty buckets at zero height, hover shows
  `HH:MM · N 会话 / M 错误`.
- Clicking a bar narrows the list (and the chart) to that bucket; clicking it
  again clears the zoom.
- No new frontend dependency; the chart is inline SVG so it renders in SSR tests.

## Non-goals

- No live/streaming updates: the chart refreshes with the page's existing load.
- No stacking by error class, no per-model/per-node series, no smoothing.
- No change to `ErrorSummary`/`/api/stats/errors`, `rollups`, or the 200-row list
  limit.

## Anti-goals

- The chart must never disagree with the list: same window, same filters, same
  failure definition (`status >= 400 OR error != ''`).
- No client-side "totals" derived from the truncated list.
- No bucket smaller than one second or more than 200 buckets.

## Constraints from the repository

- `stats.Filter.where()` already builds the WHERE clause; the series query must
  reuse it rather than duplicate filter logic.
- The entrypoint filter is not a column: `handleRequests` resolves it to
  client-model globs plus the shadowing excludes of every earlier entrypoint.
  That resolution must be shared, or the chart and the list will disagree.
- Admin routes live under `requireAuth`; list-shaped endpoints return `[]`, never
  `null`.
- `requests.ts` is indexed; the window is bounded by `retainRequests` (168 h).
- Console strings are Chinese, translated in `web/src/locales/en-US.ts`.

## Design

### Backend (`cuttle/internal/stats`, `cuttle/internal/admin`)

- `Filter` gains nothing; add a new store method in a new file
  `internal/stats/series.go`:

  ```go
  type SeriesPoint struct {
      Bucket   time.Time `json:"bucket"`
      Requests int64     `json:"requests"`
      Errors   int64     `json:"errors"`
  }
  type Series struct {
      Bucket time.Duration `json:"-"`
      Points []SeriesPoint `json:"points"`
  }
  func (s *Store) Series(ctx context.Context, f Filter, bucket time.Duration) (Series, error)
  ```

  SQL: `SELECT (ts / ?) * ? AS b, COUNT(*), SUM(CASE WHEN <failed> THEN 1 ELSE 0 END)
  FROM requests <where> GROUP BY b ORDER BY b`, with `bucket` in milliseconds.
  Sparse result: only non-empty buckets are returned; the client fills the gaps
  so a quiet hour is visibly zero rather than absent.
- `handleRequests`'s filter construction (including the entrypoint glob
  resolution and its "unknown entrypoint ⇒ empty result" behaviour) moves into a
  shared helper on `*Server`:

  ```go
  // requestFilter builds the stats filter for a list/series request. ok is false
  // when the request names an entrypoint that no longer exists.
  func (s *Server) requestFilter(r *http.Request) (f stats.Filter, ok bool)
  ```

  Both `handleRequests` and the new `handleSeries` use it.
- `GET /api/stats/series?from&to&bucket=<duration>&<same filters>`:
  - `bucket` parsed with `time.ParseDuration`, default `1h`, clamped to
    `[1s, 24h]`; invalid values are rejected with `400` rather than silently
    coerced.
  - Response: `{"bucket":"2m0s","points":[{"bucket":"2026-11-11T15:02:00Z","requests":12,"errors":3}]}`,
    points ascending, `[]` when the window is empty.
  - `ok == false` returns `{"bucket":…,"points":[]}` with `200`, matching the
    list endpoint's empty result.

### Frontend (`cuttle/web/src`)

- `charts/sessionBars.ts` (pure, unit-tested):
  - `bucketFor(rangeMs: number): number` → 15 m: 1 m, 1 h: 2 m, 24 h: 1 h,
    generic `rangeMs / 30` rounded to a whole minute, clamped to [1 m, 1 h].
  - `buildBars(points, from, to, bucket)` → exactly `ceil((to-from)/bucket)`
    entries in order, filling missing buckets with `{requests: 0, errors: 0}`.
- `components/SessionChart.tsx`: inline SVG, 30 bars max, sessions as the full
  bar and errors stacked in `#dc2626`, a baseline, `title` per bar, click
  callback with the bucket start, `aria-label` summary. Empty data renders a
  muted "窗口内没有会话" line instead of an empty axis.
- `api/client.ts`: `getSeries(q)` mirroring `getRequests`' query construction
  (same filter params + `bucket`), `types.ts`: `SeriesPoint`, `SeriesResponse`.
- `RequestsPage`: fetch the series in the same `Promise.all` as the list; keep a
  `zoom: {from, to} | null` state used by both the list query and the chart;
  clicking a bar sets `zoom`, a "清除" button (or a second click on the same bar)
  clears it. Bucket width and `from`/`to` are derived from the current range
  unless zoomed.

## Edge cases and failure behaviour

- `from >= to` or an empty window: no bars, the muted message.
- Fewer points than buckets (sparse) and more points than buckets (bucket
  changed): `buildBars` clips/pads deterministically.
- A zoomed window narrower than one bucket keeps a single bar.
- The series request failing must not blank the list: the chart shows a muted
  "趋势加载失败" line and the list renders as today.
- `errorsOnly`/`capturedOnly`/`errorClass` filters apply to the series too, so a
  filtered list and its chart always agree (an errors-only window draws bars that
  are entirely errors, which is intended).

## Testing

- Go: `Series` bucket arithmetic, gap sparsity, error counting, filter parity
  (provider/node/key/errorClass/entrypoint), bucket clamping, and the handler's
  `400` on a bad duration and `[]` for an unknown entrypoint.
- Web: `bucketFor` per preset, `buildBars` gap fill/clip, SVG bar count and
  stacked error heights, click callback, empty and failed states, i18n keys.

## Decisions taken here (check these while reading)

- Backend series (C), 30 buckets, sessions + errors only.
- Sparse server payload; gap filling in one pure client function.
- Clicking a bar zooms the whole page (list + chart) rather than filtering a
  separate copy of the data.
- No chart library.
- The series endpoint shares `handleRequests`'s filter builder; the entrypoint
  glob resolution moves there rather than being duplicated.

## Left to the builder

- Exact SVG dimensions/margins and how the bar title is composed.
- Whether the zoom reset is a button, a second click, or both.
- The bucket label format (local `HH:MM` vs. `HH:MM:SS` for sub-hour buckets).
- Where the chart sits relative to the 错误概览 block.
