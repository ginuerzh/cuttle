package gateway

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	xlogger "github.com/go-gost/x/logger"

	corehop "github.com/go-gost/core/hop"

	corechain "github.com/go-gost/core/chain"

	"github.com/ginuerzh/cuttle/internal/capture"
	gwconfig "github.com/ginuerzh/cuttle/internal/config"
	gwrewriter "github.com/ginuerzh/cuttle/internal/rewriter"
	"github.com/ginuerzh/cuttle/internal/stats"
)

// Fixture ids for the shared gateway config. Names are display labels, so every
// reference in a fixture config is one of these ids.
const (
	testProviderID    = "p_0000000001"
	testProviderKeyID = "pk_0000000001"
	testNodeID        = "n_0000000001"
	testRouteID       = "r_0000000001"
	testEntrypointID  = "e_0000000001"
	testGatewayKeyID  = "gk_0000000001"
)

func testConfig(upstreamURL string) *gwconfig.Config {
	return &gwconfig.Config{
		Gateway: gwconfig.Gateway{Listen: "127.0.0.1:0"},
		Entrypoints: []gwconfig.Entrypoint{{
			ID: testEntrypointID, Name: "default", Match: []string{"*"}, Routes: bindings(testRouteID),
		}},
		Providers: []gwconfig.Provider{{
			ID:   testProviderID,
			Name: "up",
			Type: "generic",
			Chat: upstreamURL,
			Keys: []gwconfig.ProviderKey{{ID: testProviderKeyID, Name: "key-1", Key: "sk-test"}},
		}},
		Nodes: []gwconfig.Node{{
			ID:       testNodeID,
			Name:     "up",
			Provider: testProviderID,
			Model:    "deepseek-chat",
		}},
		Routes: []gwconfig.Route{{
			ID:    testRouteID,
			Name:  "default",
			Nodes: []string{testNodeID},
		}},
	}
}

// bindings builds an entrypoint's ordered route bindings from route ids. The
// fixtures use probe-less bindings, which are always eligible.
func bindings(ids ...string) []gwconfig.RouteBinding {
	out := make([]gwconfig.RouteBinding, len(ids))
	for i, id := range ids {
		out[i] = gwconfig.RouteBinding{Route: id}
	}
	return out
}

func startGateway(t *testing.T, cfg *gwconfig.Config) *Gateway {
	t.Helper()

	gw, err := New(cfg, xlogger.Nop())
	if err != nil {
		t.Fatal(err)
	}
	if err := gw.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { gw.Close() })
	return gw
}

func TestRouteHealthUsesRouteFailSettings(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	cfg := testConfig(upstream.URL)
	cfg.Nodes = append(cfg.Nodes, gwconfig.Node{
		ID:       "n_0000000002",
		Name:     "up2",
		Provider: testProviderID,
		Model:    "deepseek-chat",
	})
	cfg.Routes[0].Nodes = []string{testNodeID, "n_0000000002"}
	cfg.Routes[0].Strategy = "fifo"
	cfg.Routes[0].MaxFails = 2
	cfg.Routes[0].FailTimeout = gwconfig.Duration(30 * time.Second)

	gw := startGateway(t, cfg)
	firstID, secondID := cfg.Nodes[0].ID, cfg.Nodes[1].ID
	nl, ok := gw.hop.(corehop.NodeList)
	if !ok {
		t.Fatal("hop does not expose nodes")
	}
	var first *corechain.Node
	for _, n := range nl.Nodes() {
		if configNodeID(n) == firstID {
			first = n
			break
		}
	}
	if first == nil {
		t.Fatalf("node %s not found", firstID)
	}

	first.Marker().Mark()
	hr := gw.RouteHealth()
	if len(hr) != 1 || len(hr[0].Nodes) != 2 {
		t.Fatalf("route health = %+v", hr)
	}
	if hr[0].MaxFails != 2 || hr[0].FailTimeout != "30s" {
		t.Fatalf("route settings = %+v", hr[0])
	}
	if hr[0].Nodes[0].Dead || hr[0].Nodes[0].FailCount != 1 {
		t.Fatalf("one failure should not be dead with maxFails=2: %+v", hr[0].Nodes[0])
	}
	if got := gw.hop.Select(context.Background()); got == nil || configNodeID(got) != firstID {
		t.Fatalf("selected = %+v, want %s before maxFails", got, firstID)
	}

	first.Marker().Mark()
	hr = gw.RouteHealth()
	if !hr[0].Nodes[0].Dead || hr[0].Nodes[0].DeadUntil == nil {
		t.Fatalf("two failures should be dead: %+v", hr[0].Nodes[0])
	}
	if got := gw.hop.Select(context.Background()); got == nil || configNodeID(got) != secondID {
		t.Fatalf("selected = %+v, want %s after maxFails", got, secondID)
	}
}

// TestBuildUsesEntityIDs proves the compiled hop group is named by entity id.
// A name is a display label an operator can change, so it may not reach x: the
// node name is what stats parses back into route, provider and node ids, and
// the metadata is what route health reads.
func TestBuildUsesEntityIDs(t *testing.T) {
	const (
		routeID    = "r_0000000001"
		providerID = "p_0000000001"
		nodeID     = "n_0000000001"
		keyAID     = "pk_0000000001"
		keyBID     = "pk_0000000002"
	)
	cfg := &gwconfig.Config{
		Entrypoints: []gwconfig.Entrypoint{{
			ID: "e_0000000001", Name: "entry-display", Match: []string{"*"}, Routes: bindings(routeID),
		}},
		Providers: []gwconfig.Provider{{
			ID: providerID, Name: "provider-display", Type: "generic",
			Chat: "https://upstream.test/v1",
			Keys: []gwconfig.ProviderKey{
				{ID: keyAID, Name: "key-a", Key: "sk-a"},
				{ID: keyBID, Name: "key-b", Key: "sk-b"},
			},
		}},
		Nodes: []gwconfig.Node{{
			ID: nodeID, Name: "node-display", Provider: providerID, Model: "m",
		}},
		Routes: []gwconfig.Route{{
			ID: routeID, Name: "route-display", Nodes: []string{nodeID},
		}},
	}

	h, rewriters, _, err := buildHopGroup(cfg, "ids-build", xlogger.Nop(), stats.NewFacts(),
		capture.NewManager(capture.Policy{Mode: "off"}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { unregisterRewriters(rewriters) })

	nl, ok := h.(corehop.NodeList)
	if !ok {
		t.Fatal("hop group does not expose its nodes")
	}
	var names []string
	for _, n := range nl.Nodes() {
		names = append(names, n.Name)
		for _, display := range []string{"route-display", "provider-display", "node-display", "key-a", "key-b"} {
			if strings.Contains(n.Name, display) {
				t.Fatalf("x node name %q contains the display name %q", n.Name, display)
			}
		}
		md := n.Metadata()
		if got, _ := md.Get("configNode").(string); got != nodeID {
			t.Fatalf("configNode = %q, want %q", got, nodeID)
		}
		if got, _ := md.Get("configRoute").(string); got != routeID {
			t.Fatalf("configRoute = %q, want %q", got, routeID)
		}
	}
	sort.Strings(names)
	want := []string{
		routeID + "/" + providerID + "/" + nodeID + "/" + keyAID,
		routeID + "/" + providerID + "/" + nodeID + "/" + keyBID,
	}
	if len(names) != len(want) || names[0] != want[0] || names[1] != want[1] {
		t.Fatalf("x node names = %v, want %v", names, want)
	}
}

// TestRouteHealthReportsIDs proves the health payload carries ids while the
// display names stay available for the console.
func TestRouteHealthReportsIDs(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	const (
		routeID    = "r_0000000001"
		providerID = "p_0000000001"
		nodeID     = "n_0000000001"
	)
	cfg := &gwconfig.Config{
		Gateway: gwconfig.Gateway{Listen: "127.0.0.1:0"},
		Entrypoints: []gwconfig.Entrypoint{{
			ID: "e_0000000001", Name: "default", Match: []string{"*"}, Routes: bindings(routeID),
		}},
		Providers: []gwconfig.Provider{{
			ID: providerID, Name: "provider-display", Type: "generic", Chat: upstream.URL,
			Keys: []gwconfig.ProviderKey{{ID: "pk_0000000001", Name: "key-1", Key: "sk-test"}},
		}},
		Nodes: []gwconfig.Node{{
			ID: nodeID, Name: "node-display", Provider: providerID, Model: "deepseek-chat",
		}},
		Routes: []gwconfig.Route{{
			ID: routeID, Name: "route-display", Nodes: []string{nodeID}, MaxFails: 2,
		}},
	}
	gw := startGateway(t, cfg)

	nl, ok := gw.hop.(corehop.NodeList)
	if !ok {
		t.Fatal("hop does not expose nodes")
	}
	marked := false
	for _, n := range nl.Nodes() {
		if configNodeID(n) == nodeID {
			n.Marker().Mark()
			marked = true
		}
	}
	if !marked {
		t.Fatalf("node %s not found in the hop group", nodeID)
	}

	hr := gw.RouteHealth()
	if len(hr) != 1 || len(hr[0].Nodes) != 1 {
		t.Fatalf("route health = %+v", hr)
	}
	if hr[0].ID != routeID || hr[0].Name != "route-display" {
		t.Fatalf("route health = %+v, want id %q and name %q", hr[0], routeID, "route-display")
	}
	node := hr[0].Nodes[0]
	if node.ID != nodeID || node.Name != "node-display" || node.ProviderID != providerID {
		t.Fatalf("node health = %+v, want id %q, name %q and provider id %q", node, nodeID, "node-display", providerID)
	}
	if node.FailCount != 1 || node.Dead {
		t.Fatalf("node health = %+v, want one recorded failure", node)
	}
}

// TestFailMarkerAccumulatesAcrossRequests locks in the x v0.31.0 marker
// semantics: a successful connect no longer clears a node's failure count, so
// consecutive failCodes responses reach maxFails, while the next non-failCodes
// response does clear it.
func TestFailMarkerAccumulatesAcrossRequests(t *testing.T) {
	var failing atomic.Bool
	failing.Store(true)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if failing.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	cfg := testConfig(upstream.URL)
	cfg.Routes[0].Strategy = "fifo"
	cfg.Routes[0].MaxFails = 2
	cfg.Routes[0].FailTimeout = gwconfig.Duration(30 * time.Second)

	gw := startGateway(t, cfg)

	post := func() int {
		t.Helper()
		resp, err := http.Post("http://"+gw.Addr().String()+"/v1/chat/completions",
			"application/json", strings.NewReader(`{"model":"deepseek-chat","messages":[]}`))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		io.Copy(io.Discard, resp.Body)
		return resp.StatusCode
	}

	for i := 0; i < 2; i++ {
		if got := post(); got != http.StatusInternalServerError {
			t.Fatalf("request %d status = %d, want 500", i+1, got)
		}
	}
	hr := gw.RouteHealth()
	if len(hr) != 1 || len(hr[0].Nodes) != 1 {
		t.Fatalf("route health = %+v", hr)
	}
	if got := hr[0].Nodes[0]; got.FailCount != 2 || !got.Dead {
		t.Fatalf("after two 500 responses = %+v, want failCount=2 and dead", got)
	}

	failing.Store(false)
	if got := post(); got != http.StatusOK {
		t.Fatalf("recovery status = %d, want 200", got)
	}
	hr = gw.RouteHealth()
	if got := hr[0].Nodes[0]; got.FailCount != 0 || got.Dead {
		t.Fatalf("after a 200 response = %+v, want failCount=0 and alive", got)
	}
}

func TestGatewayForwardsAndRewrites(t *testing.T) {
	var gotPath, gotQuery, gotAuth, gotBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		gotAuth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	gw := startGateway(t, testConfig(upstream.URL))

	req, _ := http.NewRequest(http.MethodPost,
		"http://"+gw.Addr().String()+"/v1/chat/completions?x=1",
		strings.NewReader(`{"model":"deepseek-chat","messages":[]}`))
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	if gotPath != "/chat/completions" {
		t.Fatalf("path = %q, want /chat/completions", gotPath)
	}
	if gotQuery != "x=1" {
		t.Fatalf("query = %q, want x=1", gotQuery)
	}
	if gotAuth != "Bearer sk-test" {
		t.Fatalf("authorization = %q, want Bearer sk-test", gotAuth)
	}
	if !strings.Contains(gotBody, `"model":"deepseek-chat"`) {
		t.Fatalf("body = %q, want the model unchanged", gotBody)
	}
	if !resp.Close {
		t.Fatal("response should close the connection (http.keepalive=false)")
	}
}

func TestGatewayPassesThroughUnmappedPathWithoutKey(t *testing.T) {
	var gotPath, gotAuth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	cfg := testConfig(upstream.URL)
	cfg.Providers[0].Keys = nil
	gw := startGateway(t, cfg)

	resp, err := http.Post("http://"+gw.Addr().String()+"/unknown", "application/json",
		strings.NewReader(`{"model":"deepseek-chat","messages":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	if gotPath != "/unknown" {
		t.Fatalf("path = %q, want /unknown (pass-through)", gotPath)
	}
	if gotAuth != "" {
		t.Fatalf("authorization = %q, want empty for a keyless provider", gotAuth)
	}
}

func TestGatewayRequestIDHeader(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	gw := startGateway(t, testConfig(upstream.URL))
	resp, err := http.Post("http://"+gw.Addr().String()+"/v1/chat/completions",
		"application/json", strings.NewReader(`{"model":"deepseek-chat","messages":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	if id := resp.Header.Get(gwrewriter.RequestIDHeaderName); id == "" {
		t.Fatalf("%s header is empty", gwrewriter.RequestIDHeaderName)
	}
}

func TestGatewayRequestIDMatchesStatsRow(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	cfg := testConfig(upstream.URL)
	cfg.Stats.Path = filepath.Join(t.TempDir(), "stats.sqlite")
	gw := startGateway(t, cfg)

	resp, err := http.Post("http://"+gw.Addr().String()+"/v1/chat/completions",
		"application/json", strings.NewReader(`{"model":"deepseek-chat","messages":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	id := resp.Header.Get(gwrewriter.RequestIDHeaderName)
	if id == "" {
		t.Fatal("request ID header is empty")
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		row, qerr := gw.store.Get(context.Background(), id)
		if qerr != nil {
			t.Fatal(qerr)
		}
		if row != nil {
			if row.ID != id {
				t.Fatalf("row ID = %q, header = %q", row.ID, id)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("stats row not recorded")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
