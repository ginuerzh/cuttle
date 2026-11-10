package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"sync"
	"testing"

	corehop "github.com/go-gost/core/hop"
	xlogger "github.com/go-gost/x/logger"

	"github.com/ginuerzh/cuttle/internal/capture"
	gwconfig "github.com/ginuerzh/cuttle/internal/config"
	"github.com/ginuerzh/cuttle/internal/stats"
)

// idlessConfig is an admin-written config with no ids at all: what the data
// plane sees until Tasks 4-6 mint them.
func idlessConfig(upstreamA, upstreamB string) *gwconfig.Config {
	return &gwconfig.Config{
		Gateway: gwconfig.Gateway{Listen: "127.0.0.1:0"},
		Entrypoints: []gwconfig.Entrypoint{{
			Name: "default", Match: []string{"*"}, Route: "route",
		}},
		Providers: []gwconfig.Provider{
			{
				Name: "provider-a", Type: "generic", Chat: upstreamA,
				Keys: []gwconfig.ProviderKey{{Name: "key-a", Key: "sk-a"}},
			},
			{
				Name: "provider-b", Type: "generic", Chat: upstreamB,
				Keys: []gwconfig.ProviderKey{{Name: "key-b", Key: "sk-b"}},
			},
		},
		Nodes: []gwconfig.Node{
			{Name: "node-a", Provider: "provider-a", Model: "model-a"},
			{Name: "node-b", Provider: "provider-b", Model: "model-b"},
		},
		Routes: []gwconfig.Route{{
			Name: "route", Nodes: []string{"node-a", "node-b"}, Strategy: "round",
		}},
	}
}

// upstreamModelLog records the target model of every body an upstream sees.
type upstreamModelLog struct {
	mu     sync.Mutex
	models []string
}

func (l *upstreamModelLog) add(body []byte) {
	var req struct {
		Model string `json:"model"`
	}
	_ = json.Unmarshal(body, &req)
	l.mu.Lock()
	defer l.mu.Unlock()
	l.models = append(l.models, req.Model)
}

func (l *upstreamModelLog) all() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.models...)
}

func modelUpstream(t *testing.T) (*httptest.Server, *upstreamModelLog) {
	t.Helper()

	l := &upstreamModelLog{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		l.add(body)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, openAIChatResponse)
	}))
	t.Cleanup(srv.Close)
	return srv, l
}

// TestIdLessNodesKeepDistinctEngines pins the transition fallback that keeps an
// id-less config from collapsing: the x node names and the rewriter registry keys
// must stay distinct per node, and each node must keep its own target model.
// Before the fix every node of the route shared one name, so the last node's
// engine was registered last and every node converted to that node's model.
func TestIdLessNodesKeepDistinctEngines(t *testing.T) {
	upA, logA := modelUpstream(t)
	upB, logB := modelUpstream(t)
	cfg := idlessConfig(upA.URL, upB.URL)

	h, rewriters, _, err := buildHopGroup(cfg, "idless-build", xlogger.Nop(), stats.NewFacts(),
		capture.NewManager(capture.Policy{Mode: "off"}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { unregisterRewriters(rewriters) })

	want := map[string]string{
		"route/provider-a/node-a": buildRewriterName("idless-build", "route", "default", "node-a"),
		"route/provider-b/node-b": buildRewriterName("idless-build", "route", "default", "node-b"),
	}

	nl, ok := h.(corehop.NodeList)
	if !ok {
		t.Fatal("hop group does not expose its nodes")
	}
	seen := make(map[string]bool, len(want))
	for _, n := range nl.Nodes() {
		if _, ok := want[n.Name]; !ok {
			t.Fatalf("x node name = %q, want one of %v", n.Name, keysOf(want))
		}
		seen[n.Name] = true
	}
	if len(seen) != len(want) {
		t.Fatalf("x node names = %v, want one per node", keysOf(seen))
	}

	sort.Strings(rewriters)
	wantRewriters := []string{want["route/provider-a/node-a"], want["route/provider-b/node-b"]}
	sort.Strings(wantRewriters)
	if len(rewriters) != len(wantRewriters) {
		t.Fatalf("rewriters = %v, want %v", rewriters, wantRewriters)
	}
	for i := range wantRewriters {
		if rewriters[i] != wantRewriters[i] {
			t.Fatalf("rewriters = %v, want %v", rewriters, wantRewriters)
		}
	}

	// End to end: round selection visits both nodes, and each request must be
	// rewritten to the model of the node that served it.
	gw, err := New(cfg, xlogger.Nop())
	if err != nil {
		t.Fatal(err)
	}
	if err := gw.Start(); err != nil {
		t.Fatal(err)
	}
	defer gw.Close()

	for i := 0; i < 2; i++ {
		if code := postModel(t, gw, "/v1/chat/completions", `{"model":"client-model","messages":[]}`); code != http.StatusOK {
			t.Fatalf("request %d status = %d, want 200", i, code)
		}
	}
	if got := logA.all(); len(got) != 1 || got[0] != "model-a" {
		t.Fatalf("upstream a models = %v, want [model-a]", got)
	}
	if got := logB.all(); len(got) != 1 || got[0] != "model-b" {
		t.Fatalf("upstream b models = %v, want [model-b]", got)
	}
}

func keysOf[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
