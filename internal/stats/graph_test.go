package stats

import (
	"context"
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
