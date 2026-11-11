package stats

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestInsertWritesAttemptsWithoutTouchingRollups(t *testing.T) {
	s := newStore(t)
	ts := time.Now().Add(-time.Minute)
	req := Request{
		ID: "req-1", SID: "req-1", TS: ts, Provider: "p1", Route: "r1", Node: "n1",
		Status: 200, Retries: 1,
		Attempts: []Attempt{{
			RequestID: "req-1", Attempt: 0, TS: ts, Route: "r1", Node: "n1", Provider: "p1",
			Status: 429, Error: "Rate limit exceeded.", ErrorClass: "upstream_quota",
			DurationMS: 10, BytesIn: 100, BytesOut: 40,
		}},
	}
	if err := s.Insert(context.Background(), req); err != nil {
		t.Fatal(err)
	}

	got, err := s.AttemptsForRequest(context.Background(), "req-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Status != 429 || got[0].ErrorClass != "upstream_quota" {
		t.Fatalf("attempts = %+v", got)
	}

	sum, err := s.Summary(context.Background(), ts.Add(-time.Hour), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if sum.Requests != 1 || sum.Errors != 0 {
		t.Fatalf("summary = %+v, want 1 request and 0 errors", sum)
	}
	rows, err := s.Query(context.Background(), Filter{From: ts.Add(-time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("request rows = %d, want 1", len(rows))
	}
}

func TestAttemptsAreReplacedAndPruned(t *testing.T) {
	s := newStore(t)
	ts := time.Now().Add(-time.Hour)
	req := Request{ID: "r", SID: "r", TS: ts, Node: "n1", Attempts: []Attempt{
		{RequestID: "r", Attempt: 0, TS: ts, Node: "n1", Status: 429},
	}}
	if err := s.Insert(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	req.Attempts[0].Error = "second delivery"
	if err := s.Insert(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	got, _ := s.AttemptsForRequest(context.Background(), "r")
	if len(got) != 1 || got[0].Error != "second delivery" {
		t.Fatalf("attempts = %+v, want one replaced row", got)
	}

	if err := s.Cleanup(context.Background(), time.Now(), time.Now(), time.Now()); err != nil {
		t.Fatal(err)
	}
	got, _ = s.AttemptsForRequest(context.Background(), "r")
	if len(got) != 0 {
		t.Fatalf("attempts after cleanup = %+v, want none", got)
	}
}

func TestAttemptsQueryOrderAndFilters(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	base := time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)

	for _, r := range []Request{
		{ID: "a", TS: base, Route: "r", Node: "n1", Provider: "p", Attempts: []Attempt{
			{Attempt: 0, Node: "n1", Status: 429, ErrorClass: "upstream_quota"},
			{Attempt: 1, Node: "n2", Status: 503, ErrorClass: "upstream_unavailable"},
		}},
		{ID: "b", TS: base.Add(time.Minute), Route: "r", Node: "n1", Provider: "p", Attempts: []Attempt{
			{Attempt: 0, Node: "n1", Status: 429, ErrorClass: "upstream_quota"},
		}},
	} {
		if err := s.Insert(ctx, r); err != nil {
			t.Fatal(err)
		}
	}

	all, err := s.Attempts(ctx, AttemptFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("attempts = %d, want 3", len(all))
	}
	if all[0].RequestID != "b" || all[1].RequestID != "a" || all[1].Attempt != 1 || all[2].Attempt != 0 {
		t.Fatalf("attempts = %+v, want newest request first, newest attempt first", all)
	}
	if !all[1].TS.Equal(base) || !all[2].TS.Equal(base) {
		t.Fatalf("attempt ts = %v/%v, want the parent's %v", all[1].TS, all[2].TS, base)
	}

	node, err := s.Attempts(ctx, AttemptFilter{Node: "n2"})
	if err != nil {
		t.Fatal(err)
	}
	if len(node) != 1 || node[0].Status != 503 {
		t.Fatalf("node=n2 attempts = %+v", node)
	}

	byClass, err := s.Attempts(ctx, AttemptFilter{ErrorClass: "upstream_quota", Status: 429})
	if err != nil {
		t.Fatal(err)
	}
	if len(byClass) != 2 {
		t.Fatalf("quota 429 attempts = %+v, want 2", byClass)
	}

	window, err := s.Attempts(ctx, AttemptFilter{From: base.Add(30 * time.Second), To: base.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if len(window) != 1 || window[0].RequestID != "b" {
		t.Fatalf("window attempts = %+v, want only b", window)
	}

	page, err := s.Attempts(ctx, AttemptFilter{Limit: 1, Offset: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 1 || page[0].RequestID != "a" || page[0].Attempt != 1 {
		t.Fatalf("page attempts = %+v", page)
	}

	empty, err := s.Attempts(ctx, AttemptFilter{Node: "absent"})
	if err != nil {
		t.Fatal(err)
	}
	if empty == nil || len(empty) != 0 {
		t.Fatalf("empty attempts = %+v, want []", empty)
	}
}

// TestAttemptsShareParentTimestamp pins the design rule that an attempt is
// stored with its parent's start time: retention and the graph windows filter
// on it, so a caller-supplied Attempt.TS must never reach the table.
func TestAttemptsShareParentTimestamp(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	parent := time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)

	if err := s.Insert(ctx, Request{ID: "r", SID: "r", TS: parent, Node: "n1", Attempts: []Attempt{
		{Attempt: 0, TS: parent.Add(-time.Hour), Status: 429},
		{Attempt: 1, TS: parent.Add(48 * time.Hour), Status: 503},
		{Attempt: 2, Status: 429},
	}}); err != nil {
		t.Fatal(err)
	}

	got, err := s.AttemptsForRequest(ctx, "r")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("attempts = %+v, want 3", got)
	}
	for _, a := range got {
		if !a.TS.Equal(parent) {
			t.Fatalf("attempt %d ts = %v, want the parent's %v", a.Attempt, a.TS, parent)
		}
	}

	// A far-future Attempt.TS must not keep a row alive past its parent.
	if err := s.Cleanup(ctx, parent.Add(time.Minute), parent, time.Now()); err != nil {
		t.Fatal(err)
	}
	got, err = s.AttemptsForRequest(ctx, "r")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("attempts after cleanup = %+v, want none", got)
	}
}

func TestAttemptsLimitIsCapped(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	ts := time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)

	attempts := make([]Attempt, 0, 1005)
	for i := 0; i < 1005; i++ {
		attempts = append(attempts, Attempt{Attempt: i, Status: 429})
	}
	if err := s.Insert(ctx, Request{ID: "r", SID: "r", TS: ts, Attempts: attempts}); err != nil {
		t.Fatal(err)
	}

	capped, err := s.Attempts(ctx, AttemptFilter{Limit: 5000})
	if err != nil {
		t.Fatal(err)
	}
	if len(capped) != 1000 {
		t.Fatalf("limit=5000 rows = %d, want the 1000-row cap", len(capped))
	}
	if capped[0].Attempt != 1004 {
		t.Fatalf("first row attempt = %d, want the newest 1004", capped[0].Attempt)
	}

	exact, err := s.Attempts(ctx, AttemptFilter{Limit: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if len(exact) != 1000 {
		t.Fatalf("limit=1000 rows = %d, want 1000", len(exact))
	}

	def, err := s.Attempts(ctx, AttemptFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(def) != 100 {
		t.Fatalf("default rows = %d, want 100", len(def))
	}
}

func TestOpenAddsAttemptTableToExistingDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stats.sqlite")
	seed, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	// A database written by the previous release has no attempt table; dropping
	// it (and its indexes) leaves the schema an upgrade must extend.
	if _, err := seed.db.Exec(`DROP TABLE request_attempts`); err != nil {
		seed.Close()
		t.Fatal(err)
	}
	if err := seed.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	ctx := context.Background()

	for _, obj := range []struct{ kind, name string }{
		{"table", "request_attempts"},
		{"index", "request_attempts_seq"},
		{"index", "request_attempts_node"},
		{"index", "request_attempts_ts"},
	} {
		var n int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = ? AND name = ?`,
			obj.kind, obj.name).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Fatalf("%s %s was not created", obj.kind, obj.name)
		}
	}

	if err := s.Insert(ctx, Request{ID: "r", TS: time.Now(), Attempts: []Attempt{
		{Attempt: 0, Status: 429, BodyPreview: `{"error":"busy"}`},
	}}); err != nil {
		t.Fatal(err)
	}
	got, err := s.AttemptsForRequest(ctx, "r")
	if err != nil || len(got) != 1 || got[0].BodyPreview != `{"error":"busy"}` {
		t.Fatalf("attempts = %+v err=%v", got, err)
	}
	var seq int64
	if err := s.db.QueryRow(`SELECT seq FROM request_attempts WHERE request_id = 'r'`).Scan(&seq); err != nil {
		t.Fatal(err)
	}
	if seq == 0 {
		t.Fatal("attempt row did not inherit the parent's seq")
	}
}
