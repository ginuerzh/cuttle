package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"
	"time"

	"github.com/ginuerzh/cuttle/internal/config"
	"github.com/ginuerzh/cuttle/internal/stats"
)

func providerBody(name, baseURL string) string {
	return `{"name":"` + name + `","type":"generic","chat":"` + baseURL + `"}`
}

func TestChangePassword(t *testing.T) {
	_, _, path, ts := newTestAdmin(t)
	client, csrf := login(t, ts.URL)

	if resp := doJSON(t, client, http.MethodPut, ts.URL+"/api/admin/password",
		`{"oldPassword":"pass","newPassword":"newpass123"}`, csrf); resp.StatusCode != http.StatusNoContent {
		b, _ := readAll(resp.Body)
		t.Fatalf("change password = %d: %s", resp.StatusCode, b)
	}

	if resp := doJSON(t, client, http.MethodGet, ts.URL+"/api/config", "", ""); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("old session status = %d, want 401", resp.StatusCode)
	}

	if resp := doJSON(t, &http.Client{}, http.MethodPost, ts.URL+"/api/login", `{"password":"pass"}`, ""); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("old password login = %d, want 401", resp.StatusCode)
	}
	loginWith(t, ts.URL, "newpass123")

	saved, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !config.VerifyPassword(saved.Admin.PasswordHash, "newpass123") {
		t.Fatal("saved config does not contain the new hash")
	}
}

func TestProbeOnce(t *testing.T) {
	srv, rel, _, ts := newTestAdmin(t)
	client, csrf := login(t, ts.URL)

	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer up.Close()

	body := `{"name":"p","type":"generic","chat":"` + up.URL + `","health":{"path":"/health","interval":"30s","timeout":"2s"}}`
	if resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/providers", body, csrf); resp.StatusCode != http.StatusCreated {
		t.Fatalf("create provider = %d", resp.StatusCode)
	}
	pid := providerID(t, client, ts.URL, "p")

	srv.ProbeOnce(context.Background())

	srv.mu.Lock()
	res, ok := srv.probes[pid]
	srv.mu.Unlock()
	if !ok || !res.OK || res.Status != 200 {
		t.Fatalf("probe = %+v", res)
	}
	latest, err := rel.store.LatestProbes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := latest[pid]; !ok || !got.OK {
		t.Fatalf("persisted probe = %+v, want the provider id %q", latest, pid)
	}
}

func TestGraphAPI(t *testing.T) {
	srv, rel, _, ts := newTestAdmin(t)
	client, _ := login(t, ts.URL)

	const (
		providerID = "p_3k9x2m4q7a"
		nodeID     = "n_4q7a9x2m3k"
		routeID    = "r_f3k9x2m4qa"
	)
	srv.mu.Lock()
	srv.cfg.Routes = []config.Route{{ID: routeID, Name: "r", Nodes: []string{nodeID}}}
	srv.cfg.Nodes = []config.Node{{ID: nodeID, Name: "n", Provider: providerID, Model: "m1"}}
	srv.mu.Unlock()

	base := time.Now().Add(-time.Hour)
	for _, row := range []stats.Request{
		{ID: "g1", TS: base, Route: routeID, Node: nodeID, Provider: providerID, Status: 200, DurationMS: 10, InputTokens: 4},
		{ID: "g2", TS: base.Add(time.Minute), Route: routeID, Node: nodeID, Provider: providerID, Status: 500, Error: "boom", DurationMS: 20},
	} {
		if err := rel.store.Insert(context.Background(), row); err != nil {
			t.Fatal(err)
		}
	}

	resp := doJSON(t, client, http.MethodGet, ts.URL+"/api/stats/graph", "", "")
	var g stats.GraphMetrics
	if err := jsonDecode(resp.Body, &g); err != nil {
		t.Fatal(err)
	}
	if g.Nodes["provider:"+providerID].Requests != 2 || g.Nodes["route:"+routeID].Errors != 1 ||
		g.Nodes["node:"+nodeID].Requests != 2 {
		t.Fatalf("graph = %+v", g)
	}
	routeEdge := "route:" + routeID + "|node:" + nodeID
	if g.Edges[routeEdge].Requests != 2 || g.Edges[routeEdge].Errors != 1 {
		t.Fatalf("route edge = %+v", g.Edges)
	}
	if g.Edges["node:"+nodeID+"|provider:"+providerID].Requests != 2 {
		t.Fatalf("provider edge = %+v", g.Edges)
	}
	if len(g.Errors) != 1 {
		t.Fatalf("errors = %+v", g.Errors)
	}
}

// helpers

func readAll(r interface{ Read([]byte) (int, error) }) ([]byte, error) {
	buf := make([]byte, 4096)
	n, err := r.Read(buf)
	return buf[:n], err
}

func loginWith(t *testing.T, tsURL, password string) {
	t.Helper()
	resp := doJSON(t, &http.Client{}, http.MethodPost, tsURL+"/api/login", `{"password":"`+password+`"}`, "")
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("login %q = %d", password, resp.StatusCode)
	}
}

var _ = sort.Strings

func TestGraphEntrypointAPI(t *testing.T) {
	srv, rel, _, ts := newTestAdmin(t)
	client, _ := login(t, ts.URL)

	const (
		providerID = "p_3k9x2m4q7a"
		nodeID     = "n_4q7a9x2m3k"
		routeID    = "r_f3k9x2m4qa"
	)
	srv.mu.Lock()
	srv.cfg.Entrypoints = []config.Entrypoint{{ID: "e_3k9x2m4q7a", Name: "claude", Match: []string{"claude*"}, Route: routeID}}
	srv.cfg.Routes = []config.Route{{ID: routeID, Name: "r", Nodes: []string{nodeID}}}
	srv.cfg.Nodes = []config.Node{{ID: nodeID, Name: "n", Provider: providerID, Model: "m1"}}
	srv.mu.Unlock()

	base := time.Now().Add(-time.Hour)
	if err := rel.store.Insert(context.Background(), stats.Request{
		ID: "a1", TS: base, ClientModel: "claude-sonnet-4-5", Route: routeID, Node: nodeID, Provider: providerID, Status: 200, DurationMS: 10,
	}); err != nil {
		t.Fatal(err)
	}

	resp := doJSON(t, client, http.MethodGet, ts.URL+"/api/stats/graph", "", "")
	var g stats.GraphMetrics
	if err := jsonDecode(resp.Body, &g); err != nil {
		t.Fatal(err)
	}
	if got := g.Nodes["entrypoint:e_3k9x2m4q7a"]; got.Requests != 1 {
		t.Fatalf("alias node = %+v", got)
	}
	if got := g.Edges["entrypoint:e_3k9x2m4q7a->route:"+routeID]; got.Requests != 1 {
		t.Fatalf("alias edge = %+v", got)
	}
}
