package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	corerewriter "github.com/go-gost/core/rewriter"
	xlogger "github.com/go-gost/x/logger"
	"github.com/go-gost/x/registry"

	gwconfig "github.com/ginuerzh/cuttle/internal/config"
	"github.com/ginuerzh/cuttle/internal/stats"
)

// TestBuildCapReachesRegisteredRewriter proves that a build reads the target
// model's catalog output limit and installs it on the registered node rewriter.
func TestBuildCapReachesRegisteredRewriter(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			t.Errorf("catalog path = %q, want /models", r.URL.Path)
		}
		fmt.Fprint(w, `{"data":[{"id":"glm-5.3","limit":{"output":131072}}]}`)
	}))
	defer srv.Close()

	const (
		routeID    = "r_0000000001"
		providerID = "p_0000000001"
		nodeID     = "n_0000000001"
	)
	cfg := &gwconfig.Config{
		Gateway: gwconfig.Gateway{Listen: "127.0.0.1:0"},
		Entrypoints: []gwconfig.Entrypoint{{
			ID: "e_0000000001", Name: "opus", Match: []string{"claude-opus*"},
			Routes: []gwconfig.RouteBinding{{Route: routeID}},
		}},
		Providers: []gwconfig.Provider{{
			ID: providerID, Name: "catalog", Type: "generic", Chat: srv.URL,
			Keys: []gwconfig.ProviderKey{{ID: "pk_0000000001", Name: "k1", Key: "sk-1"}},
		}},
		Nodes: []gwconfig.Node{{
			ID: nodeID, Name: "glm", Provider: providerID, Model: "glm-5.3",
		}},
		Routes: []gwconfig.Route{{
			ID: routeID, Name: "capped", Nodes: []string{nodeID},
		}},
	}

	catalog := fetchCatalogInfo(context.Background(), cfg, xlogger.Nop())
	_, _, names, _, err := buildHopGroup(cfg, "cap-build", catalog, xlogger.Nop(), stats.NewFacts(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { unregisterRewriters(names) })

	name := buildRewriterName("cap-build", routeID, variantKey(""), nodeID)
	if !registry.RewriterRegistry().IsRegistered(name) {
		t.Fatalf("rewriter %q is not registered", name)
	}
	rw := registry.RewriterRegistry().Get(name)
	out, err := rw.Rewrite(context.Background(),
		[]byte(`{"model":"claude-opus-5-5","max_tokens":256000,"messages":[{"role":"user","content":"hi"}]}`),
		corerewriter.MetadataRewriteOption(map[string]any{"sid": "s1", "direction": "request", "uri": "/v1/chat/completions"}))
	if err != nil {
		t.Fatal(err)
	}
	if got := nestedInt(t, out, []string{"max_tokens"}); got != 131072 {
		t.Fatalf("max_tokens = %d, want 131072; body = %s", got, out)
	}
}

func TestBuildEffortCeilingReachesRegisteredRewriter(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"data":[{"id":"glm-5.3","limit":{"output":131072},"capabilities":{"reasoning":{"supported":true,"effort":["low","high","xhigh"]}}}]}`)
	}))
	defer srv.Close()

	const (
		routeID    = "r_0000000001"
		providerID = "p_0000000001"
		nodeID     = "n_0000000001"
	)
	cfg := &gwconfig.Config{
		Gateway: gwconfig.Gateway{Listen: "127.0.0.1:0"},
		Entrypoints: []gwconfig.Entrypoint{{
			ID: "e_0000000001", Name: "opus", Match: []string{"claude-opus*"},
			Routes: []gwconfig.RouteBinding{{Route: routeID}},
		}},
		Providers: []gwconfig.Provider{{
			ID: providerID, Name: "catalog", Type: "generic", Chat: srv.URL,
			Keys: []gwconfig.ProviderKey{{ID: "pk_0000000001", Name: "k1", Key: "sk-1"}},
		}},
		Nodes: []gwconfig.Node{{
			ID: nodeID, Name: "glm", Provider: providerID, Model: "glm-5.3",
		}},
		Routes: []gwconfig.Route{{
			ID: routeID, Name: "capped", Nodes: []string{nodeID},
		}},
	}

	info := fetchCatalogInfo(context.Background(), cfg, xlogger.Nop())
	_, _, names, _, err := buildHopGroup(cfg, "effort-build", info, xlogger.Nop(), stats.NewFacts(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { unregisterRewriters(names) })

	rw := registry.RewriterRegistry().Get(buildRewriterName("effort-build", routeID, variantKey(""), nodeID))
	out, err := rw.Rewrite(context.Background(),
		[]byte(`{"model":"claude-opus-5-5","reasoning_effort":"max","messages":[{"role":"user","content":"hi"}]}`),
		corerewriter.MetadataRewriteOption(map[string]any{"sid": "s1", "direction": "request", "uri": "/v1/chat/completions"}))
	if err != nil {
		t.Fatal(err)
	}
	if got := nestedString(t, out, []string{"reasoning_effort"}); got != "xhigh" {
		t.Fatalf("reasoning_effort = %q, want xhigh; body = %s", got, out)
	}
}

// TestGatewayUsesCachedCatalogLimits proves the production Gateway passes the
// catalog cache into the build instead of only making reload faster.
func TestGatewayUsesCachedCatalogLimits(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"data":[{"id":"glm-5.3","limit":{"output":131072}}]}`)
	}))
	t.Cleanup(srv.Close)

	const (
		routeID    = "r_0000000001"
		providerID = "p_0000000001"
		nodeID     = "n_0000000001"
	)
	cfg := &gwconfig.Config{
		Gateway: gwconfig.Gateway{Listen: "127.0.0.1:0"},
		Entrypoints: []gwconfig.Entrypoint{{
			ID: "e_0000000001", Name: "opus", Match: []string{"claude-opus*"},
			Routes: []gwconfig.RouteBinding{{Route: routeID}},
		}},
		Providers: []gwconfig.Provider{{
			ID: providerID, Name: "catalog", Type: "generic", Chat: srv.URL,
			Keys: []gwconfig.ProviderKey{{ID: "pk_0000000001", Name: "k1", Key: "sk-1"}},
		}},
		Nodes: []gwconfig.Node{{
			ID: nodeID, Name: "glm", Provider: providerID, Model: "glm-5.3",
		}},
		Routes: []gwconfig.Route{{
			ID: routeID, Name: "capped", Nodes: []string{nodeID},
		}},
	}
	gw := startGateway(t, cfg)
	if len(gw.rewriters) == 0 {
		t.Fatal("gateway registered no rewriters")
	}
	rw := registry.RewriterRegistry().Get(gw.rewriters[0])
	if rw == nil {
		t.Fatalf("rewriter %q is not registered", gw.rewriters[0])
	}
	out, err := rw.Rewrite(context.Background(),
		[]byte(`{"model":"claude-opus-5-5","max_tokens":256000,"messages":[{"role":"user","content":"hi"}]}`),
		corerewriter.MetadataRewriteOption(map[string]any{"sid": "s1", "direction": "request", "uri": "/v1/chat/completions"}))
	if err != nil {
		t.Fatal(err)
	}
	if got := nestedInt(t, out, []string{"max_tokens"}); got != 131072 {
		t.Fatalf("max_tokens = %d, want 131072; body = %s", got, out)
	}
}

func nestedString(t *testing.T, body []byte, path []string) string {
	t.Helper()
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	for _, key := range path {
		obj, ok := v.(map[string]any)
		if !ok {
			t.Fatalf("body %s: %q is not an object", body, key)
		}
		v, ok = obj[key]
		if !ok {
			t.Fatalf("body %s: field %q missing", body, key)
		}
	}
	s, ok := v.(string)
	if !ok {
		t.Fatalf("body %s: field %v = %v, want string", body, path, v)
	}
	return s
}

func nestedInt(t *testing.T, body []byte, path []string) int64 {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	for _, key := range path {
		obj, ok := v.(map[string]any)
		if !ok {
			t.Fatalf("body %s: %q is not an object", body, key)
		}
		v, ok = obj[key]
		if !ok {
			t.Fatalf("body %s: field %q missing", body, key)
		}
	}
	num, ok := v.(json.Number)
	if !ok {
		t.Fatalf("body %s: field %v = %v, want integer", body, path, v)
	}
	n, err := num.Int64()
	if err != nil {
		t.Fatalf("body %s: field %v = %v, want integer: %v", body, path, num, err)
	}
	return n
}
