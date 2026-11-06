# Per-node decode token rate Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Show a per-node output token rate close to model decode speed by storing TTFT for streaming requests and subtracting it from total duration.

**Architecture:** The rewriter sink records the first SSE response event time in `stats.Facts`. The recorder stores `ttft_ms` with the request. The graph aggregator sums streaming output tokens and `duration_ms - ttft_ms`, then exposes `outputTokensPerSecond` on each node. The web console formats and displays that value for binding nodes.

**Tech Stack:** Go, SQLite, React, Ant Design, React Flow, Vitest.

**Spec:** `docs/superpowers/specs/2026-11-06-node-token-rate-design.md`

## Global Constraints

- Do not edit sibling repositories (`../core`, `../x`, `../llm-api-converter`).
- Run Go commands with `GOWORK=off`.
- Add or update tests with every behavior change; do not weaken assertions.
- Keep timestamps and rates out of the persisted rollups; the graph reads request detail.
- The rate is streaming-only and output-token-only.
- Existing databases must migrate additively; no destructive migration and no backfill.
- UI source strings stay Chinese and get an English entry in `web/src/locales/en-US.ts`.
- Commit after each task with its Implementation record in the same commit.
- Never push.

## Review Focus

- A stream whose first SSE event is not a content token (role/start metadata) must still produce a plausible rate, not a divide-by-zero or absurd value.
- A request with zero output tokens, or with `duration_ms <= ttft_ms`, must not enter the rate aggregate; a failed stream that still produced output remains a valid decode sample.
- A node with no streaming samples must omit the rate rather than display `0 tok/s`.
- Non-streaming and non-SSE streaming traffic must keep the existing stats and UI behavior.
- Old databases and old rows must open and render without the new column being present in historical data.

---

### Task 1: Capture first response time in facts and the rewriter

**Files:**
- Modify: `internal/stats/facts.go`
- Modify: `internal/rewriter/rewriter.go`
- Test: `internal/stats/facts_test.go` (create)
- Test: `internal/rewriter/rewriter_test.go`

**Interfaces:**
- Consumes: existing `Facts`, `Fact`, `rewriter.Sink`, `Rewriter.Rewrite`.
- Produces: `Fact.FirstResponseAt time.Time`; `(*Facts).MarkFirstResponse(sid string, at time.Time)`; `Sink.MarkFirstResponse(sid string, at time.Time)`; `Rewriter.Rewrite` calls it for the first non-`end` response SSE event.

- [ ] **Step 1: Write the failing facts test**

Add to `internal/stats/facts_test.go`:

```go
func TestMarkFirstResponseKeepsFirstTimestamp(t *testing.T) {
	f := NewFacts()
	first := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	f.MarkFirstResponse("sid", first)
	f.MarkFirstResponse("sid", first.Add(time.Second))

	fact, ok := f.Take("sid")
	if !ok || !fact.FirstResponseAt.Equal(first) {
		t.Fatalf("first response = %v, want %v", fact.FirstResponseAt, first)
	}
}
```

- [ ] **Step 2: Run the facts test and verify it fails**

Run: `GOWORK=off go test ./internal/stats -run TestMarkFirstResponseKeepsFirstTimestamp -count=1`

Expected: FAIL because `(*Facts).MarkFirstResponse` is undefined.

- [ ] **Step 3: Implement the first-response timestamp**

Add `FirstResponseAt time.Time` to `Fact`. Add:

```go
// MarkFirstResponse records the first response event for sid exactly once.
func (f *Facts) MarkFirstResponse(sid string, at time.Time) {
	if f == nil || sid == "" {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()

	fact := f.m[sid]
	if fact.FirstResponseAt.IsZero() {
		fact.FirstResponseAt = at
		fact.Updated = at
		f.m[sid] = fact
	}
}
```

- [ ] **Step 4: Run the facts test and verify it passes**

Run: `GOWORK=off go test ./internal/stats -run TestMarkFirstResponseKeepsFirstTimestamp -count=1`

Expected: PASS.

- [ ] **Step 5: Write the failing rewriter test**

Add `marked int` and `markedAt time.Time` to `sinkStub`, implement:

```go
func (s *sinkStub) MarkFirstResponse(sid string, at time.Time) {
	if s.marked == 0 {
		s.markedAt = at
	}
	s.marked++
}
```

Add:

```go
func TestRewriteMarksFirstResponseOnce(t *testing.T) {
	sink := &sinkStub{}
	rw := New(engine.New(engine.Options{}), Options{Sink: sink, Protocol: "chat"})
	meta := func(direction, phase string) corerewriter.RewriteOption {
		return corerewriter.MetadataRewriteOption(map[string]any{
			"sid": "sid-1", "direction": direction,
			"uri": "/v1/chat/completions", "sse_phase": phase,
		})
	}

	if _, err := rw.Rewrite(context.Background(), []byte(`data: {"choices":[]}`), meta("response", "start")); err != nil {
		t.Fatal(err)
	}
	first := sink.markedAt
	if sink.marked != 1 || first.IsZero() {
		t.Fatalf("after start: marked=%d at=%v", sink.marked, first)
	}

	if _, err := rw.Rewrite(context.Background(), []byte(`data: {"choices":[]}`), meta("response", "event")); err != nil {
		t.Fatal(err)
	}
	if sink.marked != 2 || !sink.markedAt.Equal(first) {
		t.Fatalf("after event: marked=%d at=%v, want first %v", sink.marked, sink.markedAt, first)
	}

	if _, err := rw.Rewrite(context.Background(), nil, meta("response", "end")); err != nil {
		t.Fatal(err)
	}
	if _, err := rw.Rewrite(context.Background(), []byte(`{}`), meta("request", "")); err != nil {
		t.Fatal(err)
	}
	if sink.marked != 2 {
		t.Fatalf("end/request marked first response: %d", sink.marked)
	}
}
```

- [ ] **Step 6: Run the rewriter test and verify it fails**

Run: `GOWORK=off go test ./internal/rewriter -run TestRewriteMarksFirstResponseOnce -count=1`

Expected: FAIL because `MarkFirstResponse` is missing from the `Sink` interface and `Rewrite` does not call it.

- [ ] **Step 7: Wire the call in `Rewriter.Rewrite`**

Add `MarkFirstResponse(sid string, at time.Time)` to `rewriter.Sink`. In `Rewrite`, after `meta` is built and before `eng.Rewrite`:

```go
if strings.EqualFold(meta.Direction, "response") &&
	meta.SSEPhase != "" &&
	!strings.EqualFold(meta.SSEPhase, "end") &&
	r.opts.Sink != nil {
	r.opts.Sink.MarkFirstResponse(meta.SID, time.Now())
}
```

Import `time` if needed.

- [ ] **Step 8: Run the rewriter and facts tests and verify they pass**

Run: `GOWORK=off go test ./internal/rewriter ./internal/stats -run 'TestRewriteMarksFirstResponseOnce|TestMarkFirstResponseKeepsFirstTimestamp' -count=1`

Expected: PASS.

- [ ] **Step 9: Commit Task 1**

```bash
git add internal/stats/facts.go internal/stats/facts_test.go internal/rewriter/rewriter.go internal/rewriter/rewriter_test.go docs/superpowers/plans/2026-11-06-node-token-rate.md
git commit -m "feat(stats): capture first streamed response time"
```

**Implementation record:**
- Date/status: 2026-11-06 / complete
- Repository + commit: `llm-gw` `da379166` (code); this docs commit follows
- Files changed: `internal/stats/facts.go`, `internal/stats/facts_test.go`, `internal/rewriter/rewriter.go`, `internal/rewriter/rewriter_test.go`, plus the spec and plan files
- Tests/commands:
  - RED: `GOWORK=off go test ./internal/stats -run TestMarkFirstResponseKeepsFirstTimestamp -count=1` → build failed, `Facts.MarkFirstResponse` and `Fact.FirstResponseAt` undefined.
  - GREEN: the same command after implementation → `ok`.
  - RED: `GOWORK=off go test ./internal/rewriter -run TestRewriteMarksFirstResponseOnce -count=1` → `marked=0`.
  - GREEN: `GOWORK=off go test ./internal/rewriter ./internal/stats -run 'TestRewriteMarksFirstResponseOnce|TestMarkFirstResponseKeepsFirstTimestamp' -count=1` → both packages `ok`.
- Review findings/fixes: none.
- Deferred items/limitations: TTFT is the first SSE response event, not the first content token; non-SSE streams are not marked.

---

### Task 2: Persist TTFT and read it in graph rows

**Files:**
- Modify: `internal/stats/store.go`
- Modify: `internal/stats/graph.go`
- Modify: `internal/stats/recorder.go`
- Test: `internal/stats/store_test.go`
- Test: `internal/stats/recorder_test.go`
- Test: `internal/stats/graph_test.go` (only if an existing graph fixture needs the new column)

**Interfaces:**
- Consumes: `Fact.FirstResponseAt` from Task 1; `ro.Time` and `ro.Duration` from the x recorder object.
- Produces: `requests.ttft_ms`; `Request.TTFTMS int64` with JSON `ttftMs,omitempty`; `GraphRow.TTFTMS int64`; `Recorder.Record` writes a clamped millisecond value only when `fact.Stream` is true.

- [ ] **Step 1: Write the failing store round-trip test**

Add to `internal/stats/store_test.go`:

```go
func TestTTFTRoundTrip(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	base := time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)

	in := Request{
		ID: "ttft-1", SID: "ttft-1", TS: base, Provider: "p", Model: "m",
		Status: 200, DurationMS: 2000, TTFTMS: 450, OutputTokens: 80, Stream: true,
	}
	if err := s.Insert(ctx, in); err != nil {
		t.Fatal(err)
	}

	got, err := s.Get(ctx, "ttft-1")
	if err != nil || got == nil {
		t.Fatalf("get = %+v err=%v", got, err)
	}
	if got.TTFTMS != 450 {
		t.Fatalf("ttft = %d, want 450", got.TTFTMS)
	}
}
```

Also extend `TestOpenMigratesMonotonicSeq` after its existing new-sequence assertion with a TTFT insert and read:

```go
if err := s.Insert(context.Background(), Request{
	ID: "new-ttft", TS: time.Now(), Provider: "p", TTFTMS: 123,
}); err != nil {
	t.Fatal(err)
}
got, err := s.Get(context.Background(), "new-ttft")
if err != nil || got == nil || got.TTFTMS != 123 {
	t.Fatalf("old database did not gain ttft_ms: %+v err=%v", got, err)
}
```

- [ ] **Step 2: Run the store tests and verify they fail**

Run: `GOWORK=off go test ./internal/stats -run 'TestTTFTRoundTrip|TestOpenMigratesMonotonicSeq' -count=1`

Expected: FAIL because `Request.TTFTMS` is undefined.

- [ ] **Step 3: Add the schema column and Request field**

In `Store.migrate`, add `"ttft_ms": "INTEGER NOT NULL DEFAULT 0"` to the `ensureColumns("requests", ...)` map. Add to `Request`:

```go
TTFTMS int64 `json:"ttftMs,omitempty"`
```

Include `ttft_ms` in the `INSERT` column list after `duration_ms`, add one `?` placeholder, and pass `r.TTFTMS`. Add `ttft_ms` after `duration_ms` in the `Query` and `Get` SELECT lists, and scan `&r.TTFTMS` after `&r.DurationMS` in `scanRequests`.

- [ ] **Step 4: Run the store test and verify it passes**

Run: `GOWORK=off go test ./internal/stats -run TestTTFTRoundTrip -count=1`

Expected: PASS.

- [ ] **Step 5: Write the failing recorder tests**

Add to `internal/stats/recorder_test.go`:

```go
func TestRecorderStoresStreamTTFT(t *testing.T) {
	s := newStore(t)
	facts := NewFacts()
	ctx := context.Background()
	sid := "ttft-rec"
	start := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)

	facts.Add(sid, "", "", engine.Result{Stream: true})
	facts.MarkFirstResponse(sid, start.Add(400*time.Millisecond))

	body, err := json.Marshal(map[string]any{
		"sid": sid, "node": "route/provider/node",
		"time": start, "duration": 2 * time.Second,
		"http": map[string]any{"statusCode": 200},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := NewRecorder(s, facts, nil, nil).Record(ctx, body); err != nil {
		t.Fatal(err)
	}

	got, err := s.Get(ctx, sid)
	if err != nil || got == nil {
		t.Fatalf("get = %+v err=%v", got, err)
	}
	if !got.Stream || got.TTFTMS != 400 {
		t.Fatalf("stream/ttft = %v/%d, want true/400", got.Stream, got.TTFTMS)
	}
}

func TestRecorderSkipsTTFTForNonStreaming(t *testing.T) {
	s := newStore(t)
	facts := NewFacts()
	ctx := context.Background()
	sid := "ttft-plain"
	start := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)

	facts.MarkFirstResponse(sid, start.Add(200*time.Millisecond))
	body, err := json.Marshal(map[string]any{
		"sid": sid, "node": "route/provider/node",
		"time": start, "duration": time.Second,
		"http": map[string]any{"statusCode": 200},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := NewRecorder(s, facts, nil, nil).Record(ctx, body); err != nil {
		t.Fatal(err)
	}

	got, err := s.Get(ctx, sid)
	if err != nil || got == nil {
		t.Fatalf("get = %+v err=%v", got, err)
	}
	if got.Stream || got.TTFTMS != 0 {
		t.Fatalf("stream/ttft = %v/%d, want false/0", got.Stream, got.TTFTMS)
	}
}
```

- [ ] **Step 6: Run the recorder tests and verify they fail**

Run: `GOWORK=off go test ./internal/stats -run 'TestRecorderStoresStreamTTFT|TestRecorderSkipsTTFTForNonStreaming' -count=1`

Expected: FAIL because the recorder does not write `TTFTMS`.

- [ ] **Step 7: Write TTFT in the recorder**

In `Recorder.Record`, after `req.Stream = fact.Stream`, add:

```go
if fact.Stream && !fact.FirstResponseAt.IsZero() {
	ttft := fact.FirstResponseAt.Sub(ro.Time)
	if ttft < 0 {
		ttft = 0
	}
	if ttft > ro.Duration {
		ttft = ro.Duration
	}
	req.TTFTMS = ttft.Milliseconds()
}
```

- [ ] **Step 8: Read TTFT in graph rows**

Add `TTFTMS int64` to `GraphRow`. Add `ttft_ms` after `duration_ms` in both `GraphAggregate` and `ForEachRequestAfter` SELECTs, and scan `&row.TTFTMS` after `&row.DurationMS`.

- [ ] **Step 9: Run the recorder and graph tests and verify they pass**

Run: `GOWORK=off go test ./internal/stats -run 'TestRecorder|TestGraph|TestTTFTRoundTrip|TestAggregator' -count=1`

Expected: PASS.

- [ ] **Step 10: Commit Task 2**

```bash
git add internal/stats/store.go internal/stats/graph.go internal/stats/recorder.go internal/stats/store_test.go internal/stats/recorder_test.go docs/superpowers/plans/2026-11-06-node-token-rate.md
git commit -m "feat(stats): persist streaming TTFT"
```

**Implementation record:**
- Date/status: 2026-11-06 / complete
- Repository + commit: `llm-gw` `8b149e71` (code); this docs commit follows
- Files changed: `internal/stats/store.go`, `internal/stats/graph.go`, `internal/stats/recorder.go`, `internal/stats/store_test.go`, `internal/stats/recorder_test.go`
- Tests/commands:
  - RED: `GOWORK=off go test ./internal/stats -run 'TestTTFTRoundTrip|TestOpenMigratesMonotonicSeq' -count=1` → build failed, `Request.TTFTMS` undefined.
  - GREEN: the same command after implementation → `ok`.
  - Recorder RED/GREEN: `GOWORK=off go test ./internal/stats -run 'TestRecorderStoresStreamTTFT|TestRecorderSkipsTTFTForNonStreaming' -count=1` → `ok` after the recorder change.
  - Package gate: `GOWORK=off go test ./internal/stats -count=1` → `ok`.
- Review findings/fixes: the old-schema migration test was extended to insert and read back `TTFTMS`; no other findings.
- Deferred items/limitations: existing rows keep `ttft_ms = 0`; non-streaming rows never get a TTFT.

---

### Task 3: Aggregate per-node streaming token rate

**Files:**
- Modify: `internal/stats/graph.go`
- Test: `internal/stats/graph_test.go`
- Test: `internal/stats/graph_live_test.go` (only if the parity fixture needs a streaming row)

**Interfaces:**
- Consumes: `GraphRow.TTFTMS` from Task 2.
- Produces: `NodeMetrics.OutputTokensPerSecond float64` with JSON `outputTokensPerSecond`; `comboStats.streamOutputTokens` and `comboStats.streamGenerationMS`; JSON omits the raw counters.

- [ ] **Step 1: Write the failing aggregation test**

Add to `internal/stats/graph_test.go`:

```go
func TestGraphComputesStreamingTokenRate(t *testing.T) {
	base := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	rows := []GraphRow{
		// 80 tokens in 800ms => 100 tok/s.
		{ID: "a", TS: base.UnixMilli(), Route: "r", Node: "n", Provider: "p", Status: 200,
			DurationMS: 1000, TTFTMS: 200, OutputTokens: 80},
		// 120 tokens in 1500ms => 80 tok/s.
		{ID: "b", TS: base.Add(time.Minute).UnixMilli(), Route: "r", Node: "n", Provider: "p", Status: 200,
			DurationMS: 2000, TTFTMS: 500, OutputTokens: 120},
		// Non-streaming: no TTFT, must not contribute.
		{ID: "c", TS: base.Add(2 * time.Minute).UnixMilli(), Route: "r", Node: "n", Provider: "p", Status: 200,
			DurationMS: 500, OutputTokens: 999},
		// No output tokens: must not contribute.
		{ID: "d", TS: base.Add(3 * time.Minute).UnixMilli(), Route: "r", Node: "n", Provider: "p", Status: 200,
			DurationMS: 500, TTFTMS: 100},
		// Zero generation time: must not contribute.
		{ID: "e", TS: base.Add(4 * time.Minute).UnixMilli(), Route: "r", Node: "n", Provider: "p", Status: 200,
			DurationMS: 300, TTFTMS: 300, OutputTokens: 10},
	}

	agg := NewAggregator()
	for _, row := range rows {
		agg.Add(row)
	}
	g := agg.Metrics(nil, nil, GraphMetrics{}, true)

	// (80 + 120) * 1000 / (800 + 1500) = 86.9565...
	got := g.Nodes["node:n"].OutputTokensPerSecond
	if got < 86.9 || got > 87.0 {
		t.Fatalf("token rate = %v, want about 86.96", got)
	}
}

func TestGraphOmitsTokenRateWithoutStreamingRows(t *testing.T) {
	agg := NewAggregator()
	agg.Add(GraphRow{
		ID: "plain", TS: time.Now().UnixMilli(), Route: "r", Node: "n", Provider: "p",
		Status: 200, DurationMS: 500, OutputTokens: 100,
	})
	g := agg.Metrics(nil, nil, GraphMetrics{}, true)
	if got := g.Nodes["node:n"].OutputTokensPerSecond; got != 0 {
		t.Fatalf("token rate = %v, want 0 for no streaming rows", got)
	}
}
```

- [ ] **Step 2: Run the aggregation tests and verify they fail**

Run: `GOWORK=off go test ./internal/stats -run 'TestGraphComputesStreamingTokenRate|TestGraphOmitsTokenRateWithoutStreamingRows' -count=1`

Expected: FAIL because `OutputTokensPerSecond` is undefined.

- [ ] **Step 3: Add the raw counters and metric field**

In `comboStats` add:

```go
streamOutputTokens int64
streamGenerationMS int64
```

In `NodeMetrics` add:

```go
OutputTokensPerSecond float64 `json:"outputTokensPerSecond"`

streamOutputTokens int64
streamGenerationMS int64
```

The unexported counters are not JSON-serialized; they exist only so multiple combos can be summed before computing one weighted rate.

- [ ] **Step 4: Accumulate eligible rows**

In `Aggregator.Add`, after the duration append:

```go
if row.TTFTMS > 0 && row.OutputTokens > 0 && row.DurationMS > row.TTFTMS {
	c.streamOutputTokens += row.OutputTokens
	c.streamGenerationMS += row.DurationMS - row.TTFTMS
}
```

In `addNode`, add the counters:

```go
n.streamOutputTokens += c.streamOutputTokens
n.streamGenerationMS += c.streamGenerationMS
```

- [ ] **Step 5: Compute the weighted rate once per node**

After the percentile loop at the end of `Metrics`, before copying error samples:

```go
for key, n := range g.Nodes {
	if n.streamGenerationMS > 0 {
		n.OutputTokensPerSecond = float64(n.streamOutputTokens) * 1000 / float64(n.streamGenerationMS)
	}
	g.Nodes[key] = n
}
```

Do not average per-request rates; the weighted sum is required so long streams do not count as much as short ones.

- [ ] **Step 6: Run the aggregation tests and verify they pass**

Run: `GOWORK=off go test ./internal/stats -run 'TestGraph|TestAggregator' -count=1`

Expected: PASS, including the existing HTTP/live parity test.

- [ ] **Step 7: Commit Task 3**

```bash
git add internal/stats/graph.go internal/stats/graph_test.go docs/superpowers/plans/2026-11-06-node-token-rate.md
git commit -m "feat(stats): aggregate per-node streaming token rate"
```

**Implementation record:**
- Date/status: 2026-11-06 / complete
- Repository + commit: `llm-gw` `4bbf44b2` (code); this docs commit follows
- Files changed: `internal/stats/graph.go`, `internal/stats/graph_test.go`
- Tests/commands:
  - RED: `GOWORK=off go test ./internal/stats -run 'TestGraphComputesStreamingTokenRate|TestGraphOmitsTokenRateWithoutStreamingRows' -count=1` → `OutputTokensPerSecond` undefined.
  - GREEN: `GOWORK=off go test ./internal/stats -run 'TestGraph|TestAggregator' -count=1` → `ok`.
  - Package gate: `GOWORK=off go test ./internal/stats -count=1` → `ok`.
- Review findings/fixes: weighted rate is computed from summed tokens and generation time, not averaged per request.
- Deferred items/limitations: only streaming rows with TTFT and output tokens contribute.

---

### Task 4: Display the rate in the web console

**Files:**
- Modify: `web/src/api/types.ts`
- Modify: `web/src/time.ts`
- Modify: `web/src/time.test.ts`
- Modify: `web/src/graph/nodes.tsx`
- Modify: `web/src/graph/nodes.test.tsx`
- Modify: `web/src/App.tsx`
- Modify: `web/src/locales/en-US.ts`
- Test: `web/src/App.test.tsx` (create)
- Modify: `README.md`

**Interfaces:**
- Consumes: `NodeMetrics.outputTokensPerSecond` from Task 3.
- Produces: `formatTokenRate(rate: number): string`; `NodeStats` gains `showRate?: boolean`; `NodePanel` is exported for a static-render test.

- [ ] **Step 1: Write the failing rate-format test**

Add to `web/src/time.test.ts`:

```ts
describe('formatTokenRate', () => {
  it('shows one decimal and strips a trailing .0', () => {
    expect(formatTokenRate(42.34)).toBe('42.3 tok/s');
    expect(formatTokenRate(42)).toBe('42 tok/s');
  });

  it('returns an empty string for non-positive rates', () => {
    expect(formatTokenRate(0)).toBe('');
    expect(formatTokenRate(-1)).toBe('');
  });
});
```

- [ ] **Step 2: Run the rate-format test and verify it fails**

Run: `npm test -- src/time.test.ts`

Expected: FAIL because `formatTokenRate` is undefined.

- [ ] **Step 3: Implement `formatTokenRate`**

In `web/src/time.ts`:

```ts
/** Format a token rate as a compact one-decimal value; empty when unavailable. */
export function formatTokenRate(rate: number): string {
  if (!Number.isFinite(rate) || rate <= 0) return '';
  return `${rate.toFixed(1).replace(/\.0$/, '')} tok/s`;
}
```

- [ ] **Step 4: Run the rate-format test and verify it passes**

Run: `npm test -- src/time.test.ts`

Expected: PASS.

- [ ] **Step 5: Update the API type and its test fixtures**

Add to `web/src/api/types.ts` in `NodeMetrics`:

```ts
outputTokensPerSecond: number;
```

Add `outputTokensPerSecond: 0` to the existing `NodeMetrics` fixtures in `web/src/graph/nodes.test.tsx` and `web/src/useLiveGraph.test.tsx`.

- [ ] **Step 6: Write the failing `NodeStats` tests**

Add to `web/src/graph/nodes.test.tsx`:

```tsx
it('shows the token rate only when asked and available', () => {
  const metrics = {
    requests: 10, errors: 0, p50Ms: 3, p95Ms: 5,
    inputTokens: 0, outputTokens: 80, cachedTokens: 0,
    outputTokensPerSecond: 42.34,
  };
  const withRate = renderToStaticMarkup(<NodeStats metrics={metrics} showRate />);
  expect(withRate).toContain('42.3 tok/s');

  const withoutRate = renderToStaticMarkup(<NodeStats metrics={metrics} />);
  expect(withoutRate).not.toContain('tok/s');
});

it('omits a zero token rate', () => {
  const html = renderToStaticMarkup(
    <NodeStats
      metrics={{
        requests: 10, errors: 0, p50Ms: 3, p95Ms: 5,
        inputTokens: 0, outputTokens: 0, cachedTokens: 0,
        outputTokensPerSecond: 0,
      }}
      showRate
    />,
  );
  expect(html).not.toContain('tok/s');
});
```

- [ ] **Step 7: Run the `NodeStats` tests and verify they fail**

Run: `npm test -- src/graph/nodes.test.tsx`

Expected: FAIL because `showRate`/`outputTokensPerSecond` are not rendered.

- [ ] **Step 8: Render the rate in `NodeStats`**

In `web/src/graph/nodes.tsx`:

- Import `formatTokenRate` from `../time`.
- Change `metricsText(m: NodeMetrics)` to `metricsText(m: NodeMetrics, showRate: boolean)` and append
  ` · ${formatTokenRate(m.outputTokensPerSecond)}` when `showRate` and the rate is positive.
- Change the signature to
  `export function NodeStats({ metrics, showRate = false }: { metrics?: NodeMetrics; showRate?: boolean })`.
- After the p95 fragment, append the rate segment only when `showRate && metrics.outputTokensPerSecond > 0`.
- In `BindingNode`, render `<NodeStats metrics={data.metrics} showRate />`; leave the other node components unchanged.

- [ ] **Step 9: Run the `NodeStats` tests and verify they pass**

Run: `npm test -- src/graph/nodes.test.tsx`

Expected: PASS.

- [ ] **Step 10: Write the failing node-panel test**

Create `web/src/App.test.tsx`:

```tsx
import { renderToStaticMarkup } from 'react-dom/server';
import { beforeEach, describe, expect, it } from 'vitest';
import { NodePanel } from './App';
import { setActiveLang } from './i18n';

beforeEach(() => setActiveLang('zh-CN'));

const metrics = {
  requests: 10, errors: 0, p50Ms: 3, p95Ms: 5,
  inputTokens: 0, outputTokens: 80, cachedTokens: 0,
  outputTokensPerSecond: 42.34,
};

describe('NodePanel', () => {
  it('shows output speed for a binding node', () => {
    const html = renderToStaticMarkup(
      <NodePanel id="node:fast" metrics={metrics} errors={[]} />,
    );
    expect(html).toContain('输出速度');
    expect(html).toContain('42.3 tok/s');
  });

  it('omits output speed for non-binding nodes', () => {
    const html = renderToStaticMarkup(
      <NodePanel id="provider:p" metrics={metrics} errors={[]} />,
    );
    expect(html).not.toContain('输出速度');
  });
});
```

- [ ] **Step 11: Run the node-panel test and verify it fails**

Run: `npm test -- src/App.test.tsx`

Expected: FAIL because `NodePanel` is not exported and has no output-speed row.

- [ ] **Step 12: Add the detail-panel row**

In `web/src/App.tsx`:

- Export `NodePanel` (`export function NodePanel(...)`).
- Import `formatTokenRate` alongside `formatDurationMs`.
- After the P50/P95 row, add a conditional row only for binding nodes with a positive rate:

```tsx
{id.startsWith('node:') && (metrics?.outputTokensPerSecond ?? 0) > 0 ? (
  <Descriptions.Item label={translate('输出速度')}>
    {formatTokenRate(metrics!.outputTokensPerSecond)}
  </Descriptions.Item>
) : null}
```

- Add `'输出速度': 'Output speed'` to `web/src/locales/en-US.ts`.
- In `README.md`, extend the Overview row to mention per-node output speed.

- [ ] **Step 13: Run the node-panel test and verify it passes**

Run: `npm test -- src/App.test.tsx`

Expected: PASS.

- [ ] **Step 14: Run the web gate**

Run: `npm test && npx tsc --noEmit && npm run build`

Expected: all tests pass, TypeScript is clean, and the Vite bundle is rebuilt into `internal/admin/dist`.

- [ ] **Step 15: Commit Task 4**

```bash
git add web/src/api/types.ts web/src/time.ts web/src/time.test.ts \
  web/src/graph/nodes.tsx web/src/graph/nodes.test.tsx \
  web/src/useLiveGraph.test.tsx web/src/App.tsx web/src/App.test.tsx \
  web/src/locales/en-US.ts README.md docs/superpowers/plans/2026-11-06-node-token-rate.md
git commit -m "feat(web): show per-node output token rate"
```

**Implementation record:**
- Date/status:
- Repository + commit:
- Files changed:
- Tests/commands:
- Review findings/fixes:
- Deferred items/limitations:

---

### Task 5: Gateway TTFT integration and full gate

**Files:**
- Test: `internal/gateway/stats_test.go`
- Modify: `docs/superpowers/plans/2026-11-06-node-token-rate.md` (records only)

**Interfaces:**
- Consumes: the full chain from Tasks 1-4.
- Produces: an end-to-end assertion that a real streamed gateway request stores TTFT, plus final verification.

- [ ] **Step 1: Write the failing gateway test**

Add to `internal/gateway/stats_test.go`:

```go
func TestStatsRecordsStreamTTFT(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		f := w.(http.Flusher)
		time.Sleep(30 * time.Millisecond)
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n")
		f.Flush()
		time.Sleep(30 * time.Millisecond)
		io.WriteString(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":7,\"completion_tokens\":3}}\n\n")
		f.Flush()
	}))
	defer upstream.Close()

	gw, err := gateway.New(statsConfig(t, upstream.URL), xlogger.Nop())
	if err != nil {
		t.Fatal(err)
	}
	if err := gw.Start(); err != nil {
		t.Fatal(err)
	}
	defer gw.Close()

	resp, err := http.Post("http://"+gw.Addr().String()+"/v1/chat/completions", "application/json",
		strings.NewReader(`{"model":"gpt-4.5","stream":true,"messages":[{"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
	}
	resp.Body.Close()

	row := waitRow(t, gw)
	if !row.Stream {
		t.Fatalf("stream = false, want true: %+v", row)
	}
	if row.TTFTMS <= 0 || row.TTFTMS > row.DurationMS {
		t.Fatalf("ttft = %d, duration = %d", row.TTFTMS, row.DurationMS)
	}
}
```

- [ ] **Step 2: Run the gateway test and verify it passes**

Run: `GOWORK=off go test ./internal/gateway -run TestStatsRecordsStreamTTFT -count=1`

Expected: PASS. If it fails, fix the capture path rather than loosening the assertion; the test must see a positive TTFT no larger than the total duration.

- [ ] **Step 3: Run the full Go gate**

Run:

```bash
GOWORK=off go build ./...
GOWORK=off go vet ./...
GOWORK=off go test ./... -count=1
```

Expected: all packages pass.

- [ ] **Step 4: Run the full web gate**

Run:

```bash
cd web
npm test
npx tsc --noEmit
npm run build
```

Expected: all tests pass, TypeScript is clean, and the embedded bundle is rebuilt.

- [ ] **Step 5: Rebuild the Go binary against the embedded bundle**

Run: `GOWORK=off go build ./...`

Expected: PASS.

- [ ] **Step 6: Commit Task 5**

```bash
git add internal/gateway/stats_test.go docs/superpowers/plans/2026-11-06-node-token-rate.md
git commit -m "test(gateway): verify streaming TTFT capture"
```

**Implementation record:**
- Date/status:
- Repository + commit:
- Files changed:
- Tests/commands:
- Review findings/fixes:
- Deferred items/limitations:

---


