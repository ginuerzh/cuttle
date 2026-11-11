package stats

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestGraphAggregates(t *testing.T) {
	s := newStore(t)
	base := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)

	rows := []Request{
		{ID: "1", TS: base, Route: "strong", Node: "n1", Provider: "p", Status: 200, DurationMS: 10, InputTokens: 1, OutputTokens: 2},
		{ID: "2", TS: base.Add(time.Minute), Route: "strong", Node: "n2", Provider: "p", Status: 500, Error: "boom", DurationMS: 30},
		{ID: "3", TS: base.Add(2 * time.Minute), Provider: "q", Status: 200, DurationMS: 20},
	}
	for _, r := range rows {
		if err := s.Insert(context.Background(), r); err != nil {
			t.Fatal(err)
		}
	}

	g, err := s.Graph(context.Background(), base.Add(-time.Hour), base.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}

	p := g.Nodes["provider:p"]
	if p.Requests != 2 || p.Errors != 1 || p.P50MS != 30 || p.P95MS != 30 {
		t.Fatalf("provider:p = %+v", p)
	}
	if got := g.Nodes["route:strong"]; got.Requests != 2 || got.Errors != 1 {
		t.Fatalf("route:strong = %+v", got)
	}
	if got := g.Nodes["unrouted"]; got.Requests != 1 {
		t.Fatalf("unrouted = %+v", got)
	}
	if got := g.Nodes["node:n1"]; got.Requests != 1 {
		t.Fatalf("node:n1 = %+v", got)
	}
	if got := g.Nodes["node:n2"]; got.Requests != 1 || got.Errors != 1 {
		t.Fatalf("node:n2 = %+v", got)
	}
	if got := g.Edges["route:strong|node:n1"]; got.Requests != 1 {
		t.Fatalf("route->n1 edge = %+v", got)
	}
	if got := g.Edges["route:strong|node:n2"]; got.Requests != 1 || got.Errors != 1 {
		t.Fatalf("route->n2 edge = %+v", got)
	}
	if got := g.Edges["node:n1|provider:p"]; got.Requests != 1 {
		t.Fatalf("n1->p edge = %+v", got)
	}
	if got := g.Edges["node:n2|provider:p"]; got.Requests != 1 || got.Errors != 1 {
		t.Fatalf("n2->p edge = %+v", got)
	}
	if got := g.Edges["unrouted|provider:q"]; got.Requests != 1 {
		t.Fatalf("unrouted edge = %+v", got)
	}
	if len(g.Errors) != 1 || g.Errors[0].RequestID != "2" || g.Errors[0].Status != 500 {
		t.Fatalf("errors = %+v", g.Errors)
	}
}

func TestParseNodeName(t *testing.T) {
	cases := []struct {
		in                    string
		route, provider, node string
	}{
		{"r/p/n1", "r", "p", "n1"},
		{"r/p/n1/2", "r", "p", "n1"},
		{"r/p", "r", "p", ""},
		{"p", "", "p", ""},
		{"", "", "", ""},
	}
	for _, tc := range cases {
		route, provider, node := parseNodeName(tc.in)
		if route != tc.route || provider != tc.provider || node != tc.node {
			t.Fatalf("parseNodeName(%q) = %q/%q/%q, want %q/%q/%q",
				tc.in, route, provider, node, tc.route, tc.provider, tc.node)
		}
	}
}

func TestGraphEntrypointMetrics(t *testing.T) {
	s := newStore(t)
	base := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)

	rows := []Request{
		{ID: "a1", TS: base, ClientModel: "claude-sonnet-4-5", Route: "fast", Node: "n", Provider: "p", Status: 200, DurationMS: 10},
		{ID: "a2", TS: base.Add(time.Minute), ClientModel: "claude-sonnet-4-5", Route: "fast", Node: "n", Provider: "p", Status: 500, Error: "boom", DurationMS: 20},
		{ID: "a3", TS: base.Add(2 * time.Minute), ClientModel: "gpt-4.5", Route: "fast", Node: "n", Provider: "p", Status: 200, DurationMS: 30},
		{ID: "a4", TS: base.Add(3 * time.Minute), ClientModel: "direct-model", Route: "fast", Node: "n", Provider: "p", Status: 200, DurationMS: 40},
	}
	for _, r := range rows {
		if err := s.Insert(context.Background(), r); err != nil {
			t.Fatal(err)
		}
	}

	resolve := func(clientModel string) string {
		switch clientModel {
		case "claude-sonnet-4-5":
			return "claude"
		case "gpt-4.5":
			return "fast"
		default:
			return ""
		}
	}
	g, err := s.GraphWithEntrypoints(context.Background(), base.Add(-time.Hour), base.Add(time.Hour), resolve)
	if err != nil {
		t.Fatal(err)
	}

	if got := g.Nodes["entrypoint:claude"]; got.Requests != 2 || got.Errors != 1 || got.P50MS != 20 {
		t.Fatalf("claude entrypoint = %+v", got)
	}
	if got := g.Nodes["entrypoint:fast"]; got.Requests != 1 || got.Errors != 0 {
		t.Fatalf("fast entrypoint = %+v", got)
	}
	if _, ok := g.Nodes["entrypoint:direct-model"]; ok {
		t.Fatalf("unexpected entrypoint metrics for an unmatched client model: %+v", g.Nodes)
	}
	if got := g.Edges["entrypoint:claude->route:fast"]; got.Requests != 2 || got.Errors != 1 {
		t.Fatalf("claude edge = %+v", got)
	}
	if got := g.Edges["entrypoint:fast->route:fast"]; got.Requests != 1 {
		t.Fatalf("fast edge = %+v", got)
	}
	if len(g.Errors) != 1 || g.Errors[0].Entrypoint != "claude" {
		t.Fatalf("error rows = %+v, want the claude entrypoint", g.Errors)
	}
}

// TestGraphEmptyClientModelUsesCatchallEntrypoint covers requests that reach
// the catch-all entrypoint with no model (for example an empty JSON body).
// The empty client model must still be passed to the resolver so a "*" glob
// can attribute the row to catchall.
func TestGraphEmptyClientModelUsesCatchallEntrypoint(t *testing.T) {
	s := newStore(t)
	base := time.Date(2026, 11, 7, 0, 53, 0, 0, time.UTC)

	rows := []Request{
		{ID: "empty-error", TS: base, ClientModel: "", Route: "free", Node: "oc-go-free", Provider: "opencode-go", Status: 400, Error: "`model` must be a non-empty string"},
		{ID: "empty-ok", TS: base.Add(time.Minute), ClientModel: "", Route: "free", Node: "oc-go-free", Provider: "opencode-go", Status: 200},
	}
	for _, r := range rows {
		if err := s.Insert(context.Background(), r); err != nil {
			t.Fatal(err)
		}
	}

	resolve := func(clientModel string) string {
		if clientModel == "" {
			return "catchall"
		}
		return ""
	}
	g, err := s.GraphWithEntrypoints(context.Background(), base.Add(-time.Hour), base.Add(time.Hour), resolve)
	if err != nil {
		t.Fatal(err)
	}
	if got := g.Nodes["entrypoint:catchall"]; got.Requests != 2 || got.Errors != 1 {
		t.Fatalf("catchall entrypoint = %+v", got)
	}
	if got := g.Edges["entrypoint:catchall->route:free"]; got.Requests != 2 || got.Errors != 1 {
		t.Fatalf("catchall edge = %+v", got)
	}
	if len(g.Errors) != 1 || g.Errors[0].Entrypoint != "catchall" {
		t.Fatalf("error rows = %+v, want catchall", g.Errors)
	}
}

// TestGraphEntrypointsUseRowRoute: attribution uses the route recorded on the
// request, not the entrypoint's current configured route.
func TestGraphEntrypointsUseRowRoute(t *testing.T) {
	s := newStore(t)
	base := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)

	for _, r := range []Request{
		{ID: "r1", TS: base, ClientModel: "gpt-4.5", Route: "old", Node: "n", Provider: "p", Status: 200},
		{ID: "r2", TS: base.Add(time.Minute), ClientModel: "gpt-4.5", Route: "new", Node: "n", Provider: "p", Status: 200},
	} {
		if err := s.Insert(context.Background(), r); err != nil {
			t.Fatal(err)
		}
	}

	g, err := s.GraphWithEntrypoints(context.Background(), base.Add(-time.Hour), base.Add(time.Hour),
		func(string) string { return "fast" })
	if err != nil {
		t.Fatal(err)
	}
	if got := g.Edges["entrypoint:fast->route:old"]; got.Requests != 1 {
		t.Fatalf("old route edge = %+v", got)
	}
	if got := g.Edges["entrypoint:fast->route:new"]; got.Requests != 1 {
		t.Fatalf("new route edge = %+v", got)
	}
	if got := g.Edges["entrypoint:fast->route:configured"]; got.Requests != 0 {
		t.Fatalf("unexpected configured-route edge = %+v", got)
	}
	if _, ok := g.Nodes["alias:gpt-4.5"]; ok {
		t.Fatalf("alias node still present: %+v", g.Nodes)
	}
}

func TestGraphTopologyFilter(t *testing.T) {
	s := newStore(t)
	base := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)

	rows := []Request{
		{ID: "current", TS: base, Route: "fast", Node: "n", Provider: "p", Status: 200, DurationMS: 10},
		{ID: "stale", TS: base.Add(time.Minute), Route: "fast", Node: "old", Provider: "q", Status: 200, DurationMS: 20},
		{ID: "unrouted", TS: base.Add(2 * time.Minute), Provider: "p", Status: 200, DurationMS: 30},
	}
	for _, r := range rows {
		if err := s.Insert(context.Background(), r); err != nil {
			t.Fatal(err)
		}
	}

	include := func(route, node, provider string) bool {
		return route == "" || node == "n"
	}
	g, err := s.GraphWithFilter(context.Background(), base.Add(-time.Hour), base.Add(time.Hour), nil, include)
	if err != nil {
		t.Fatal(err)
	}
	if got := g.Nodes["node:n"]; got.Requests != 1 {
		t.Fatalf("node:n = %+v", got)
	}
	if _, ok := g.Nodes["node:old"]; ok {
		t.Fatalf("stale node metrics = %+v", g.Nodes["node:old"])
	}
	if _, ok := g.Nodes["provider:q"]; ok {
		t.Fatalf("stale provider metrics = %+v", g.Nodes["provider:q"])
	}
	if got := g.Nodes["provider:p"]; got.Requests != 2 {
		t.Fatalf("provider:p = %+v, want current + unrouted", got)
	}
	if got := g.Nodes["route:fast"]; got.Requests != 1 {
		t.Fatalf("route:fast = %+v", got)
	}
	if got := g.Nodes["unrouted"]; got.Requests != 1 {
		t.Fatalf("unrouted = %+v", got)
	}
	if got := g.Edges["node:old|provider:q"]; got.Requests != 0 {
		t.Fatalf("stale edge = %+v", got)
	}
}

func TestGraphCountsAttemptsAndFailures(t *testing.T) {
	s := newStore(t)
	ts := time.Now().Add(-time.Minute)
	// parent request that failed once and was retried into a 502
	if err := s.Insert(context.Background(), Request{
		ID: "r1", SID: "r1", TS: ts, Route: "r", Node: "n2", Provider: "p", Status: 502,
		Attempts: []Attempt{
			{RequestID: "r1", Attempt: 0, TS: ts, Route: "r", Node: "n1", Provider: "p", Status: 429},
			{RequestID: "r1", Attempt: 1, TS: ts, Route: "r", Node: "n2", Provider: "p", Status: 503},
		},
	}); err != nil {
		t.Fatal(err)
	}
	g, err := s.Graph(context.Background(), ts.Add(-time.Hour), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	n1 := g.Nodes["node:n1"]
	if n1.Attempts != 1 || n1.RetriedRequests != 1 || n1.AttemptStatus["429"] != 1 {
		t.Fatalf("node:n1 = %+v", n1)
	}
	n2 := g.Nodes["node:n2"]
	if n2.Attempts != 1 || n2.FailureStatus["502"] != 1 || n2.AttemptStatus["503"] != 1 {
		t.Fatalf("node:n2 = %+v", n2)
	}
	edge := g.Edges["route:r|node:n1"]
	if edge.Attempts != 1 || edge.RetriedRequests != 1 {
		t.Fatalf("edge = %+v", edge)
	}
	if got := g.Nodes["provider:p"]; got.Attempts != 2 || got.RetriedRequests != 1 {
		t.Fatalf("provider:p = %+v", got)
	}
}

func TestAggregatorDedupesAttempts(t *testing.T) {
	a := NewAggregator()
	row := GraphAttemptRow{RequestID: "r1", TS: 1, Route: "r", Node: "n", Provider: "p", Status: 429}
	a.AddAttempt(row)
	a.AddAttempt(row)
	g := a.Metrics(nil, nil, GraphMetrics{}, true)
	if n := g.Nodes["node:n"].Attempts; n != 1 {
		t.Fatalf("attempts = %d, want 1", n)
	}
	if n := g.Nodes["node:n"].RetriedRequests; n != 1 {
		t.Fatalf("retried requests = %d, want 1", n)
	}
}

func TestGraphDropsAttemptsOutsideTopology(t *testing.T) {
	s := newStore(t)
	ts := time.Now().Add(-time.Minute)
	if err := s.Insert(context.Background(), Request{
		ID: "r1", SID: "r1", TS: ts, Route: "r", Node: "n1", Provider: "p", Status: 200,
		Attempts: []Attempt{{RequestID: "r1", Attempt: 0, TS: ts, Route: "r", Node: "n1", Provider: "p", Status: 429}},
	}); err != nil {
		t.Fatal(err)
	}
	hide := func(route, node, provider string) bool { return node != "n1" }
	g, err := s.GraphWithFilter(context.Background(), ts.Add(-time.Hour), time.Now(), nil, hide)
	if err != nil {
		t.Fatal(err)
	}
	if got := g.Nodes["node:n1"].Attempts; got != 0 {
		t.Fatalf("node:n1 attempts = %d, want 0", got)
	}
	if _, ok := g.Edges["route:r|node:n1"]; ok {
		t.Fatal("edge for a filtered-out node must not exist")
	}
}

// TestGraphCountsFinalFailuresByStatus pins failureStatus to the final parent
// row: a failed dial carries status 0 and is keyed as "0", and a request that
// reached the client is never counted.
func TestGraphCountsFinalFailuresByStatus(t *testing.T) {
	s := newStore(t)
	ts := time.Now().Add(-time.Minute)
	for _, r := range []Request{
		{ID: "dial", SID: "dial", TS: ts, Route: "r", Node: "n1", Provider: "p", Error: "dial tcp: connection refused"},
		{ID: "rate", SID: "rate", TS: ts.Add(time.Second), Route: "r", Node: "n1", Provider: "p", Status: 429, Error: "rate limited"},
		{ID: "ok", SID: "ok", TS: ts.Add(2 * time.Second), Route: "r", Node: "n1", Provider: "p", Status: 200},
	} {
		if err := s.Insert(context.Background(), r); err != nil {
			t.Fatal(err)
		}
	}
	g, err := s.Graph(context.Background(), ts.Add(-time.Hour), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	n := g.Nodes["node:n1"]
	if len(n.FailureStatus) != 2 || n.FailureStatus["0"] != 1 || n.FailureStatus["429"] != 1 {
		t.Fatalf("node:n1 failure status = %+v", n.FailureStatus)
	}
	if got := g.Nodes["provider:p"].FailureStatus["429"]; got != 1 {
		t.Fatalf("provider:p failure status = %+v", g.Nodes["provider:p"].FailureStatus)
	}
	if got := g.Nodes["route:r"].FailureStatus["0"]; got != 1 {
		t.Fatalf("route:r failure status = %+v", g.Nodes["route:r"].FailureStatus)
	}

	// Attempt counters are for discarded attempts only: no attempt rows here.
	if got := g.Nodes["node:n1"].Attempts; got != 0 {
		t.Fatalf("node:n1 attempts = %d, want 0", got)
	}
	if got := g.Edges["route:r|node:n1"].Attempts; got != 0 {
		t.Fatalf("route->n1 edge attempts = %d, want 0", got)
	}
}

// TestGraphCountsUnroutedAttempts covers a retried request that never had a
// route: attempts follow the same `unrouted` key and edge as request rows.
func TestGraphCountsUnroutedAttempts(t *testing.T) {
	s := newStore(t)
	ts := time.Now().Add(-time.Minute)
	if err := s.Insert(context.Background(), Request{
		ID: "r1", SID: "r1", TS: ts, Provider: "p", Status: 502,
		Attempts: []Attempt{{RequestID: "r1", Attempt: 0, TS: ts, Provider: "p", Status: 429}},
	}); err != nil {
		t.Fatal(err)
	}
	g, err := s.Graph(context.Background(), ts.Add(-time.Hour), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if got := g.Nodes["unrouted"]; got.Attempts != 1 || got.RetriedRequests != 1 || got.AttemptStatus["429"] != 1 {
		t.Fatalf("unrouted = %+v", got)
	}
	if got := g.Nodes["provider:p"]; got.Attempts != 1 || got.FailureStatus["502"] != 1 {
		t.Fatalf("provider:p = %+v", got)
	}
	if got := g.Edges["unrouted|provider:p"]; got.Attempts != 1 || got.RetriedRequests != 1 {
		t.Fatalf("unrouted edge = %+v", got)
	}
}

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

// TestGraphReportsNewestFailureAndAttempt pins the data behind the dead badge
// tooltip's "最后 …" section: the newest failed parent row and the newest
// discarded attempt. A graph node merges several combos, so the newest of those
// keys has to win rather than whichever the loop visited last.
func TestGraphReportsNewestFailureAndAttempt(t *testing.T) {
	s := newStore(t)
	old := time.Now().Add(-time.Hour).Truncate(time.Second)
	newer := old.Add(2 * time.Minute)
	for _, r := range []Request{
		{
			ID: "old", SID: "old", TS: old, Route: "r", Node: "n1", Provider: "p",
			Status: 429, Error: "rate limited",
			Attempts: []Attempt{{RequestID: "old", Attempt: 0, Route: "r", Node: "n1", Provider: "p", Status: 429}},
		},
		{
			ID: "new", SID: "new", TS: newer, Route: "r", Node: "n2", Provider: "p",
			Status: 502, Error: "overloaded",
			Attempts: []Attempt{{RequestID: "new", Attempt: 0, Route: "r", Node: "n2", Provider: "p", Status: 503}},
		},
	} {
		if err := s.Insert(context.Background(), r); err != nil {
			t.Fatal(err)
		}
	}

	g, err := s.Graph(context.Background(), old.Add(-time.Hour), time.Now())
	if err != nil {
		t.Fatal(err)
	}

	n1 := g.Nodes["node:n1"]
	if n1.LastFailureTS != old.UnixMilli() || n1.LastFailureStatus != 429 {
		t.Fatalf("node:n1 failure = %d/%d, want %d/429", n1.LastFailureTS, n1.LastFailureStatus, old.UnixMilli())
	}
	if n1.LastAttemptTS != old.UnixMilli() || n1.LastAttemptStatus != 429 {
		t.Fatalf("node:n1 attempt = %d/%d, want %d/429", n1.LastAttemptTS, n1.LastAttemptStatus, old.UnixMilli())
	}

	n2 := g.Nodes["node:n2"]
	if n2.LastFailureTS != newer.UnixMilli() || n2.LastFailureStatus != 502 {
		t.Fatalf("node:n2 failure = %d/%d, want %d/502", n2.LastFailureTS, n2.LastFailureStatus, newer.UnixMilli())
	}
	if n2.LastAttemptTS != newer.UnixMilli() || n2.LastAttemptStatus != 503 {
		t.Fatalf("node:n2 attempt = %d/%d, want %d/503", n2.LastAttemptTS, n2.LastAttemptStatus, newer.UnixMilli())
	}

	// provider:p and route:r merge both parents: the newer one wins.
	for _, key := range []string{"provider:p", "route:r"} {
		got := g.Nodes[key]
		if got.LastFailureTS != newer.UnixMilli() || got.LastFailureStatus != 502 {
			t.Fatalf("%s failure = %d/%d, want %d/502", key, got.LastFailureTS, got.LastFailureStatus, newer.UnixMilli())
		}
		if got.LastAttemptTS != newer.UnixMilli() || got.LastAttemptStatus != 503 {
			t.Fatalf("%s attempt = %d/%d, want %d/503", key, got.LastAttemptTS, got.LastAttemptStatus, newer.UnixMilli())
		}
	}
}

// TestAggregatorNewestAttemptBreaksTiesByIndex covers one parent's attempts,
// which all share the parent's timestamp: the last attempt is the newest even
// when the rows arrive newest index first.
func TestAggregatorNewestAttemptBreaksTiesByIndex(t *testing.T) {
	a := NewAggregator()
	ts := time.Now().UnixMilli()
	a.AddAttempt(GraphAttemptRow{RequestID: "r", Attempt: 1, TS: ts, Route: "r", Node: "n", Provider: "p", Status: 503})
	a.AddAttempt(GraphAttemptRow{RequestID: "r", Attempt: 0, TS: ts, Route: "r", Node: "n", Provider: "p", Status: 429})

	if got := a.Metrics(nil, nil, GraphMetrics{}, true).Nodes["node:n"]; got.LastAttemptStatus != 503 {
		t.Fatalf("last attempt status = %d, want 503 (attempt 1)", got.LastAttemptStatus)
	}
}

// TestGraphPayloadCarriesLastFailureFields pins the JSON the console decodes.
func TestGraphPayloadCarriesLastFailureFields(t *testing.T) {
	ts := time.Now().Truncate(time.Second)
	agg := NewAggregator()
	agg.Add(GraphRow{ID: "r", TS: ts.UnixMilli(), Route: "r", Node: "n", Provider: "p", Status: 429, Error: "rate limited"})

	b, err := json.Marshal(agg.Metrics(nil, nil, GraphMetrics{}, true))
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Nodes map[string]map[string]json.RawMessage `json:"nodes"`
	}
	if err := json.Unmarshal(b, &payload); err != nil {
		t.Fatal(err)
	}
	node := payload.Nodes["node:n"]
	want := map[string]bool{
		"lastFailureTs":     true,
		"lastFailureStatus": true,
		"lastAttemptTs":     false, // omitted when nothing failed as an attempt
		"lastAttemptStatus": true,
	}
	for key, present := range want {
		_, ok := node[key]
		if ok != present {
			t.Fatalf("node:n payload key %q present = %v, want %v (payload %s)", key, ok, present, b)
		}
	}
}
