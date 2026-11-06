package stats

import (
	"testing"
	"time"
)

func TestMarkFirstResponseKeepsFirstTimestamp(t *testing.T) {
	f := NewFacts()
	first := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	f.MarkFirstResponse("sid", first)
	f.MarkFirstResponse("sid", first.Add(time.Second))

	fact, ok := f.Take("sid")
	if !ok || !fact.FirstResponseAt.Equal(first) {
		t.Fatalf("first response = %v, want %v", fact.FirstResponseAt, first)
	}
}
