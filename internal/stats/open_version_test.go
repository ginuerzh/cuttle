package stats

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestOpenLeavesExistingDatabaseForMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stats.sqlite")
	ctx := context.Background()
	base := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)

	// Build a database that already holds rows, then mark it as written by the
	// previous release. Open must not treat it as fresh, and the v2 rewrite must
	// stay a separate, explicit step.
	seed, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := seed.Insert(ctx, Request{ID: "old", TS: base, Provider: "alpha", Model: "m"}); err != nil {
		seed.Close()
		t.Fatal(err)
	}
	if _, err := seed.db.Exec(`PRAGMA user_version = 0`); err != nil {
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

	if got := userVersion(t, s); got != 1 {
		t.Fatalf("user_version after Open = %d, want 1 until the ids are rewritten", got)
	}
	row, err := s.Get(ctx, "old")
	if err != nil || row == nil {
		t.Fatalf("get old = %+v err=%v", row, err)
	}
	if row.Provider != "alpha" {
		t.Fatalf("old row provider = %q, want the v1 name until migration", row.Provider)
	}

	if err := s.MigrateIdentities(ctx, lookupIDs(map[string]string{"provider/alpha": "p_0000000001"})); err != nil {
		t.Fatal(err)
	}
	row, err = s.Get(ctx, "old")
	if err != nil || row == nil {
		t.Fatalf("get old after migration = %+v err=%v", row, err)
	}
	if row.Provider != "p_0000000001" {
		t.Fatalf("old row provider = %q, want the id", row.Provider)
	}
	if got := userVersion(t, s); got != statsVersion {
		t.Fatalf("user_version = %d, want %d", got, statsVersion)
	}
}
