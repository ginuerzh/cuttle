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
			{Attempt: 0, TS: base, Node: "n1", Status: 429, ErrorClass: "upstream_quota"},
			{Attempt: 1, TS: base.Add(time.Second), Node: "n2", Status: 503, ErrorClass: "upstream_unavailable"},
		}},
		{ID: "b", TS: base.Add(time.Minute), Route: "r", Node: "n1", Provider: "p", Attempts: []Attempt{
			{Attempt: 0, TS: base.Add(time.Minute), Node: "n1", Status: 429, ErrorClass: "upstream_quota"},
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
	if all[0].RequestID != "b" || all[1].RequestID != "a" || all[2].TS.UnixMilli() != base.UnixMilli() {
		t.Fatalf("attempts = %+v, want newest first", all)
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
