package gateway

import (
	"net/http"
	"testing"

	gwconfig "github.com/go-gost/llm-gw/internal/config"
	"github.com/go-gost/llm-gw/internal/provider"
)

func TestEntrypointOrderWins(t *testing.T) {
	strongSrv, strongHit := captureUpstream(t)
	fastSrv, fastHit := captureUpstream(t)

	cfg := matrixConfig(fastSrv.URL, provider.Chat)
	cfg.Nodes[0].Model = "fast-model"
	cfg.Providers = append(cfg.Providers, gwconfig.Provider{
		Name: "strong",
		Type: "generic",
		Chat: strongSrv.URL,
	})
	cfg.Nodes = append(cfg.Nodes, gwconfig.Node{
		Name:     "strong",
		Provider: "strong",
		Model:    "strong-model",
	})
	// The catch-all route is declared first on purpose: entrypoint order, not
	// route order, must decide which one wins.
	cfg.Routes = []gwconfig.Route{
		{Name: "default", Nodes: []string{"up"}},
		{Name: "specific", Nodes: []string{"strong"}},
	}
	cfg.Entrypoints = []gwconfig.Entrypoint{
		{Name: "specific", Match: []string{"gpt-5.5"}, Route: "specific"},
		{Name: "default", Match: []string{"*"}, Route: "default"},
	}
	gw := startGateway(t, cfg)

	reset := func() { strongHit.path, fastHit.path = "", "" }
	reset()
	if code := postModel(t, gw, "/v1/chat/completions", `{"model":"gpt-5.5","messages":[]}`); code != http.StatusOK {
		t.Fatalf("specific status = %d", code)
	}
	if strongHit.path == "" || fastHit.path != "" {
		t.Fatalf("specific model routed to wrong node: strong=%q fast=%q", strongHit.path, fastHit.path)
	}

	reset()
	if code := postModel(t, gw, "/v1/chat/completions", `{"model":"unknown-model","messages":[]}`); code != http.StatusOK {
		t.Fatalf("fallback status = %d", code)
	}
	if fastHit.path == "" || strongHit.path != "" {
		t.Fatalf("unmatched model routed to wrong node: strong=%q fast=%q", strongHit.path, fastHit.path)
	}
}

func TestEntrypointMultipleGlobs(t *testing.T) {
	up, hit := captureUpstream(t)
	cfg := matrixConfig(up.URL, provider.Chat)
	cfg.Nodes[0].Model = "mapped-model"
	cfg.Entrypoints = []gwconfig.Entrypoint{{
		Name:  "claude",
		Match: []string{"claude-*", "gpt-5*"},
		Route: "r",
	}}
	gw := startGateway(t, cfg)

	for _, model := range []string{"claude-opus-4-5", "gpt-5.5"} {
		hit.path, hit.body = "", ""
		if code := postModel(t, gw, "/v1/chat/completions", `{"model":"`+model+`","messages":[]}`); code != http.StatusOK {
			t.Fatalf("%s status = %d", model, code)
		}
		if hit.path == "" {
			t.Fatalf("%s did not reach the upstream", model)
		}
		if got := decodeBody(t, hit.body)["model"]; got != "mapped-model" {
			t.Fatalf("%s upstream model = %v, want mapped-model", model, got)
		}
	}
}

func TestUnmatchedModelIsUnrouted(t *testing.T) {
	up, hit := captureUpstream(t)
	cfg := matrixConfig(up.URL, provider.Chat)
	cfg.Entrypoints = []gwconfig.Entrypoint{{
		Name:  "only-gpt",
		Match: []string{"gpt-4.5*"},
		Route: "r",
	}}
	gw := startGateway(t, cfg)

	if code := postModel(t, gw, "/v1/chat/completions", `{"model":"unknown-model","messages":[]}`); code < 500 {
		t.Fatalf("unmatched model status = %d, want 5xx", code)
	}
	if hit.path != "" {
		t.Fatalf("unmatched model reached the upstream: %q", hit.path)
	}
}

func TestEmptyEntrypointsServesNothing(t *testing.T) {
	up, hit := captureUpstream(t)
	cfg := testConfig(up.URL)
	cfg.Entrypoints = nil
	gw := startGateway(t, cfg)

	if code := postModel(t, gw, "/v1/chat/completions", `{"model":"anything","messages":[]}`); code < 500 {
		t.Fatalf("status = %d, want 5xx without entrypoints", code)
	}
	if hit.path != "" {
		t.Fatalf("an empty entrypoint list reached the upstream: %q", hit.path)
	}
}

func TestNodeModelRequired(t *testing.T) {
	up, _ := captureUpstream(t)
	cfg := testConfig(up.URL)
	cfg.Nodes[0].Model = ""
	if _, err := New(cfg, nil); err == nil {
		t.Fatal("New accepted a node without a model")
	}
}

func TestRouteWithOnlyDisabledNodesIsUnrouted(t *testing.T) {
	up, hit := captureUpstream(t)
	cfg := testConfig(up.URL)
	off := false
	cfg.Providers[0].Enabled = &off
	gw := startGateway(t, cfg)

	if code := postModel(t, gw, "/v1/chat/completions", `{"model":"anything","messages":[]}`); code < 500 {
		t.Fatalf("status = %d, want 5xx when every node is disabled", code)
	}
	if hit.path != "" {
		t.Fatalf("a disabled provider reached the upstream: %q", hit.path)
	}
}
