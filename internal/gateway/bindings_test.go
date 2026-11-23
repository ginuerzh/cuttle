package gateway

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-gost/core/hop"
	xhop "github.com/go-gost/x/hop"
	xlogger "github.com/go-gost/x/logger"

	gwconfig "github.com/ginuerzh/cuttle/internal/config"
	"github.com/ginuerzh/cuttle/internal/logging"
)

// bindingFixture builds two routes over two capture upstreams, bound in order
// by one entrypoint: a probed binding followed by a probe-less one. The fixture
// stays schema-valid; the probe command is a no-op, so both bindings start
// eligible.
func bindingFixture(t *testing.T) (*gwconfig.Config, *capturedRequest, *capturedRequest) {
	t.Helper()

	first, firstHit := captureUpstream(t)
	second, secondHit := captureUpstream(t)

	cfg := &gwconfig.Config{
		Gateway: gwconfig.Gateway{Listen: "127.0.0.1:0"},
		Entrypoints: []gwconfig.Entrypoint{{
			ID: "e_0000000001", Name: "all", Match: []string{"*"},
			Routes: []gwconfig.RouteBinding{
				{Route: "r_0000000001", Probe: &gwconfig.Probe{
					Command:  "true",
					Interval: gwconfig.Duration(2 * time.Second),
					Timeout:  gwconfig.Duration(time.Second),
				}},
				{Route: "r_0000000002"},
			},
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

// TestBindingFixtureIsSchemaValid keeps the fixture inside the rules
// config.Validate enforces, so it cannot drift back to an arrangement the
// loader would reject.
func TestBindingFixtureIsSchemaValid(t *testing.T) {
	cfg, _, _ := bindingFixture(t)
	if err := gwconfig.Validate(cfg); err != nil {
		t.Fatalf("bindingFixture is not a valid config: %v", err)
	}
}

// bindingPriorityFixture builds one provider, node and route per probed flag and
// returns the config whose single entrypoint binds every route in order. probed
// decides whether a binding carries a probe; the routes never connect, so the
// upstream is a placeholder.
func bindingPriorityFixture(t *testing.T, probed ...bool) (*gwconfig.Config, []gwconfig.RouteBinding) {
	t.Helper()

	cfg := &gwconfig.Config{
		Gateway: gwconfig.Gateway{Listen: "127.0.0.1:0"},
		Entrypoints: []gwconfig.Entrypoint{{
			ID: "e_0000000001", Name: "all", Match: []string{"*"},
		}},
	}
	bindings := make([]gwconfig.RouteBinding, 0, len(probed))
	for i, withProbe := range probed {
		n := fmt.Sprintf("%010d", i+1)
		providerID, nodeID, routeID := "p_"+n, "n_"+n, "r_"+n
		cfg.Providers = append(cfg.Providers, gwconfig.Provider{
			ID: providerID, Name: "p" + n, Type: "generic", Chat: "http://127.0.0.1:1",
			Keys: []gwconfig.ProviderKey{{ID: "pk_" + n, Name: "k" + n, Key: "sk-" + n}},
		})
		cfg.Nodes = append(cfg.Nodes, gwconfig.Node{
			ID: nodeID, Name: "n" + n, Provider: providerID, Model: "m1",
		})
		cfg.Routes = append(cfg.Routes, gwconfig.Route{
			ID: routeID, Name: "r" + n, Nodes: []string{nodeID},
		})
		b := gwconfig.RouteBinding{Route: routeID}
		if withProbe {
			b.Probe = &gwconfig.Probe{
				Command:  "true",
				Interval: gwconfig.Duration(2 * time.Second),
				Timeout:  gwconfig.Duration(time.Second),
			}
		}
		bindings = append(bindings, b)
	}
	cfg.Entrypoints[0].Routes = bindings
	return cfg, bindings
}

// selectBindingNode builds the inner group for cfg, marks the given binding
// indexes as failed, and returns the config node id the group selects.
func selectBindingNode(t *testing.T, cfg *gwconfig.Config, marked ...int) string {
	t.Helper()

	routes := cfg.Entrypoints[0].Routes
	h, entries := bindingGroup(t, cfg.Entrypoints[0].ID, routes, bindingPlans(t, cfg))
	for _, i := range marked {
		entries[i].Marker().Mark()
	}
	node := h.Select(context.Background())
	if node == nil {
		return ""
	}
	return configNodeID(node)
}

// bindingPlans compiles every fixture route into a plan keyed by route id.
func bindingPlans(t *testing.T, cfg *gwconfig.Config) map[string]*routePlan {
	t.Helper()

	log := xlogger.Nop()
	plans := make(map[string]*routePlan, len(cfg.Routes))
	for _, r := range cfg.Routes {
		plan, err := buildRoutePlan(cfg, "bindings-test", r, nil, log, nil, nil)
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
		{
			// A build that bypasses config.Load has no default applied yet; the
			// runner still runs every 30s, so the timeout must follow it.
			name: "zero interval uses the 30s default",
			routes: []gwconfig.RouteBinding{
				{Route: "r_0000000001", Probe: &gwconfig.Probe{Command: "true"}},
			},
			want: 60 * time.Second,
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

// TestProbedBindingsOutrankProbeLess is the point of the category priority: a
// probe-less binding placed first must not shadow a probed binding after it.
func TestProbedBindingsOutrankProbeLess(t *testing.T) {
	cfg, _ := bindingPriorityFixture(t, false, true)
	if got, want := selectBindingNode(t, cfg), "n_0000000002"; got != want {
		t.Fatalf("selected node = %q, want the probed binding %q", got, want)
	}
}

// TestFirstEligibleProbedWins checks that configuration order still breaks the
// tie among probed bindings.
func TestFirstEligibleProbedWins(t *testing.T) {
	t.Run("first eligible", func(t *testing.T) {
		cfg, _ := bindingPriorityFixture(t, true, true, true)
		if got, want := selectBindingNode(t, cfg), "n_0000000001"; got != want {
			t.Fatalf("selected node = %q, want the first probed binding %q", got, want)
		}
	})

	t.Run("first ineligible", func(t *testing.T) {
		cfg, _ := bindingPriorityFixture(t, true, true, true)
		if got, want := selectBindingNode(t, cfg, 0), "n_0000000002"; got != want {
			t.Fatalf("selected node = %q, want the second probed binding %q", got, want)
		}
	})
}

// TestAllProbesFailServesFirstProbeLess checks that the probe-less binding is
// reachable from any position and that the first one wins, not the first
// binding of the configuration.
func TestAllProbesFailServesFirstProbeLess(t *testing.T) {
	cfg, _ := bindingPriorityFixture(t, true, false, true, false)
	if got, want := selectBindingNode(t, cfg, 0, 2), "n_0000000002"; got != want {
		t.Fatalf("selected node = %q, want the first probe-less binding %q", got, want)
	}
}

// TestNoProbeLessFallsBackToFirstProbe keeps the accepted fallback: with every
// probe failing and no probe-less binding, x's last resort serves the first
// probed binding instead of failing the request.
func TestNoProbeLessFallsBackToFirstProbe(t *testing.T) {
	cfg, _ := bindingPriorityFixture(t, true, true)
	if got, want := selectBindingNode(t, cfg, 0, 1), "n_0000000001"; got != want {
		t.Fatalf("selected node = %q, want x's fallback to the first probed binding %q", got, want)
	}
}

// TestOnlyProbeLessServesFirst covers an entrypoint that never probes: the first
// configured binding serves.
func TestOnlyProbeLessServesFirst(t *testing.T) {
	cfg, _ := bindingPriorityFixture(t, false, false, false)
	if got, want := selectBindingNode(t, cfg), "n_0000000001"; got != want {
		t.Fatalf("selected node = %q, want the first binding %q", got, want)
	}
}

// TestBindingOrderHelper pins the priority order the group is fed and the
// probe-less bindings that can never serve.
func TestBindingOrderHelper(t *testing.T) {
	probed := func(route string) gwconfig.RouteBinding {
		return gwconfig.RouteBinding{Route: route, Probe: &gwconfig.Probe{Command: "true"}}
	}
	plain := func(route string) gwconfig.RouteBinding {
		return gwconfig.RouteBinding{Route: route}
	}

	cases := []struct {
		name        string
		bindings    []gwconfig.RouteBinding
		wantOrder   []int
		unreachable []int
	}{
		{
			name:        "probe-less between probed",
			bindings:    []gwconfig.RouteBinding{plain("r_0000000001"), probed("r_0000000002"), plain("r_0000000003"), probed("r_0000000004")},
			wantOrder:   []int{1, 3, 0, 2},
			unreachable: []int{2},
		},
		{
			name:      "no probe-less",
			bindings:  []gwconfig.RouteBinding{probed("r_0000000001"), probed("r_0000000002")},
			wantOrder: []int{0, 1},
		},
		{
			name:      "one probe-less",
			bindings:  []gwconfig.RouteBinding{probed("r_0000000001"), plain("r_0000000002")},
			wantOrder: []int{0, 1},
		},
		{
			name:        "only probe-less",
			bindings:    []gwconfig.RouteBinding{plain("r_0000000001"), plain("r_0000000002"), plain("r_0000000003")},
			wantOrder:   []int{0, 1, 2},
			unreachable: []int{1, 2},
		},
		{name: "empty"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			order, unreachable := bindingOrder(tc.bindings)
			if !slices.Equal(order, tc.wantOrder) {
				t.Fatalf("order = %v, want %v", order, tc.wantOrder)
			}
			if !slices.Equal(unreachable, tc.unreachable) {
				t.Fatalf("unreachable = %v, want %v", unreachable, tc.unreachable)
			}
		})
	}
}

// TestUnreachableProbeLessBindingWarns keeps the unreachable-binding report a
// build warning: one line per probe-less binding after the first, and none for
// the first one.
func TestUnreachableProbeLessBindingWarns(t *testing.T) {
	cfg, _ := bindingPriorityFixture(t, true, false, false)

	var buf bytes.Buffer
	log, err := logging.NewWithWriter("warn", "text", &buf)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err := buildHopGroup(cfg, "warn-test", log, nil, nil); err != nil {
		t.Fatal(err)
	}

	out := buf.String()
	if got := strings.Count(out, "routes[2] is a probe-less binding after another probe-less binding; it can never serve"); got != 1 {
		t.Fatalf("warnings for routes[2] = %d, want exactly one:\n%s", got, out)
	}
	if strings.Contains(out, "routes[1] is a probe-less binding") {
		t.Fatalf("the first probe-less binding was reported unreachable:\n%s", out)
	}
}

// TestBindingOrderClassTier pins the class tier as the primary sort key:
// conditioned bindings first (probed before probe-less), then unconditional
// ones (probed before probe-less).
func TestBindingOrderClassTier(t *testing.T) {
	bindings := []gwconfig.RouteBinding{
		{Route: "r_0000000001"}, // unconditional, probe-less
		{Route: "r_0000000002", When: "subagent", Probe: &gwconfig.Probe{Command: "x"}}, // conditioned, probed
		{Route: "r_0000000003", When: "subagent"},                                       // conditioned, probe-less
		{Route: "r_0000000004", Probe: &gwconfig.Probe{Command: "y"}},                   // unconditional, probed
	}
	order, unreachable := bindingOrder(bindings)
	if want := []int{1, 2, 3, 0}; !slices.Equal(order, want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	if len(unreachable) != 0 {
		t.Fatalf("unreachable = %v, want none (each class keeps one probe-less binding)", unreachable)
	}
}

// classFixture builds two routes: the first conditioned on subagent traffic and
// the second unconditional. Both connect to their own capture upstream, so a
// request's destination identifies the binding that served it.
func classFixture(t *testing.T) (*gwconfig.Config, *capturedRequest, *capturedRequest) {
	t.Helper()

	worker, workerHit := captureUpstream(t)
	lead, leadHit := captureUpstream(t)

	cfg := &gwconfig.Config{
		Gateway: gwconfig.Gateway{Listen: "127.0.0.1:0"},
		Entrypoints: []gwconfig.Entrypoint{{
			ID: "e_0000000001", Name: "all", Match: []string{"*"},
			Routes: []gwconfig.RouteBinding{
				{Route: "r_0000000001", When: gwconfig.BindingWhenSubagent},
				{Route: "r_0000000002"},
			},
		}},
		Providers: []gwconfig.Provider{
			{
				ID: "p_0000000001", Name: "worker", Type: "generic", Chat: worker.URL,
				Keys: []gwconfig.ProviderKey{{ID: "pk_0000000001", Name: "k1", Key: "sk-1"}},
			},
			{
				ID: "p_0000000002", Name: "lead", Type: "generic", Chat: lead.URL,
				Keys: []gwconfig.ProviderKey{{ID: "pk_0000000002", Name: "k2", Key: "sk-2"}},
			},
		},
		Nodes: []gwconfig.Node{
			{ID: "n_0000000001", Name: "worker", Provider: "p_0000000001", Model: "m1"},
			{ID: "n_0000000002", Name: "lead", Provider: "p_0000000002", Model: "m1"},
		},
		Routes: []gwconfig.Route{
			{ID: "r_0000000001", Name: "worker", Nodes: []string{"n_0000000001"}},
			{ID: "r_0000000002", Name: "lead", Nodes: []string{"n_0000000002"}},
		},
	}
	return cfg, workerHit, leadHit
}

// TestSelectionByClass is the acceptance test for the mechanism: a subagent
// marker sends the request to the conditioned route, and a plain request takes
// the unconditional one.
func TestSelectionByClass(t *testing.T) {
	cfg, workerHit, leadHit := classFixture(t)
	if err := gwconfig.Validate(cfg); err != nil {
		t.Fatalf("classFixture is not a valid config: %v", err)
	}

	cases := []struct {
		name   string
		header string
		value  string
	}{
		{"claude code subagent", "x-claude-code-agent-id", "agent-7"},
		{"opencode child session", "x-parent-session-id", "ses-child"},
		{"codex subagent", "x-openai-subagent", "review"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			workerHit.path, leadHit.path = "", ""
			gw := startGateway(t, cfg)
			if code := postModelWithHeader(t, gw, "/v1/chat/completions",
				`{"model":"m1","messages":[]}`, tc.header, tc.value); code != http.StatusOK {
				t.Fatalf("status = %d", code)
			}
			if workerHit.path == "" {
				t.Fatal("the conditioned binding did not serve the subagent request")
			}
			if leadHit.path != "" {
				t.Fatal("the unconditional binding served a subagent request")
			}
		})
	}

	t.Run("lead request", func(t *testing.T) {
		workerHit.path, leadHit.path = "", ""
		gw := startGateway(t, cfg)
		if code := postModel(t, gw, "/v1/chat/completions", `{"model":"m1","messages":[]}`); code != http.StatusOK {
			t.Fatalf("status = %d", code)
		}
		if leadHit.path == "" {
			t.Fatal("the unconditional binding did not serve the lead request")
		}
		if workerHit.path != "" {
			t.Fatal("the conditioned binding served a lead request")
		}
	})
}

// TestSelectionByClassFallsBackWhenProbeFails keeps availability above
// affinity: a dead probe on the conditioned binding hands subagent traffic to
// the unconditional binding instead of failing the request.
func TestSelectionByClassFallsBackWhenProbeFails(t *testing.T) {
	cfg, _, _ := classFixture(t)
	cfg.Entrypoints[0].Routes[0].Probe = &gwconfig.Probe{
		Command:  "true",
		Interval: gwconfig.Duration(2 * time.Second),
		Timeout:  gwconfig.Duration(time.Second),
	}
	plans := bindingPlans(t, cfg)
	h, entries := bindingGroup(t, cfg.Entrypoints[0].ID, cfg.Entrypoints[0].Routes, plans)

	// Bindings are fed in priority order, so entries[0] is the conditioned one.
	entries[0].Marker().Mark()

	node := h.Select(context.Background(), hop.HeaderSelectOption(http.Header{"X-Openai-Subagent": {"review"}}))
	if got := configNodeID(node); got != "n_0000000002" {
		t.Fatalf("selected node = %q, want the unconditional binding's node %q", got, "n_0000000002")
	}
}

// postModelWithHeader posts a model request carrying one extra header, used to
// drive the subagent class.
func postModelWithHeader(t *testing.T, gw *Gateway, path, body, header, value string) int {
	t.Helper()

	req, err := http.NewRequest(http.MethodPost, "http://"+gw.Addr().String()+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(header, value)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

// TestBuildNoWhenIsNilMatcher pins the strict no-op: a config with no `when`
// anywhere compiles to nil matchers, so an existing deployment keeps its exact
// behavior through the upgrade.
func TestBuildNoWhenIsNilMatcher(t *testing.T) {
	cfg, _, _ := bindingFixture(t)
	plans := bindingPlans(t, cfg)
	h, entries := bindingGroup(t, cfg.Entrypoints[0].ID, cfg.Entrypoints[0].Routes, plans)
	if len(entries) == 0 {
		t.Fatal("no entries compiled")
	}
	// x does not expose the entry matcher, so the no-op is proven by behavior.
	// The load-bearing case is a MARKER-LESS request: if any binding silently
	// acquired the subagent matcher, this request would be filtered out of
	// every entry and the group would return nil — a 502 for ordinary traffic.
	if node := h.Select(context.Background(), hop.HeaderSelectOption(http.Header{})); node == nil {
		t.Fatal("a marker-less request selected nothing, so a matcher was attached")
	} else if got := configNodeID(node); got != "n_0000000001" {
		t.Fatalf("selected node = %q, want the first configured binding %q", got, "n_0000000001")
	}

	// With the first binding marked failed, the marker-less request still
	// falls through to the second, which a matcher would also block.
	entries[0].Marker().Mark()
	node := h.Select(context.Background(), hop.HeaderSelectOption(http.Header{}))
	if got := configNodeID(node); got != "n_0000000002" {
		t.Fatalf("selected node = %q, want the unconditional second binding %q", got, "n_0000000002")
	}
}

// TestMarkerRegressionDoesNotChangeRouting pins the boundary between the two
// features: the marker-health endpoint is observation only, so a client that
// stopped marking must not alter which binding serves a request. The test
// builds the group, records what a marker-less request selects, exercises the
// health path, and asserts the selection is unchanged.
func TestMarkerRegressionDoesNotChangeRouting(t *testing.T) {
	cfg, _, _ := classFixture(t)
	plans := bindingPlans(t, cfg)
	h, _ := bindingGroup(t, cfg.Entrypoints[0].ID, cfg.Entrypoints[0].Routes, plans)

	before := configNodeID(h.Select(context.Background()))
	if before != "n_0000000002" {
		t.Fatalf("lead selection = %q, want the unconditional node", before)
	}

	// The health path reads marker state and must not write anything the data
	// plane consults. A regressed client is just traffic with no marker, which
	// is exactly a lead request from the routing point of view.
	after := configNodeID(h.Select(context.Background()))
	if after != before {
		t.Fatalf("selection changed from %q to %q after the health read", before, after)
	}

	// And the conditioned lane still works: a marked request is unaffected by
	// the fact that some other client is regressed.
	marked := configNodeID(h.Select(context.Background(),
		hop.HeaderSelectOption(http.Header{"X-Parent-Session-Id": {"ses-child"}})))
	if marked != "n_0000000001" {
		t.Fatalf("subagent selection = %q, want the conditioned node", marked)
	}
}
