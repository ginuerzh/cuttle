package stats

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func TestAggregatorMatchesGraphWithFilter(t *testing.T) {
	s := newStore(t)
	base := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	from, to := base.Add(-time.Hour), base.Add(time.Hour)

	rows := []Request{
		{ID: "1", TS: base, Route: "strong", Node: "n1", Provider: "p", ClientModel: "gpt-4.5", Status: 200, DurationMS: 10, InputTokens: 1, OutputTokens: 2},
		{ID: "2", TS: base.Add(time.Minute), Route: "strong", Node: "n2", Provider: "p", ClientModel: "gpt-4.5", Status: 500, Error: "boom", DurationMS: 30},
		{ID: "3", TS: base.Add(2 * time.Minute), Provider: "q", ClientModel: "other", Status: 200, DurationMS: 20},
	}
	for _, r := range rows {
		if err := s.Insert(context.Background(), r); err != nil {
			t.Fatal(err)
		}
	}

	resolve := func(clientModel string) string {
		if clientModel == "gpt-4.5" {
			return "fast"
		}
		return ""
	}
	include := func(route, node, provider string) bool { return provider != "q" }

	want, err := s.GraphWithFilter(context.Background(), from, to, resolve, include)
	if err != nil {
		t.Fatal(err)
	}
	agg, _, err := s.GraphAggregate(context.Background(), from, to)
	if err != nil {
		t.Fatal(err)
	}
	got := agg.Metrics(resolve, include, GraphMetrics{}, true)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("aggregator = %#v, want %#v", got, want)
	}
}

func TestGraphAggregateTailIncludesLateRow(t *testing.T) {
	s := newStore(t)
	base := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	from, to := base.Add(-time.Hour), base.Add(time.Hour)

	if err := s.Insert(context.Background(), Request{
		ID: "first", TS: base, Route: "r", Node: "n", Provider: "p", Status: 200, DurationMS: 10,
	}); err != nil {
		t.Fatal(err)
	}
	agg, high, err := s.GraphAggregate(context.Background(), from, to)
	if err != nil {
		t.Fatal(err)
	}

	// This request started before the first snapshot but completes after it,
	// so it has a higher rowid and an older ts.
	if err := s.Insert(context.Background(), Request{
		ID: "late", TS: base.Add(-30 * time.Minute), Route: "r", Node: "n", Provider: "p", Status: 200, DurationMS: 20,
	}); err != nil {
		t.Fatal(err)
	}

	seen := 0
	last, err := s.ForEachRequestAfter(context.Background(), high, func(row GraphRow) {
		seen++
		if row.TS >= from.UnixMilli() && row.TS < to.UnixMilli() {
			agg.Add(row)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if last <= high {
		t.Fatalf("cursor = %d, want greater than %d", last, high)
	}
	if seen != 1 {
		t.Fatalf("tail rows = %d, want 1", seen)
	}
	got := agg.Metrics(nil, nil, GraphMetrics{}, true)
	if got.Nodes["provider:p"].Requests != 2 {
		t.Fatalf("provider:p = %+v, want 2 requests", got.Nodes["provider:p"])
	}
}

func TestGraphAggregateCursorAdvancesPastOutOfWindowRows(t *testing.T) {
	s := newStore(t)
	base := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	from, to := base, base.Add(time.Hour)

	// The in-window row is inserted first, the out-of-window row second.
	if err := s.Insert(context.Background(), Request{
		ID: "in", TS: base.Add(time.Minute), Provider: "p", Status: 200,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Insert(context.Background(), Request{
		ID: "old", TS: base.Add(-time.Hour), Provider: "p", Status: 200,
	}); err != nil {
		t.Fatal(err)
	}

	var rowidIn, rowidOld int64
	if err := s.db.QueryRow(`SELECT rowid FROM requests WHERE request_id = 'in'`).Scan(&rowidIn); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT rowid FROM requests WHERE request_id = 'old'`).Scan(&rowidOld); err != nil {
		t.Fatal(err)
	}

	agg := NewAggregator()
	seen := 0
	last, err := s.ForEachRequestAfter(context.Background(), 0, func(row GraphRow) {
		seen++
		if row.TS >= from.UnixMilli() && row.TS < to.UnixMilli() {
			agg.Add(row)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if seen != 2 {
		t.Fatalf("scanned rows = %d, want 2", seen)
	}
	if last != rowidOld {
		t.Fatalf("cursor = %d, want %d (out-of-window row)", last, rowidOld)
	}
	if last <= rowidIn {
		t.Fatalf("cursor = %d, want past in-window rowid %d", last, rowidIn)
	}
	got := agg.Metrics(nil, nil, GraphMetrics{}, true)
	if got.Nodes["provider:p"].Requests != 1 {
		t.Fatalf("provider:p = %+v, want 1 request", got.Nodes["provider:p"])
	}

	calls := 0
	last2, err := s.ForEachRequestAfter(context.Background(), last, func(GraphRow) { calls++ })
	if err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatalf("second tail callbacks = %d, want 0", calls)
	}
	if last2 != last {
		t.Fatalf("second cursor = %d, want %d", last2, last)
	}
}

func TestAggregatorDeduplicatesRequestID(t *testing.T) {
	agg := NewAggregator()
	row := GraphRow{
		ID: "same", TS: time.Now().UnixMilli(), Route: "r", Node: "n", Provider: "p",
		Status: 200, DurationMS: 10,
	}
	agg.Add(row)
	agg.Add(row)

	got := agg.Metrics(nil, nil, GraphMetrics{}, true)
	if got.Nodes["provider:p"].Requests != 1 {
		t.Fatalf("provider:p = %+v, want 1 request", got.Nodes["provider:p"])
	}
}

func TestAggregatorAppliesResolverAndFilterAtReadTime(t *testing.T) {
	agg := NewAggregator()
	agg.Add(GraphRow{
		ID: "1", TS: time.Now().UnixMilli(), Route: "r", Node: "n", Provider: "p",
		ClientModel: "gpt-4.5", Status: 200, DurationMS: 10,
	})
	resolve := func(clientModel string) string {
		if clientModel == "gpt-4.5" {
			return "fast"
		}
		return ""
	}

	with := agg.Metrics(resolve, nil, GraphMetrics{}, true)
	if got := with.Nodes["entrypoint:fast"]; got.Requests != 1 {
		t.Fatalf("entrypoint:fast = %+v, want 1 request", got)
	}
	if got := with.Edges["entrypoint:fast->route:r"]; got.Requests != 1 {
		t.Fatalf("entrypoint edge = %+v, want 1 request", got)
	}

	without := agg.Metrics(nil, nil, GraphMetrics{}, true)
	if _, ok := without.Nodes["entrypoint:fast"]; ok {
		t.Fatalf("unexpected entrypoint node: %+v", without.Nodes)
	}

	filtered := agg.Metrics(nil, func(string, string, string) bool { return false }, GraphMetrics{}, true)
	if len(filtered.Nodes) != 0 || len(filtered.Edges) != 0 {
		t.Fatalf("filtered graph = %+v, want empty", filtered)
	}
}

func TestAggregatorPercentileCache(t *testing.T) {
	agg := NewAggregator()
	base := time.Now().UnixMilli()
	agg.Add(GraphRow{ID: "1", TS: base, Provider: "p", Status: 200, DurationMS: 10})
	agg.Add(GraphRow{ID: "2", TS: base + 1, Provider: "p", Status: 200, DurationMS: 20})
	first := agg.Metrics(nil, nil, GraphMetrics{}, true)
	firstP50 := first.Nodes["provider:p"].P50MS
	firstP95 := first.Nodes["provider:p"].P95MS

	agg.Add(GraphRow{ID: "3", TS: base + 2, Provider: "p", Status: 200, DurationMS: 30})
	cached := agg.Metrics(nil, nil, first, false)
	if cached.Nodes["provider:p"].Requests != 3 {
		t.Fatalf("cached requests = %d, want 3", cached.Nodes["provider:p"].Requests)
	}
	if cached.Nodes["provider:p"].P50MS != firstP50 || cached.Nodes["provider:p"].P95MS != firstP95 {
		t.Fatalf("cached percentiles = %+v, want p50=%d p95=%d", cached.Nodes["provider:p"], firstP50, firstP95)
	}

	recomputed := agg.Metrics(nil, nil, first, true)
	if got := recomputed.Nodes["provider:p"].P50MS; got != 20 {
		t.Fatalf("recomputed p50 = %d, want 20", got)
	}
	if got := recomputed.Nodes["provider:p"].P95MS; got != 30 {
		t.Fatalf("recomputed p95 = %d, want 30", got)
	}
}

func TestGraphWindowBoundaries(t *testing.T) {
	s := newStore(t)
	base := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	from, to := base, base.Add(time.Minute)

	for i, ts := range []time.Time{from, from.Add(time.Millisecond), to.Add(-time.Millisecond), to} {
		if err := s.Insert(context.Background(), Request{
			ID: "b" + string(rune('0'+i)), TS: ts, Provider: "p", Status: 200,
		}); err != nil {
			t.Fatal(err)
		}
	}

	agg, _, err := s.GraphAggregate(context.Background(), from, to)
	if err != nil {
		t.Fatal(err)
	}
	got := agg.Metrics(nil, nil, GraphMetrics{}, true)
	if got.Nodes["provider:p"].Requests != 3 {
		t.Fatalf("provider:p = %+v, want 3 requests", got.Nodes["provider:p"])
	}
}
