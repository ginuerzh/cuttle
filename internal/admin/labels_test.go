package admin

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/ginuerzh/cuttle/internal/config"
	"github.com/ginuerzh/cuttle/internal/identity"
	"github.com/ginuerzh/cuttle/internal/stats"
)

// requestViewTest mirrors the documented JSON shape without depending on the
// implementation type, so a missing field fails as an empty value.
type requestViewTest struct {
	ID             string `json:"requestId"`
	Provider       string `json:"provider"`
	Node           string `json:"node"`
	Route          string `json:"route"`
	GatewayKey     string `json:"gatewayKey"`
	ProviderName   string `json:"providerName"`
	NodeName       string `json:"nodeName"`
	RouteName      string `json:"routeName"`
	GatewayKeyName string `json:"gatewayKeyName"`
}

func TestLabelSetPrefersConfigThenStoredLabels(t *testing.T) {
	s, rel, _, _ := newTestAdmin(t)

	providerID := identity.Derive(identity.Provider, "provider")
	storedProviderID := identity.Derive(identity.Provider, "stored-provider")
	unknownProviderID := identity.Derive(identity.Provider, "unknown-provider")
	nodeID := identity.Derive(identity.Node, "node")
	routeID := identity.Derive(identity.Route, "route")
	entrypointID := identity.Derive(identity.Entrypoint, "entrypoint")
	keyID := identity.Derive(identity.GatewayKey, "key")

	s.mu.Lock()
	s.cfg.Providers = []config.Provider{{ID: providerID, Name: "provider"}}
	s.cfg.Nodes = []config.Node{{ID: nodeID, Name: "node", Provider: providerID}}
	s.cfg.Routes = []config.Route{{ID: routeID, Name: "route", Nodes: []string{nodeID}}}
	s.cfg.Entrypoints = []config.Entrypoint{{ID: entrypointID, Name: "entrypoint", Route: routeID}}
	s.cfg.Gateway.Keys = []config.GatewayKey{{ID: keyID, Name: "key", Key: "sk-gw-secret"}}
	s.mu.Unlock()

	// A label the config no longer lists survives in the store, and a stale
	// label for a configured entity loses against the current name.
	if err := rel.store.SyncLabels(context.Background(), map[string]map[string]string{
		"provider":    {providerID: "stale-provider", storedProviderID: "stored-provider"},
		"node":        {nodeID: "stale-node"},
		"route":       {routeID: "stale-route"},
		"entrypoint":  {entrypointID: "stale-entrypoint"},
		"gateway_key": {keyID: "stale-key"},
	}); err != nil {
		t.Fatal(err)
	}

	l := s.newLabelSet(context.Background())
	if got := l.provider(providerID); got != "provider" {
		t.Fatalf("configured provider label = %q, want the current config name", got)
	}
	if got := l.provider(storedProviderID); got != "stored-provider" {
		t.Fatalf("deleted provider label = %q, want the stored label", got)
	}
	if got := l.provider(unknownProviderID); got != unknownProviderID {
		t.Fatalf("unlabelled provider = %q, want the raw id", got)
	}
	if got := l.node(nodeID); got != "node" {
		t.Fatalf("node label = %q, want node", got)
	}
	if got := l.route(routeID); got != "route" {
		t.Fatalf("route label = %q, want route", got)
	}
	if got := l.entrypoint(entrypointID); got != "entrypoint" {
		t.Fatalf("entrypoint label = %q, want entrypoint", got)
	}
	if got := l.gatewayKey(keyID); got != "key" {
		t.Fatalf("gateway key label = %q, want key", got)
	}
	if got := l.provider(""); got != "" {
		t.Fatalf("empty provider id = %q, want an empty label", got)
	}
}

func TestLabelSetWithoutStatsStore(t *testing.T) {
	s, _, _, _ := newTestAdmin(t)
	providerID := identity.Derive(identity.Provider, "provider")
	s.mu.Lock()
	s.cfg.Providers = []config.Provider{{ID: providerID, Name: "provider"}}
	s.mu.Unlock()
	s.stats = nil

	l := s.newLabelSet(context.Background())
	if got := l.provider(providerID); got != "provider" {
		t.Fatalf("provider label = %q, want the config name without a store", got)
	}
	if got := l.node("n_0000000001"); got != "n_0000000001" {
		t.Fatalf("node label = %q, want the raw id", got)
	}
}

func TestRequestViewsCarryLabels(t *testing.T) {
	s, rel, _, ts := newTestAdmin(t)
	now := time.Now().UTC()

	providerID := identity.Derive(identity.Provider, "provider-display")
	storedProviderID := identity.Derive(identity.Provider, "gone")
	unknownProviderID := identity.Derive(identity.Provider, "unknown")
	nodeID := identity.Derive(identity.Node, "node-display")
	routeID := identity.Derive(identity.Route, "route-display")
	keyID := identity.Derive(identity.GatewayKey, "key-display")

	s.mu.Lock()
	s.cfg.Providers = []config.Provider{{ID: providerID, Name: "provider-display", Type: "generic", Chat: "https://a.example"}}
	s.cfg.Nodes = []config.Node{{ID: nodeID, Name: "node-display", Provider: providerID, Model: "m1"}}
	s.cfg.Routes = []config.Route{{ID: routeID, Name: "route-display", Nodes: []string{nodeID}}}
	s.cfg.Gateway.Keys = []config.GatewayKey{{ID: keyID, Name: "key-display", Key: "sk-gw-secret"}}
	s.mu.Unlock()

	for _, row := range []stats.Request{
		{ID: "known", SID: "known", TS: now, Provider: providerID, Node: nodeID, Route: routeID, GatewayKey: keyID, Model: "m", Status: 200},
		{ID: "deleted", SID: "deleted", TS: now.Add(-time.Second), Provider: storedProviderID, Node: nodeID, Route: routeID, Model: "m", Status: 200},
		{ID: "unknown", SID: "unknown", TS: now.Add(-2 * time.Second), Provider: unknownProviderID, Model: "m", Status: 200},
	} {
		if err := rel.store.Insert(context.Background(), row); err != nil {
			t.Fatal(err)
		}
	}

	// The store learned the labels the config synced before it changed.
	if err := rel.store.SyncLabels(context.Background(), map[string]map[string]string{
		"provider":    {providerID: "stale-provider", storedProviderID: "gone-provider"},
		"node":        {nodeID: "stale-node"},
		"route":       {routeID: "stale-route"},
		"gateway_key": {keyID: "stale-key"},
	}); err != nil {
		t.Fatal(err)
	}

	client, _ := login(t, ts.URL)
	resp := doJSON(t, client, http.MethodGet, ts.URL+"/api/requests", "", "")
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d: %s", resp.StatusCode, body)
	}
	var rows []requestViewTest
	if err := json.Unmarshal(body, &rows); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	if len(rows) != 3 {
		t.Fatalf("rows = %+v, want three", rows)
	}
	byID := make(map[string]requestViewTest, len(rows))
	for _, row := range rows {
		byID[row.ID] = row
	}

	// The current config wins over the stored label.
	known := byID["known"]
	if known.Provider != providerID || known.ProviderName != "provider-display" {
		t.Fatalf("known provider = %q/%q, want the current config name", known.Provider, known.ProviderName)
	}
	if known.NodeName != "node-display" || known.RouteName != "route-display" || known.GatewayKeyName != "key-display" {
		t.Fatalf("known labels = %+v", known)
	}

	// A deleted entity keeps its last stored label.
	deleted := byID["deleted"]
	if deleted.ProviderName != "gone-provider" {
		t.Fatalf("deleted provider = %q, want the stored label", deleted.ProviderName)
	}
	if deleted.NodeName != "node-display" || deleted.RouteName != "route-display" {
		t.Fatalf("deleted labels = %+v, want the current node and route labels", deleted)
	}

	// An id without any label falls back to the raw id, and an absent
	// reference stays empty.
	unknown := byID["unknown"]
	if unknown.ProviderName != unknownProviderID {
		t.Fatalf("unknown provider = %q, want the raw id", unknown.ProviderName)
	}
	if unknown.NodeName != "" || unknown.RouteName != "" || unknown.GatewayKeyName != "" {
		t.Fatalf("unknown labels = %+v, want empty", unknown)
	}

	oneResp := doJSON(t, client, http.MethodGet, ts.URL+"/api/requests/known", "", "")
	oneBody, err := io.ReadAll(oneResp.Body)
	if err != nil {
		t.Fatal(err)
	}
	var one requestViewTest
	if err := json.Unmarshal(oneBody, &one); err != nil {
		t.Fatalf("decode %s: %v", oneBody, err)
	}
	if one.ID != "known" || one.ProviderName != "provider-display" || one.GatewayKeyName != "key-display" {
		t.Fatalf("single request = %+v, want the resolved labels", one)
	}
}
