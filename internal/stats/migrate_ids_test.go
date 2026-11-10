package stats

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/ginuerzh/cuttle/internal/identity"
)

// openStoreAt returns a store plus the database path, so a test can inspect the
// v1 backup beside it.
func openStoreAt(t *testing.T) (*Store, string) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "stats.sqlite")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, path
}

// forceVersion marks a store as a database written by the previous release: its
// rows still hold display names and it has not been migrated yet.
func forceVersion(t *testing.T, s *Store, version int) {
	t.Helper()

	if _, err := s.db.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, version)); err != nil {
		t.Fatal(err)
	}
}

func userVersion(t *testing.T, s *Store) int {
	t.Helper()

	var version int
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	return version
}

func lookupIDs(pairs map[string]string) func(kind, name string) string {
	return func(kind, name string) string { return pairs[kind+"/"+name] }
}

func backupFiles(t *testing.T, path string) []string {
	t.Helper()

	files, err := filepath.Glob(path + ".v1.bak-*")
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func TestMigrateIdentitiesRewritesEveryColumn(t *testing.T) {
	s, path := openStoreAt(t)
	ctx := context.Background()
	base := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)

	ids := map[string]string{
		"provider/alpha":     "p_0000000001",
		"route/fast":         "r_0000000001",
		"node/n1":            "n_0000000001",
		"entrypoint/edge":    "e_0000000001",
		"gateway_key/client": "gk_0000000001",
	}
	for _, r := range []Request{
		// Names, as the previous release recorded them.
		{ID: "a1", TS: base, Provider: "alpha", Route: "fast", Node: "n1", GatewayKey: "client", Model: "m1", InputTokens: 1, OutputTokens: 2},
		{ID: "a2", TS: base.Add(time.Minute), Provider: "alpha", Route: "fast", Node: "n1", GatewayKey: "client", Model: "m1", InputTokens: 3, OutputTokens: 4},
		// Already an id: the id-aware gateway wrote this row after upgrading to
		// ids but before the database was migrated.
		{ID: "a3", TS: base.Add(2 * time.Minute), Provider: "p_0000000001", Model: "m1", InputTokens: 5, OutputTokens: 6},
		// Unrouted: no route and no node, but still attributed to a provider.
		{ID: "a4", TS: base.Add(3 * time.Minute), Provider: "alpha", Model: "m2", InputTokens: 7, OutputTokens: 8},
		// An id-keyed gateway key series in the same hour.
		{ID: "a5", TS: base.Add(4 * time.Minute), Provider: "alpha", Model: "m3", GatewayKey: "gk_0000000001"},
	} {
		if err := s.Insert(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.InsertProbe(ctx, ProbeResult{TS: base, Provider: "alpha", OK: true, Status: 200}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveTopologyPositions(ctx, map[string]Position{
		"provider:alpha":     {X: 1, Y: 2},
		"route:fast":         {X: 3, Y: 4},
		"node:n1":            {X: 5, Y: 6},
		"entrypoint:edge":    {X: 7, Y: 8},
		"node:custom-layout": {X: 9, Y: 10},
		"unrouted":           {X: 11, Y: 12},
	}); err != nil {
		t.Fatal(err)
	}

	forceVersion(t, s, 1)
	if err := s.MigrateIdentities(ctx, lookupIDs(ids)); err != nil {
		t.Fatal(err)
	}

	row, err := s.Get(ctx, "a1")
	if err != nil || row == nil {
		t.Fatalf("get a1 = %+v err=%v", row, err)
	}
	if row.Provider != ids["provider/alpha"] || row.Route != ids["route/fast"] ||
		row.Node != ids["node/n1"] || row.GatewayKey != ids["gateway_key/client"] {
		t.Fatalf("row a1 = %q/%q/%q/%q, want ids", row.Provider, row.Route, row.Node, row.GatewayKey)
	}
	unrouted, err := s.Get(ctx, "a4")
	if err != nil || unrouted == nil {
		t.Fatalf("get a4 = %+v err=%v", unrouted, err)
	}
	if unrouted.Provider != ids["provider/alpha"] || unrouted.Route != "" ||
		unrouted.Node != "" || unrouted.GatewayKey != "" {
		t.Fatalf("unrouted row = %q/%q/%q/%q, want provider id and empty references",
			unrouted.Provider, unrouted.Route, unrouted.Node, unrouted.GatewayKey)
	}

	usage, err := s.ProviderUsage(ctx, base, base.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(usage) != 1 {
		t.Fatalf("provider usage = %+v, want one series", usage)
	}
	want := ProviderUsage{Provider: ids["provider/alpha"], Requests: 5, InputTokens: 16, OutputTokens: 20}
	if usage[0] != want {
		t.Fatalf("provider usage = %+v, want %+v", usage[0], want)
	}
	// The v1 name series and the id series in the same bucket merged instead of
	// overwriting each other.
	rows, err := s.db.Query(`SELECT model, requests FROM rollups WHERE provider = ? ORDER BY model`, ids["provider/alpha"])
	if err != nil {
		t.Fatal(err)
	}
	merged := map[string]int64{}
	for rows.Next() {
		var model string
		var requests int64
		if err := rows.Scan(&model, &requests); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		merged[model] = requests
	}
	rows.Close()
	if len(merged) != 3 || merged["m1"] != 3 || merged["m2"] != 1 || merged["m3"] != 1 {
		t.Fatalf("rollups by model = %+v, want m1=3 m2=1 m3=1", merged)
	}

	keys, err := s.KeyUsage(ctx, base, base.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 || keys[0].Key != ids["gateway_key/client"] || keys[0].Requests != 3 {
		t.Fatalf("key usage = %+v, want one series for %q with 3 requests", keys, ids["gateway_key/client"])
	}

	probes, err := s.LatestProbes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := probes["alpha"]; ok {
		t.Fatalf("probes still keyed by the v1 name: %+v", probes)
	}
	if probe, ok := probes[ids["provider/alpha"]]; !ok || !probe.OK {
		t.Fatalf("probes = %+v, want the provider id", probes)
	}

	positions, err := s.LoadTopologyPositions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	customNode := identity.Derive(identity.Node, "custom-layout")
	wantPositions := map[string]Position{
		"provider:" + ids["provider/alpha"]:    {X: 1, Y: 2},
		"route:" + ids["route/fast"]:           {X: 3, Y: 4},
		"node:" + ids["node/n1"]:               {X: 5, Y: 6},
		"entrypoint:" + ids["entrypoint/edge"]: {X: 7, Y: 8},
		"node:" + customNode:                   {X: 9, Y: 10},
		"unrouted":                             {X: 11, Y: 12},
	}
	if len(positions) != len(wantPositions) {
		t.Fatalf("positions = %+v, want %+v", positions, wantPositions)
	}
	for id, want := range wantPositions {
		if got, ok := positions[id]; !ok || got != want {
			t.Fatalf("position %q = %+v (present %v), want %+v", id, got, ok, want)
		}
	}

	labels, err := s.Labels(ctx)
	if err != nil {
		t.Fatal(err)
	}
	wantLabels := map[string]map[string]string{
		"provider":    {ids["provider/alpha"]: "alpha"},
		"route":       {ids["route/fast"]: "fast"},
		"node":        {ids["node/n1"]: "n1", customNode: "custom-layout"},
		"entrypoint":  {ids["entrypoint/edge"]: "edge"},
		"gateway_key": {ids["gateway_key/client"]: "client"},
	}
	for kind, byID := range wantLabels {
		for id, name := range byID {
			if got := labels[kind][id]; got != name {
				t.Fatalf("%s label %q = %q, want %q (labels %+v)", kind, id, got, name, labels)
			}
		}
	}

	if got := userVersion(t, s); got != statsVersion {
		t.Fatalf("user_version = %d, want %d", got, statsVersion)
	}
	backups := backupFiles(t, path)
	if len(backups) != 1 {
		t.Fatalf("backups = %v, want exactly one", backups)
	}
	// The copy holds the database as it was before the rewrite.
	backup, err := sql.Open("sqlite", backups[0])
	if err != nil {
		t.Fatal(err)
	}
	defer backup.Close()
	var backupProvider string
	if err := backup.QueryRow(`SELECT provider FROM requests WHERE request_id = 'a1'`).Scan(&backupProvider); err != nil {
		t.Fatal(err)
	}
	if backupProvider != "alpha" {
		t.Fatalf("backup provider = %q, want the v1 name alpha", backupProvider)
	}
}

func TestMigrateIdentitiesUnknownNames(t *testing.T) {
	s, _ := openStoreAt(t)
	ctx := context.Background()
	base := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)

	for _, r := range []Request{
		{ID: "u1", TS: base, Provider: "ghost-a", Model: "m"},
		{ID: "u2", TS: base.Add(time.Minute), Provider: "ghost-b", Model: "m"},
	} {
		if err := s.Insert(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	forceVersion(t, s, 1)

	// A config that no longer knows these names must still get stable, distinct
	// ids so each keeps one continuous series and its historical label.
	if err := s.MigrateIdentities(ctx, func(kind, name string) string { return "" }); err != nil {
		t.Fatal(err)
	}

	wantA := identity.Derive(identity.Provider, "ghost-a")
	wantB := identity.Derive(identity.Provider, "ghost-b")
	if wantA == wantB {
		t.Fatalf("derived ids collide: %q", wantA)
	}
	rows, err := s.Query(ctx, Filter{})
	if err != nil {
		t.Fatal(err)
	}
	providers := make(map[string]string, len(rows))
	for _, r := range rows {
		providers[r.ID] = r.Provider
	}
	if providers["u1"] != wantA || providers["u2"] != wantB {
		t.Fatalf("providers = %+v, want u1=%q u2=%q", providers, wantA, wantB)
	}

	labels, err := s.Labels(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := labels["provider"][wantA]; got != "ghost-a" {
		t.Fatalf("ghost-a label = %q, want the historical name", got)
	}
	if got := labels["provider"][wantB]; got != "ghost-b" {
		t.Fatalf("ghost-b label = %q, want the historical name", got)
	}
}

func TestMigrateIdentitiesKeepsExistingIDsAndUnrouted(t *testing.T) {
	s, _ := openStoreAt(t)
	ctx := context.Background()
	base := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)

	// Rows the id-aware gateway wrote, plus the literal unrouted marker: a
	// rewrite here would split history from the ids already in use.
	if err := s.Insert(ctx, Request{
		ID: "k1", TS: base, Provider: "p_0000000002", Route: "unrouted",
		Node: "n_0000000002", GatewayKey: "gk_0000000002", Model: "m",
	}); err != nil {
		t.Fatal(err)
	}
	forceVersion(t, s, 1)

	lookup := func(kind, name string) string {
		return map[string]string{
			"provider/p_0000000002":     "p_0000000009",
			"node/n_0000000002":         "n_0000000009",
			"route/unrouted":            "r_0000000009",
			"gateway_key/gk_0000000002": "gk_0000000009",
		}[kind+"/"+name]
	}
	if err := s.MigrateIdentities(ctx, lookup); err != nil {
		t.Fatal(err)
	}

	row, err := s.Get(ctx, "k1")
	if err != nil || row == nil {
		t.Fatalf("get k1 = %+v err=%v", row, err)
	}
	if row.Provider != "p_0000000002" || row.Route != "unrouted" ||
		row.Node != "n_0000000002" || row.GatewayKey != "gk_0000000002" {
		t.Fatalf("row k1 = %q/%q/%q/%q, want the recorded ids and unrouted untouched",
			row.Provider, row.Route, row.Node, row.GatewayKey)
	}

	labels, err := s.Labels(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(labels) != 0 {
		t.Fatalf("labels = %+v, want none for untouched values", labels)
	}
}

func TestMigrateIdentitiesIsIdempotent(t *testing.T) {
	s, path := openStoreAt(t)
	ctx := context.Background()
	base := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)

	if err := s.Insert(ctx, Request{ID: "i1", TS: base, Provider: "alpha", Route: "fast", Model: "m"}); err != nil {
		t.Fatal(err)
	}
	forceVersion(t, s, 1)

	ids := map[string]string{"provider/alpha": "p_0000000001", "route/fast": "r_0000000001"}
	if err := s.MigrateIdentities(ctx, lookupIDs(ids)); err != nil {
		t.Fatal(err)
	}
	// A second call, as every reload makes, must not rewrite anything and must
	// not take a second backup.
	if err := s.MigrateIdentities(ctx, func(kind, name string) string {
		t.Fatalf("second migration resolved %s %q", kind, name)
		return ""
	}); err != nil {
		t.Fatal(err)
	}

	row, err := s.Get(ctx, "i1")
	if err != nil || row == nil {
		t.Fatalf("get i1 = %+v err=%v", row, err)
	}
	if row.Provider != ids["provider/alpha"] || row.Route != ids["route/fast"] {
		t.Fatalf("row i1 = %q/%q, want ids", row.Provider, row.Route)
	}
	if backups := backupFiles(t, path); len(backups) != 1 {
		t.Fatalf("backups = %v, want exactly one after two calls", backups)
	}
}

func TestMigrateIdentitiesSkipsFreshDatabase(t *testing.T) {
	s, path := openStoreAt(t)

	if got := userVersion(t, s); got != statsVersion {
		t.Fatalf("fresh user_version = %d, want %d", got, statsVersion)
	}
	if err := s.MigrateIdentities(context.Background(), func(kind, name string) string {
		t.Fatalf("fresh database resolved %s %q", kind, name)
		return ""
	}); err != nil {
		t.Fatal(err)
	}
	if backups := backupFiles(t, path); len(backups) != 0 {
		t.Fatalf("backups = %v, want none for a fresh database", backups)
	}
}

func TestRenameKeepsRollupsAndLabelsFollow(t *testing.T) {
	s, _ := openStoreAt(t)
	ctx := context.Background()
	base := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)

	for i := 0; i < 2; i++ {
		if err := s.Insert(ctx, Request{
			ID: fmt.Sprintf("x%d", i), TS: base.Add(time.Duration(i) * time.Minute),
			Provider: "alpha", Route: "fast", Model: "m",
		}); err != nil {
			t.Fatal(err)
		}
	}
	forceVersion(t, s, 1)

	ids := map[string]string{"provider/alpha": "p_0000000001", "route/fast": "r_0000000001"}
	if err := s.MigrateIdentities(ctx, lookupIDs(ids)); err != nil {
		t.Fatal(err)
	}
	if err := s.SyncLabels(ctx, map[string]map[string]string{
		"provider": {ids["provider/alpha"]: "alpha"},
		"route":    {ids["route/fast"]: "fast"},
	}); err != nil {
		t.Fatal(err)
	}

	// The admin renames the provider; the id, and therefore the history, stays.
	if err := s.SyncLabels(ctx, map[string]map[string]string{
		"provider": {ids["provider/alpha"]: "alpha-renamed"},
	}); err != nil {
		t.Fatal(err)
	}

	usage, err := s.ProviderUsage(ctx, base, base.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(usage) != 1 || usage[0].Provider != ids["provider/alpha"] || usage[0].Requests != 2 {
		t.Fatalf("provider usage = %+v, want one series for %q with 2 requests", usage, ids["provider/alpha"])
	}
	labels, err := s.Labels(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := labels["provider"][ids["provider/alpha"]]; got != "alpha-renamed" {
		t.Fatalf("provider label = %q, want alpha-renamed", got)
	}
	if got := labels["route"][ids["route/fast"]]; got != "fast" {
		t.Fatalf("route label = %q, want fast", got)
	}
}
