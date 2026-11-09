package stats

import (
	"context"
	"testing"
	"time"

	"github.com/ginuerzh/cuttle/internal/capture"
)

func TestCaptureRetainOverridesFallback(t *testing.T) {
	s := newStore(t)
	now := time.Now()

	if got := s.captureCutoff(now, time.Hour); !got.Equal(now.Add(-time.Hour)) {
		t.Fatalf("cutoff = %v, want the fallback", got)
	}

	s.SetCaptureRetain(10 * time.Minute)
	if got := s.captureCutoff(now, time.Hour); !got.Equal(now.Add(-10 * time.Minute)) {
		t.Fatalf("cutoff = %v, want the reloaded retention", got)
	}
	if got := s.CaptureRetain(); got != 10*time.Minute {
		t.Fatalf("CaptureRetain = %v", got)
	}
}

func TestCaptureRetainReloadIsLive(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	if err := s.PutCapture(ctx, "old", &capture.Capture{CreatedAt: time.Now().Add(-2 * time.Hour)}); err != nil {
		t.Fatal(err)
	}

	// The loop starts with a generous fallback; the reloaded retention wins.
	s.SetCaptureRetain(1 * time.Hour)
	loopCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.StartCleanup(loopCtx, 5*time.Millisecond, 24*time.Hour, 24*time.Hour, 24*time.Hour)

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		got, err := s.GetCapture(ctx, "old")
		if err != nil {
			t.Fatal(err)
		}
		if got == nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("capture older than the reloaded retention survived cleanup")
}
