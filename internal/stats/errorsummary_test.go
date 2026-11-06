package stats

import (
	"context"
	"testing"
	"time"
)

func TestErrorSummary(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	base := time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)
	rows := []Request{
		{ID: "r1", TS: base, Provider: "p", Route: "a", ClientModel: "m1", Status: 200},
		{ID: "r2", TS: base.Add(time.Minute), Provider: "q", Route: "b", ClientModel: "m2", Status: 429, Error: "rate limit", ErrorClass: ErrorClassUpstreamQuota},
		{ID: "r3", TS: base.Add(2 * time.Minute), Provider: "q", Route: "b", ClientModel: "m2", Status: 429, Error: "rate limit", ErrorClass: ErrorClassUpstreamQuota},
		{ID: "r4", TS: base.Add(3 * time.Minute), Provider: "q", Route: "b", ClientModel: "m2", Status: 500, Error: "boom", ErrorClass: ErrorClassUpstreamUnavailable},
	}
	for _, r := range rows {
		if err := s.Insert(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	sum, err := s.ErrorSummary(ctx, base, base.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if sum.Total != 4 || sum.Errors != 3 || sum.ErrorRate != 0.75 {
		t.Fatalf("totals = %+v", sum)
	}
	if len(sum.ByClass) != 2 || sum.ByClass[0].Key != ErrorClassUpstreamQuota || sum.ByClass[0].Errors != 2 {
		t.Fatalf("byClass = %+v", sum.ByClass)
	}
	if len(sum.ByProvider) != 2 {
		t.Fatalf("byProvider = %+v", sum.ByProvider)
	}
	if len(sum.TopMessages) != 2 || sum.TopMessages[0].Message != "rate limit" || sum.TopMessages[0].Count != 2 {
		t.Fatalf("topMessages = %+v", sum.TopMessages)
	}
	if sum.TopMessages[0].LastSeen == "" {
		t.Fatalf("topMessages lastSeen empty: %+v", sum.TopMessages)
	}
}

func TestFingerprintErrorCollapsesVolatileParts(t *testing.T) {
	a := FingerprintError("request_id: abc-123 failed for 203.0.113.9 at 123e4567-e89b-12d3-a456-426614174000 deadbeefdeadbeef")
	b := FingerprintError("request_id: def-456 failed for 198.51.100.7 at 223e4567-e89b-12d3-a456-426614174000 cafebabecafebabe")
	if a != b {
		t.Fatalf("fingerprints differ:\n%q\n%q", a, b)
	}
	if got := FingerprintError("  Rate   Limit  "); got != "rate limit" {
		t.Fatalf("fingerprint = %q", got)
	}
}

func TestAlerts(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)
	mk := func(id string, ts time.Time, class string) Request {
		return Request{ID: id, TS: ts, Status: 500, Error: "e", ErrorClass: class}
	}
	for _, r := range []Request{
		mk("a1", now.Add(-time.Minute), ErrorClassUpstreamAuth),
		mk("g1", now.Add(-2*time.Minute), ErrorClassGatewayTimeout),
		mk("t1", now.Add(-3*time.Minute), ErrorClassUpstreamTruncated),
		mk("t2", now.Add(-4*time.Minute), ErrorClassUpstreamTruncated),
		mk("t3", now.Add(-5*time.Minute), ErrorClassUpstreamTruncated),
		mk("t4", now.Add(-6*time.Minute), ErrorClassUpstreamTruncated),
	} {
		if err := s.Insert(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	alerts, err := s.Alerts(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	ids := make(map[string]bool, len(alerts))
	for _, a := range alerts {
		ids[a.ID] = true
	}
	for _, want := range []string{"auth", "gateway_timeout", "truncation"} {
		if !ids[want] {
			t.Fatalf("missing alert %q: %+v", want, alerts)
		}
	}
	if ids["error_rate"] {
		t.Fatalf("unexpected error_rate alert with only 6 requests: %+v", alerts)
	}
}
