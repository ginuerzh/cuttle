package gateway

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	gwconfig "github.com/ginuerzh/cuttle/internal/config"
)

// probingUpstream is an upstream server that counts the requests it serves, so
// a test can tell which route handled a request and prove that no probe ever
// reached an upstream.
func probingUpstream(t *testing.T) (*httptest.Server, *atomic.Int64) {
	t.Helper()

	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, openAIChatResponse)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

// probeRouteFixture builds one provider, node and route per upstream URL, in
// order, and names them with the index-based fixture ids.
func probeRouteFixture(t *testing.T, upstreams ...string) ([]gwconfig.Provider, []gwconfig.Node, []gwconfig.Route) {
	t.Helper()

	providers := make([]gwconfig.Provider, len(upstreams))
	nodes := make([]gwconfig.Node, len(upstreams))
	routes := make([]gwconfig.Route, len(upstreams))
	for i, url := range upstreams {
		n := strconv.Itoa(i + 1)
		providers[i] = gwconfig.Provider{
			ID: "p_000000000" + n, Name: "p" + n, Type: "generic", Chat: url,
			Keys: []gwconfig.ProviderKey{{ID: "pk_000000000" + n, Name: "k" + n, Key: "sk-" + n}},
		}
		nodes[i] = gwconfig.Node{ID: "n_000000000" + n, Name: "n" + n, Provider: "p_000000000" + n, Model: "m1"}
		routes[i] = gwconfig.Route{ID: "r_000000000" + n, Name: "r" + n, Nodes: []string{"n_000000000" + n}}
	}
	return providers, nodes, routes
}

// waitBindingEligibility waits until entrypoint entrypointID has reported a
// probe verdict for binding index that matches eligible.
func waitBindingEligibility(t *testing.T, gw *Gateway, entrypointID string, index int, eligible bool) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for {
		for _, e := range gw.EntrypointHealth() {
			if e.ID != entrypointID || index >= len(e.Bindings) {
				continue
			}
			if b := e.Bindings[index]; b.Last != nil && b.Eligible == eligible {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("binding %s/%d never reported eligible=%t: %+v", entrypointID, index, eligible, gw.EntrypointHealth())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestEntrypointSwitchesRouteByProbe(t *testing.T) {
	flag := filepath.Join(t.TempDir(), "flag")
	first, firstHits := probingUpstream(t)
	second, secondHits := probingUpstream(t)

	providers, nodes, routes := probeRouteFixture(t, first.URL, second.URL)
	cfg := &gwconfig.Config{
		Gateway: gwconfig.Gateway{Listen: "127.0.0.1:0"},
		Entrypoints: []gwconfig.Entrypoint{{
			ID: "e_0000000001", Name: "all", Match: []string{"*"},
			Routes: []gwconfig.RouteBinding{
				{Route: "r_0000000001", Probe: &gwconfig.Probe{
					// Exit 0 means eligible: the flag's presence makes this
					// binding ineligible, which is what the switch test toggles.
					Command: "test ! -f " + strconv.Quote(flag),
					// The sub-second interval deliberately bypasses
					// config.Validate (minimum 1s) to keep the test fast.
					Interval: gwconfig.Duration(20 * time.Millisecond),
					Timeout:  gwconfig.Duration(time.Second),
				}},
				{Route: "r_0000000002"},
			},
		}},
		Providers: providers,
		Nodes:     nodes,
		Routes:    routes,
	}
	gw := startGateway(t, cfg)

	sent := 0
	send := func() {
		sent++
		if code := postModel(t, gw, "/v1/chat/completions", `{"model":"m1","messages":[]}`); code != http.StatusOK {
			t.Fatalf("status = %d", code)
		}
	}

	// The probe runs immediately on start; wait for its verdict so the first
	// request exercises the probe, not the not-yet-probed default.
	waitBindingEligibility(t, gw, "e_0000000001", 0, true)
	send()
	if got := firstHits.Load(); got != 1 {
		t.Fatalf("first upstream hits = %d, want the first binding to serve", got)
	}
	if got := secondHits.Load(); got != 0 {
		t.Fatalf("second upstream hits = %d, want none", got)
	}

	// Making the probe fail switches traffic to the probe-less second binding.
	if err := os.WriteFile(flag, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	waitBindingEligibility(t, gw, "e_0000000001", 0, false)

	before := firstHits.Load()
	deadline := time.Now().Add(2 * time.Second)
	for secondHits.Load() == 0 {
		send()
		if time.Now().After(deadline) {
			t.Fatalf("the second binding never served a request (first upstream hits %d)", firstHits.Load()-before)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if firstHits.Load() != before {
		t.Fatalf("the ineligible first binding still served %d requests", firstHits.Load()-before)
	}

	// Removing the flag brings traffic back to the first binding.
	if err := os.Remove(flag); err != nil {
		t.Fatal(err)
	}
	waitBindingEligibility(t, gw, "e_0000000001", 0, true)

	before = firstHits.Load()
	deadline = time.Now().Add(2 * time.Second)
	for firstHits.Load() == before {
		send()
		if time.Now().After(deadline) {
			t.Fatal("traffic never returned to the recovered first binding")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// A probe is a local command: every upstream hit must belong to a client
	// request this test sent.
	if got := firstHits.Load() + secondHits.Load(); got != int64(sent) {
		t.Fatalf("upstream hits = %d, want exactly the %d client requests: a probe reached an upstream", got, sent)
	}
	for _, r := range gw.RouteHealth() {
		for _, n := range r.Nodes {
			if n.Dead {
				t.Fatalf("node %s reports dead: the probe must not touch node markers", n.ID)
			}
		}
	}
}

// TestIneligibleBindingDoesNotFallThrough proves the outer hop group has no
// filter: an entrypoint whose bindings are all ineligible must still serve its
// own first binding instead of a later entrypoint's catch-all.
func TestIneligibleBindingDoesNotFallThrough(t *testing.T) {
	first, firstHits := probingUpstream(t)
	second, secondHits := probingUpstream(t)
	catchAll, catchAllHits := probingUpstream(t)

	providers, nodes, routes := probeRouteFixture(t, first.URL, second.URL, catchAll.URL)
	failing := func(route string) gwconfig.RouteBinding {
		return gwconfig.RouteBinding{Route: route, Probe: &gwconfig.Probe{
			Command:  "false",
			Interval: gwconfig.Duration(20 * time.Millisecond),
			Timeout:  gwconfig.Duration(time.Second),
		}}
	}
	cfg := &gwconfig.Config{
		Gateway: gwconfig.Gateway{Listen: "127.0.0.1:0"},
		Entrypoints: []gwconfig.Entrypoint{
			{
				ID: "e_0000000001", Name: "probed", Match: []string{"*"},
				Routes: []gwconfig.RouteBinding{failing("r_0000000001"), failing("r_0000000002")},
			},
			{
				ID: "e_0000000002", Name: "catch-all", Match: []string{"*"},
				Routes: []gwconfig.RouteBinding{{Route: "r_0000000003"}},
			},
		},
		Providers: providers,
		Nodes:     nodes,
		Routes:    routes,
	}
	gw := startGateway(t, cfg)

	waitBindingEligibility(t, gw, "e_0000000001", 0, false)
	waitBindingEligibility(t, gw, "e_0000000001", 1, false)

	if code := postModel(t, gw, "/v1/chat/completions", `{"model":"m1","messages":[]}`); code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if got := catchAllHits.Load(); got != 0 {
		t.Fatalf("the catch-all entrypoint served %d requests: an ineligible entrypoint fell through", got)
	}
	if got := firstHits.Load(); got != 1 {
		t.Fatalf("first binding hits = %d, want x's last-resort fallback", got)
	}
	if got := secondHits.Load(); got != 0 {
		t.Fatalf("second binding hits = %d, want none", got)
	}
}
