package authfail

import (
	"testing"
	"time"
)

func TestTrackerKeepsRecentFailuresInOrder(t *testing.T) {
	tr := New(3)
	base := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	for i, reason := range []string{"missing", "invalid", "disabled"} {
		tr.Add(Failure{TS: base.Add(time.Duration(i) * time.Second), Reason: reason})
	}

	snap := tr.Snapshot()
	if len(snap.Recent) != 3 {
		t.Fatalf("recent = %d, want 3", len(snap.Recent))
	}
	if snap.Truncated {
		t.Fatal("truncated = true, want false")
	}
	for i, want := range []string{"missing", "invalid", "disabled"} {
		if snap.Recent[i].Reason != want {
			t.Fatalf("recent[%d] = %q, want %q", i, snap.Recent[i].Reason, want)
		}
	}
}

func TestTrackerDropsOldestAtLimit(t *testing.T) {
	tr := New(2)
	base := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	for i, reason := range []string{"missing", "invalid", "disabled"} {
		tr.Add(Failure{TS: base.Add(time.Duration(i) * time.Second), Reason: reason})
	}

	snap := tr.Snapshot()
	if len(snap.Recent) != 2 {
		t.Fatalf("recent = %d, want 2", len(snap.Recent))
	}
	if !snap.Truncated {
		t.Fatal("truncated = false, want true")
	}
	if snap.Recent[0].Reason != "invalid" || snap.Recent[1].Reason != "disabled" {
		t.Fatalf("recent = %+v, want the two newest", snap.Recent)
	}
}

func TestSnapshotIsACopy(t *testing.T) {
	tr := New(2)
	tr.Add(Failure{Reason: "missing"})
	snap := tr.Snapshot()

	tr.Add(Failure{Reason: "invalid"})
	snap.Recent[0].Reason = "mutated"

	fresh := tr.Snapshot()
	if len(fresh.Recent) != 2 {
		t.Fatalf("recent = %d, want 2", len(fresh.Recent))
	}
	if fresh.Recent[0].Reason != "missing" {
		t.Fatalf("recent[0] = %q, want missing", fresh.Recent[0].Reason)
	}
}

func TestNilTrackerIsSafe(t *testing.T) {
	var tr *Tracker
	tr.Add(Failure{Reason: "invalid"})

	snap := tr.Snapshot()
	if snap.Recent == nil {
		t.Fatal("recent = nil, want an empty slice")
	}
	if len(snap.Recent) != 0 || snap.Truncated {
		t.Fatalf("snapshot = %+v, want empty", snap)
	}
}

func TestAddDefaultsTimestampToUTC(t *testing.T) {
	tr := New(1)
	tr.Add(Failure{})
	tr.Add(Failure{TS: time.Date(2026, 11, 8, 12, 0, 0, 0, time.FixedZone("x", 3600))})

	snap := tr.Snapshot()
	if snap.Recent[0].TS.IsZero() {
		t.Fatal("timestamp is zero")
	}
	if snap.Recent[0].TS.Location() != time.UTC {
		t.Fatalf("location = %v, want UTC", snap.Recent[0].TS.Location())
	}
}
