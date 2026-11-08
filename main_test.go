package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ginuerzh/cuttle/internal/bootstrap"
	"github.com/ginuerzh/cuttle/internal/stats"
	"github.com/ginuerzh/cuttle/internal/version"
)

func TestPrintVersion(t *testing.T) {
	var buf bytes.Buffer
	printVersion(&buf)
	want := "cuttle " + version.String() + "\n"
	if got := buf.String(); got != want {
		t.Fatalf("printVersion() = %q, want %q", got, want)
	}
}

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

func TestApplySessionReset(t *testing.T) {
	store, err := stats.Open(filepath.Join(t.TempDir(), "stats.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	dataDir := t.TempDir()
	marker := filepath.Join(dataDir, bootstrap.SessionsResetFileName)
	ctx := context.Background()

	if err := store.PutSession(ctx, "old", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := applySessionReset(store, dataDir, true); err != nil {
		t.Fatal(err)
	}
	if got := mustLoadSessions(t, store); len(got) != 0 {
		t.Fatalf("sessions = %d, want 0", len(got))
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("marker still exists: %v", err)
	}

	if err := store.PutSession(ctx, "keep", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := applySessionReset(store, dataDir, false); err != nil {
		t.Fatal(err)
	}
	if got := mustLoadSessions(t, store); len(got) != 1 {
		t.Fatalf("sessions = %d, want 1 without a reset", len(got))
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("marker was removed without a reset: %v", err)
	}

	if err := applySessionReset(nil, dataDir, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("marker still exists after a nil-store reset: %v", err)
	}
}
