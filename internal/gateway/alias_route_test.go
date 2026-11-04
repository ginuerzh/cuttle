package gateway

import (
	"net/http"
	"testing"

	gwconfig "github.com/go-gost/llm-gw/internal/config"
	"github.com/go-gost/llm-gw/internal/provider"
)

func TestRouteMatchesAliasUnion(t *testing.T) {
	strongSrv, strongHit := captureUpstream(t)
	fastSrv, fastHit := captureUpstream(t)

	cfg := matrixConfig(fastSrv.URL, provider.Chat)
	cfg.Nodes[0].Models = map[string]string{"*": "fast-model"}
	cfg.Providers = append(cfg.Providers, gwconfig.Provider{
		Name: "strong",
		Type: "generic",
		Chat: strongSrv.URL,
	})
	cfg.Nodes = append(cfg.Nodes, gwconfig.Node{
		Name:     "strong",
		Provider: "strong",
		Models:   map[string]string{"*": "strong-model"},
	})
	cfg.Routes = []gwconfig.Route{
		{Name: "strong", Nodes: []string{"strong"}, Match: gwconfig.Match{Model: "strong"}},
		{Name: "fast", Nodes: []string{"up"}, Match: gwconfig.Match{Model: "fast"}},
	}
	gw := startGateway(t, cfg)

	reset := func() { strongHit.path, fastHit.path = "", "" }

	for _, tc := range []struct {
		model string
		want  string
	}{
		{"claude-opus-4-5", "strong"},
		{"gpt-5.5", "strong"},
		{"gpt-4.5", "fast"},
		{"some-unknown-model", "fast"},
	} {
		reset()
		code := postModel(t, gw, "/v1/chat/completions", `{"model":"`+tc.model+`","messages":[]}`)
		if code < 200 || code >= 300 {
			t.Fatalf("%s: status = %d", tc.model, code)
		}
		got := "strong"
		if fastHit.path != "" {
			got = "fast"
		}
		if strongHit.path == "" && fastHit.path == "" {
			t.Fatalf("%s: neither upstream received the request", tc.model)
		}
		if got != tc.want {
			t.Fatalf("%s: routed to %s, want %s", tc.model, got, tc.want)
		}
	}
}

func TestRouteFallsBackToLiteralGlobWithoutAlias(t *testing.T) {
	up, hit := captureUpstream(t)
	cfg := matrixConfig(up.URL, provider.Chat)
	cfg.Aliases = nil
	cfg.Routes[0].Match = gwconfig.Match{Model: "gpt-4.5"}
	gw := startGateway(t, cfg)

	if code := postModel(t, gw, "/v1/chat/completions", `{"model":"gpt-4.5","messages":[]}`); code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if hit.path == "" {
		t.Fatal("literal glob route did not match")
	}

	if code := postModel(t, gw, "/v1/chat/completions", `{"model":"other","messages":[]}`); code < 500 {
		t.Fatalf("unmatched model status = %d, want 5xx", code)
	}
}
