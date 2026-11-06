package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-gost/llm-gw/internal/stats"
)

func TestParseServeArgs(t *testing.T) {
	got, err := parseServeArgs([]string{"serve", "-c", "x.yaml"})
	if err != nil {
		t.Fatal(err)
	}
	if got != "x.yaml" {
		t.Fatalf("path = %q, want x.yaml", got)
	}

	got, err = parseServeArgs([]string{"serve"})
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Fatalf("default path = %q, want empty", got)
	}

	if _, err := parseServeArgs(nil); err == nil {
		t.Fatal("want usage error")
	}
	if _, err := parseServeArgs([]string{"run"}); err == nil {
		t.Fatal("want usage error for unknown subcommand")
	}
}

func TestResetAdminSessions(t *testing.T) {
	store, err := stats.Open(filepath.Join(t.TempDir(), "stats.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()
	if err := store.PutSession(ctx, "old", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := resetAdminSessions(store, false); err != nil {
		t.Fatal(err)
	}
	if got := mustLoadSessions(t, store); len(got) != 1 {
		t.Fatalf("sessions = %d, want 1", len(got))
	}
	if err := resetAdminSessions(store, true); err != nil {
		t.Fatal(err)
	}
	if got := mustLoadSessions(t, store); len(got) != 0 {
		t.Fatalf("sessions = %d, want 0", len(got))
	}
	if err := resetAdminSessions(nil, true); err != nil {
		t.Fatal(err)
	}
}

func mustLoadSessions(t *testing.T, store *stats.Store) map[string]time.Time {
	t.Helper()
	sessions, err := store.LoadSessions(context.Background(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return sessions
}
