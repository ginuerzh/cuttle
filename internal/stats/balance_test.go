package stats

import (
	"context"
	"testing"
	"time"
)

func TestMergeBalancePointRules(t *testing.T) {
	base := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	p := func(at time.Time, amount float64) BalancePoint {
		return BalancePoint{Provider: "p_1", Key: "pk_1", Currency: "USD", At: at, Amount: amount}
	}

	// an out-of-order reading is dropped
	got := mergeBalancePoint([]BalancePoint{p(base, 10)}, p(base.Add(-time.Minute), 9))
	if len(got) != 1 || got[0].Amount != 10 {
		t.Fatalf("out-of-order = %+v", got)
	}

	// an equal run keeps one sliding point
	pts := []BalancePoint{p(base, 10), p(base.Add(time.Minute), 10)}
	got = mergeBalancePoint(pts, p(base.Add(2*time.Minute), 10))
	if len(got) != 2 || !got[1].At.Equal(base.Add(2*time.Minute)) {
		t.Fatalf("flat run = %+v", got)
	}

	// a closer reading that did not rise moves the last point
	pts = []BalancePoint{p(base, 10), p(base.Add(time.Minute), 9)}
	got = mergeBalancePoint(pts, p(base.Add(2*time.Minute), 8))
	if len(got) != 2 || !got[1].At.Equal(base.Add(2*time.Minute)) || got[1].Amount != 8 {
		t.Fatalf("close reading = %+v", got)
	}

	// a top-up is always appended, even two minutes later
	pts = []BalancePoint{p(base, 10), p(base.Add(time.Minute), 9)}
	got = mergeBalancePoint(pts, p(base.Add(2*time.Minute), 49))
	if len(got) != 3 || got[2].Amount != 49 {
		t.Fatalf("top-up = %+v", got)
	}
}

func TestBalanceSeriesDownsamples(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	from := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
	var pts []BalancePoint
	for i := 0; i < 10; i++ {
		pts = append(pts, BalancePoint{Provider: "p_1", Key: "pk_1", Currency: "USD",
			At: from.Add(time.Duration(i) * 10 * time.Minute), Amount: float64(100 - i)})
	}
	pts = append(pts, BalancePoint{Provider: "p_1", Key: "pk_2", Currency: "CNY",
		At: from.Add(time.Minute), Amount: 7})
	if err := s.RecordBalancePoints(ctx, pts); err != nil {
		t.Fatal(err)
	}
	// 10-minute spacing is wider than the merge gap, so all ten readings stay;
	// 100 minutes over 3 points keeps the last reading of each third.
	got, err := s.BalanceSeries(ctx, from, from.Add(100*time.Minute), 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("groups = %+v", got)
	}
	usd := got[0]
	if usd.Key != "pk_1" || len(usd.Points) != 3 {
		t.Fatalf("usd group = %+v, want 3 points of pk_1", usd)
	}
	if usd.Points[2].Amount != 91 {
		t.Fatalf("last usd amount = %v, want the reading at +90m", usd.Points[2].Amount)
	}
	for i := 1; i < len(usd.Points); i++ {
		if !usd.Points[i].At.After(usd.Points[i-1].At) {
			t.Fatalf("points not ascending: %+v", usd.Points)
		}
	}
}

func TestCleanupDeletesBalancePoints(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	old := BalancePoint{Provider: "p_1", Key: "pk_1", Currency: "USD",
		At: now.Add(-48 * time.Hour), Amount: 20}
	fresh := BalancePoint{Provider: "p_1", Key: "pk_1", Currency: "USD",
		At: now.Add(-time.Minute), Amount: 11}
	if err := s.RecordBalancePoints(ctx, []BalancePoint{old, fresh}); err != nil {
		t.Fatal(err)
	}
	cutoff := now.Add(-24 * time.Hour)
	if err := s.Cleanup(ctx, cutoff, cutoff, cutoff); err != nil {
		t.Fatal(err)
	}
	got, err := s.BalanceSeries(ctx, now.Add(-72*time.Hour), now.Add(time.Minute), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || len(got[0].Points) != 1 || got[0].Points[0].Amount != 11 {
		t.Fatalf("points after cleanup = %+v, want only the recent reading", got)
	}
}
