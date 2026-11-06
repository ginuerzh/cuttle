package stats

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func newStore(t *testing.T) *Store {
	t.Helper()

	s, err := Open(filepath.Join(t.TempDir(), "stats.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestInsertQuerySummary(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	base := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)

	rows := []Request{
		{ID: "r1", SID: "s1", TS: base.Add(1 * time.Minute), Provider: "p", Model: "m1", Effort: "high", Status: 200, DurationMS: 10, InputTokens: 1, OutputTokens: 2},
		{ID: "r2", SID: "s2", TS: base.Add(2 * time.Minute), Provider: "p", Model: "m1", Effort: "low", Status: 200, DurationMS: 20, InputTokens: 3, OutputTokens: 4},
		{ID: "r3", SID: "s3", TS: base.Add(3 * time.Minute), Provider: "p", Model: "m2", Effort: "medium", Status: 500, Error: "boom", DurationMS: 30, InputTokens: 5, OutputTokens: 6},
		{ID: "r4", SID: "s4", TS: base.Add(4 * time.Minute), Provider: "q", Model: "m2", Effort: "none", Status: 200, DurationMS: 40, InputTokens: 7, OutputTokens: 8},
	}
	for _, r := range rows {
		if err := s.Insert(ctx, r); err != nil {
			t.Fatal(err)
		}
	}

	got, err := s.Query(ctx, Filter{From: base, To: base.Add(time.Hour), Provider: "p"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("query rows = %d, want 3", len(got))
	}
	if got[0].ID != "r3" {
		t.Fatalf("first row = %q, want newest first", got[0].ID)
	}

	sum, err := s.Summary(ctx, base, base.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if sum.Requests != 4 || sum.Errors != 1 {
		t.Fatalf("totals = %d/%d, want 4/1", sum.Requests, sum.Errors)
	}
	if sum.InputTokens != 16 || sum.OutputTokens != 20 {
		t.Fatalf("tokens = %d/%d, want 16/20", sum.InputTokens, sum.OutputTokens)
	}
	if sum.P50MS != 30 || sum.P95MS != 40 {
		t.Fatalf("percentiles = %d/%d, want 30/40", sum.P50MS, sum.P95MS)
	}
	if len(sum.ByProvider) != 2 || sum.ByProvider[0].Key != "p" {
		t.Fatalf("byProvider = %+v", sum.ByProvider)
	}
	if sum.ByEffort["high"] != 1 || sum.ByEffort["none"] != 1 {
		t.Fatalf("byEffort = %+v", sum.ByEffort)
	}

	one, err := s.Get(ctx, "r2")
	if err != nil || one == nil || one.SID != "s2" {
		t.Fatalf("get = %+v err=%v", one, err)
	}
}

func TestCleanupKeepsRollups(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	base := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)

	if err := s.Insert(ctx, Request{ID: "old", TS: base, Provider: "p", Model: "m", Status: 200, InputTokens: 5}); err != nil {
		t.Fatal(err)
	}
	now := base.Add(48 * time.Hour)
	if err := s.Cleanup(ctx, now.Add(-24*time.Hour), now.Add(-90*24*time.Hour)); err != nil {
		t.Fatal(err)
	}

	rows, err := s.Query(ctx, Filter{From: base.Add(-time.Hour), To: now})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("detail rows = %d, want 0 after cleanup", len(rows))
	}
	sum, err := s.Summary(ctx, base.Add(-time.Hour), now)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Requests != 1 || sum.InputTokens != 5 {
		t.Fatalf("rollups lost: %+v", sum)
	}
}

func TestRequestsSchemaHasNoHeadersOrBody(t *testing.T) {
	s := newStore(t)
	rows, err := s.db.Query(`PRAGMA table_info(requests)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	for rows.Next() {
		var cid int
		var name, typ string
		var notNull, pk int
		var dflt any
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk); err != nil {
			t.Fatal(err)
		}
		lower := strings.ToLower(name)
		if strings.Contains(lower, "header") || strings.Contains(lower, "body") || strings.Contains(lower, "auth") {
			t.Fatalf("requests table must not store %q", name)
		}
	}
}

func TestTopologyPositions(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	empty, err := s.LoadTopologyPositions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(empty) != 0 {
		t.Fatalf("initial positions = %+v, want empty", empty)
	}

	first := map[string]Position{
		"entrypoint:fast": {X: 10, Y: 20},
		"node:deepseek":   {X: 30, Y: 40},
	}
	if err := s.SaveTopologyPositions(ctx, first); err != nil {
		t.Fatal(err)
	}
	got, err := s.LoadTopologyPositions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, first) {
		t.Fatalf("positions = %+v, want %+v", got, first)
	}

	second := map[string]Position{"route:fast": {X: -5.5, Y: 99}}
	if err := s.SaveTopologyPositions(ctx, second); err != nil {
		t.Fatal(err)
	}
	got, err = s.LoadTopologyPositions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, second) {
		t.Fatalf("replaced positions = %+v, want %+v", got, second)
	}

	if err := s.SaveTopologyPositions(ctx, nil); err != nil {
		t.Fatal(err)
	}
	got, err = s.LoadTopologyPositions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("cleared positions = %+v, want empty", got)
	}
}

func TestRequestRewriteFieldsRoundTrip(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	want := Request{
		ID: "rid", SID: "rid", TS: time.Now().UTC(),
		InboundProtocol: "anthropic",
		ErrorStage:      "stream_rewrite",
		ErrorDirection:  "response",
		SSEPhase:        "end",
		EventIndex:      7,
	}
	if err := s.Insert(ctx, want); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, "rid")
	if err != nil || got == nil {
		t.Fatalf("get = %+v err=%v", got, err)
	}
	if got.InboundProtocol != want.InboundProtocol || got.ErrorStage != want.ErrorStage ||
		got.ErrorDirection != want.ErrorDirection || got.SSEPhase != want.SSEPhase ||
		got.EventIndex != want.EventIndex {
		t.Fatalf("fields = %+v", got)
	}
}
