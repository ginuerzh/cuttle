package gateway_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	xlogger "github.com/go-gost/x/logger"

	"github.com/ginuerzh/cuttle/internal/config"
	"github.com/ginuerzh/cuttle/internal/gateway"
	"github.com/ginuerzh/cuttle/internal/stats"
)

// TestApplyMigratesStatsAndSyncsLabels drives the one-time id migration and the
// label sync through New and Reload: both reach them through apply.
func TestApplyMigratesStatsAndSyncsLabels(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stats.sqlite")
	base := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)

	cfg := &config.Config{}
	cfg.Gateway.Listen = "127.0.0.1:0"
	cfg.Stats.Path = path
	cfg.Entrypoints = []config.Entrypoint{{Name: "edge", Match: []string{"*"}, Route: "fast"}}
	cfg.Providers = []config.Provider{{
		Name: "alpha",
		Type: "generic",
		Chat: "http://127.0.0.1:1",
		Keys: []config.ProviderKey{{Name: "key-1", Key: "sk-secret"}},
	}}
	cfg.Nodes = []config.Node{{Name: "n1", Provider: "alpha", Model: "m"}}
	cfg.Routes = []config.Route{{Name: "fast", Nodes: []string{"n1"}}}
	withIDs(t, cfg)

	// A database written by the previous release: rows hold display names.
	seed, err := stats.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := seed.Insert(context.Background(), stats.Request{
		ID: "s1", TS: base, Provider: "alpha", Route: "fast", Node: "n1", Model: "m",
	}); err != nil {
		seed.Close()
		t.Fatal(err)
	}
	if err := seed.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA user_version = 1`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	gw, err := gateway.New(cfg, xlogger.Nop())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { gw.Close() })
	ctx := context.Background()

	row, err := gw.Stats().Get(ctx, "s1")
	if err != nil || row == nil {
		t.Fatalf("get s1 = %+v err=%v", row, err)
	}
	if row.Provider != cfg.Providers[0].ID || row.Route != cfg.Routes[0].ID || row.Node != cfg.Nodes[0].ID {
		t.Fatalf("row = %q/%q/%q, want %q/%q/%q", row.Provider, row.Route, row.Node,
			cfg.Providers[0].ID, cfg.Routes[0].ID, cfg.Nodes[0].ID)
	}
	if backups, err := filepath.Glob(path + ".v1.bak-*"); err != nil || len(backups) != 1 {
		t.Fatalf("backups = %v err=%v, want exactly one", backups, err)
	}

	labels, err := gw.Stats().Labels(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for kind, want := range map[string]struct{ id, name string }{
		"provider":   {cfg.Providers[0].ID, "alpha"},
		"route":      {cfg.Routes[0].ID, "fast"},
		"node":       {cfg.Nodes[0].ID, "n1"},
		"entrypoint": {cfg.Entrypoints[0].ID, "edge"},
	} {
		if got := labels[kind][want.id]; got != want.name {
			t.Fatalf("%s label %q = %q, want %q (labels %+v)", kind, want.id, got, want.name, labels)
		}
	}

	// A reload after an admin rename must keep the id and follow the new name.
	cfg.Providers[0].Name = "alpha-renamed"
	if err := gw.Reload(cfg); err != nil {
		t.Fatal(err)
	}
	labels, err = gw.Stats().Labels(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := labels["provider"][cfg.Providers[0].ID]; got != "alpha-renamed" {
		t.Fatalf("provider label after rename = %q, want alpha-renamed", got)
	}
	row, err = gw.Stats().Get(ctx, "s1")
	if err != nil || row == nil {
		t.Fatalf("get s1 after reload = %+v err=%v", row, err)
	}
	if row.Provider != cfg.Providers[0].ID {
		t.Fatalf("row provider after rename = %q, want the unchanged id %q", row.Provider, cfg.Providers[0].ID)
	}
}
