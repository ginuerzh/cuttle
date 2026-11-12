package gateway

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/go-gost/core/hop"
	xhop "github.com/go-gost/x/hop"
	xlogger "github.com/go-gost/x/logger"

	gwconfig "github.com/ginuerzh/cuttle/internal/config"
)

// bindingFixture builds two routes over two capture upstreams, bound in order
// by one entrypoint.
func bindingFixture(t *testing.T) (*gwconfig.Config, *capturedRequest, *capturedRequest) {
	t.Helper()

	first, firstHit := captureUpstream(t)
	second, secondHit := captureUpstream(t)

	cfg := &gwconfig.Config{
		Gateway: gwconfig.Gateway{Listen: "127.0.0.1:0"},
		Entrypoints: []gwconfig.Entrypoint{{
			ID: "e_0000000001", Name: "all", Match: []string{"*"},
			Routes: bindings("r_0000000001", "r_0000000002"),
		}},
		Providers: []gwconfig.Provider{
			{
				ID: "p_0000000001", Name: "first", Type: "generic", Chat: first.URL,
				Keys: []gwconfig.ProviderKey{{ID: "pk_0000000001", Name: "k1", Key: "sk-1"}},
			},
			{
				ID: "p_0000000002", Name: "second", Type: "generic", Chat: second.URL,
				Keys: []gwconfig.ProviderKey{{ID: "pk_0000000002", Name: "k2", Key: "sk-2"}},
			},
		},
		Nodes: []gwconfig.Node{
			{ID: "n_0000000001", Name: "first", Provider: "p_0000000001", Model: "m1"},
			{ID: "n_0000000002", Name: "second", Provider: "p_0000000002", Model: "m1"},
		},
		Routes: []gwconfig.Route{
			{ID: "r_0000000001", Name: "first", Nodes: []string{"n_0000000001"}},
			{ID: "r_0000000002", Name: "second", Nodes: []string{"n_0000000002"}},
		},
	}
	return cfg, firstHit, secondHit
}

// bindingPlans compiles every fixture route into a plan keyed by route id.
func bindingPlans(t *testing.T, cfg *gwconfig.Config) map[string]*routePlan {
	t.Helper()

	log := xlogger.Nop()
	plans := make(map[string]*routePlan, len(cfg.Routes))
	for _, r := range cfg.Routes {
		plan, err := buildRoutePlan(cfg, "bindings-test", r, log, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		plans[r.ID] = plan
	}
	return plans
}

func bindingGroup(t *testing.T, entrypointID string, routes []gwconfig.RouteBinding, plans map[string]*routePlan) (hop.Hop, []*xhop.HopEntry) {
	t.Helper()

	h, entries, err := buildBindingsHop(entrypointID, routes, plans, "", bindingFailTimeout(routes), xlogger.Nop())
	if err != nil {
		t.Fatal(err)
	}
	return h, entries
}

func TestBindingOrderWins(t *testing.T) {
	cfg, firstHit, secondHit := bindingFixture(t)
	gw := startGateway(t, cfg)

	if code := postModel(t, gw, "/v1/chat/completions", `{"model":"m1","messages":[]}`); code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if firstHit.path == "" {
		t.Fatal("the first binding did not serve the request")
	}
	if secondHit.path != "" {
		t.Fatal("the second binding served a request the first one owns")
	}
}

func TestBindingFailFilterSkipsMarkedEntry(t *testing.T) {
	cfg, _, _ := bindingFixture(t)
	plans := bindingPlans(t, cfg)
	routes := cfg.Entrypoints[0].Routes

	h, entries := bindingGroup(t, cfg.Entrypoints[0].ID, routes, plans)
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want one per binding", len(entries))
	}

	entries[0].Marker().Mark()

	node := h.Select(context.Background())
	if got := configNodeID(node); got != "n_0000000002" {
		t.Fatalf("selected node = %q, want the second binding's node", got)
	}
}

func TestAllBindingsIneligibleFallsBackToFirst(t *testing.T) {
	cfg, _, _ := bindingFixture(t)
	plans := bindingPlans(t, cfg)
	routes := cfg.Entrypoints[0].Routes

	h, entries := bindingGroup(t, cfg.Entrypoints[0].ID, routes, plans)
	for _, e := range entries {
		e.Marker().Mark()
	}

	node := h.Select(context.Background())
	if node == nil {
		t.Fatal("select returned no node, want the first binding's node")
	}
	if got := configNodeID(node); got != "n_0000000001" {
		t.Fatalf("selected node = %q, want the first binding's node", got)
	}
}

func TestSingleBindingIgnoresMarker(t *testing.T) {
	cfg, _, _ := bindingFixture(t)
	plans := bindingPlans(t, cfg)
	routes := cfg.Entrypoints[0].Routes[:1]

	h, entries := bindingGroup(t, cfg.Entrypoints[0].ID, routes, plans)
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want one", len(entries))
	}

	entries[0].Marker().Mark()

	node := h.Select(context.Background())
	if got := configNodeID(node); got != "n_0000000001" {
		t.Fatalf("selected node = %q, want the only binding's node", got)
	}
}

func TestBindingFailTimeoutDerivation(t *testing.T) {
	cases := []struct {
		name   string
		routes []gwconfig.RouteBinding
		want   time.Duration
	}{
		{
			name:   "no probes",
			routes: bindings("r_0000000001", "r_0000000002"),
			want:   10 * time.Second,
		},
		{
			name: "longest interval doubled",
			routes: []gwconfig.RouteBinding{
				{Route: "r_0000000001", Probe: &gwconfig.Probe{Command: "true", Interval: gwconfig.Duration(5 * time.Second)}},
				{Route: "r_0000000002", Probe: &gwconfig.Probe{Command: "true", Interval: gwconfig.Duration(40 * time.Second)}},
			},
			want: 80 * time.Second,
		},
		{
			name: "below the floor",
			routes: []gwconfig.RouteBinding{
				{Route: "r_0000000001", Probe: &gwconfig.Probe{Command: "true", Interval: gwconfig.Duration(3 * time.Second)}},
			},
			want: 10 * time.Second,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := bindingFailTimeout(tc.routes); got != tc.want {
				t.Fatalf("bindingFailTimeout = %s, want %s", got, tc.want)
			}
		})
	}
}
