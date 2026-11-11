package stats

import (
	"context"
	"testing"
	"time"
)

func TestSeriesBucketsRequestsAndErrors(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	base := time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)

	rows := []Request{
		{ID: "a", SID: "a", TS: base.Add(10 * time.Second), Provider: "p1", Status: 200},
		{ID: "b", SID: "b", TS: base.Add(50 * time.Second), Provider: "p1", Status: 429, Error: "slow down"},
		{ID: "c", SID: "c", TS: base.Add(2*time.Minute + 5*time.Second), Provider: "p1", Status: 200},
		{ID: "d", SID: "d", TS: base.Add(5*time.Minute + time.Second), Provider: "p2", Status: 502, Error: "bad gateway"},
	}
	for _, r := range rows {
		if err := s.Insert(ctx, r); err != nil {
			t.Fatal(err)
		}
	}

	got, err := s.Series(ctx, Filter{From: base, To: base.Add(6 * time.Minute), Provider: "p1"}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Points) != 2 {
		t.Fatalf("points = %+v, want the two non-empty p1 buckets", got.Points)
	}
	if !got.Points[0].Bucket.Equal(base) || got.Points[0].Requests != 2 || got.Points[0].Errors != 1 {
		t.Fatalf("first point = %+v, want base bucket 2 requests / 1 error", got.Points[0])
	}
	if !got.Points[1].Bucket.Equal(base.Add(2*time.Minute)) || got.Points[1].Requests != 1 || got.Points[1].Errors != 0 {
		t.Fatalf("second point = %+v, want the +2m bucket with one healthy request", got.Points[1])
	}

	failed, err := s.Series(ctx, Filter{From: base, To: base.Add(6 * time.Minute), OnlyErrors: true}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(failed.Points) != 2 {
		t.Fatalf("failed points = %+v, want the two failed buckets", failed.Points)
	}
	if failed.Points[0].Requests != 1 || failed.Points[0].Errors != 1 {
		t.Fatalf("failed[0] = %+v, want one failed request", failed.Points[0])
	}
	if !failed.Points[1].Bucket.Equal(base.Add(5 * time.Minute)) {
		t.Fatalf("failed[1] = %+v, want the +5m bucket", failed.Points[1])
	}

	// A window that does not start on a bucket boundary keeps its first partial
	// bucket: bars are drawn at from+i*width, so an epoch-aligned bucket would
	// fall before from and be dropped.
	offsetBase := base.Add(23 * time.Second)
	offsetRows := []Request{
		{ID: "e", SID: "e", TS: offsetBase.Add(10 * time.Second), Provider: "p3", Status: 200},
		{ID: "f", SID: "f", TS: offsetBase.Add(70 * time.Second), Provider: "p3", Status: 429, Error: "slow"},
	}
	for _, r := range offsetRows {
		if err := s.Insert(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	unaligned, err := s.Series(ctx, Filter{From: offsetBase, To: offsetBase.Add(3 * time.Minute), Provider: "p3"}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(unaligned.Points) != 2 {
		t.Fatalf("unaligned points = %+v, want the two buckets inside the window", unaligned.Points)
	}
	if !unaligned.Points[0].Bucket.Equal(offsetBase) {
		t.Fatalf("first unaligned bucket = %s, want the window start %s", unaligned.Points[0].Bucket, offsetBase)
	}
	if !unaligned.Points[1].Bucket.Equal(offsetBase.Add(time.Minute)) {
		t.Fatalf("second unaligned bucket = %s", unaligned.Points[1].Bucket)
	}
	if got := unaligned.Points[0].Requests + unaligned.Points[1].Requests; got != 2 {
		t.Fatalf("unaligned total = %d, want both rows counted", got)
	}

	empty, err := s.Series(ctx, Filter{From: base.Add(10 * time.Minute), To: base.Add(11 * time.Minute)}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if empty.Points == nil || len(empty.Points) != 0 {
		t.Fatalf("empty series = %+v, want a non-nil empty slice", empty.Points)
	}
}
