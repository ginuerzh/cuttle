package gateway

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	gwconfig "github.com/ginuerzh/cuttle/internal/config"
)

// hitCounter is a fake upstream that only counts requests. It returns a valid
// OpenAI chat completion so an accidentally routed request looks successful.
type hitCounter struct {
	mu   sync.Mutex
	hits int
}

func (c *hitCounter) get() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.hits
}

func countingUpstream(t *testing.T) (*httptest.Server, *hitCounter) {
	t.Helper()

	c := &hitCounter{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.mu.Lock()
		c.hits++
		c.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, openAIChatResponse)
	}))
	t.Cleanup(srv.Close)
	return srv, c
}

// disabledModelConfig routes every request to one node whose "*" mapping is
// the disabled model on the only provider.
func disabledModelConfig(upstreamURL string) *gwconfig.Config {
	return &gwconfig.Config{
		Gateway: gwconfig.Gateway{Listen: "127.0.0.1:0"},
		Providers: []gwconfig.Provider{{
			ID:   "p_0000000001",
			Name: "up",
			Type: "generic",
			Chat: upstreamURL,
			Keys: []gwconfig.ProviderKey{{ID: "pk_0000000001", Name: "key-1", Key: "sk-test"}},
			Models: map[string]gwconfig.ModelOverride{
				"disabled-model": {Disabled: true},
			},
		}},
		Entrypoints: []gwconfig.Entrypoint{{ID: "e_0000000001", Name: "default", Match: []string{"*"}, Routes: bindings("r_0000000001")}},
		Nodes: []gwconfig.Node{{
			ID:       "n_0000000001",
			Name:     "up",
			Provider: "p_0000000001",
			Model:    "disabled-model",
		}},
		Routes: []gwconfig.Route{{ID: "r_0000000001", Name: "default", Nodes: []string{"n_0000000001"}}},
	}
}

func TestDisabledTargetModelSkipsNode(t *testing.T) {
	upstream, hits := countingUpstream(t)
	gw := startGateway(t, disabledModelConfig(upstream.URL))

	code := postModel(t, gw, "/v1/chat/completions", `{"model":"gpt-4.5","messages":[]}`)
	if code < 500 {
		t.Fatalf("status = %d, want 5xx when the only node targets a disabled model", code)
	}
	if got := hits.get(); got != 0 {
		t.Fatalf("upstream hits = %d, want 0 (disabled target model must not receive traffic)", got)
	}
}

func TestDisabledTargetModelFallsBackToOtherNode(t *testing.T) {
	primarySrv, primaryHits := countingUpstream(t)
	backupSrv, backupHits := countingUpstream(t)

	cfg := &gwconfig.Config{
		Gateway: gwconfig.Gateway{Listen: "127.0.0.1:0"},
		Providers: []gwconfig.Provider{
			{
				ID:   "p_0000000001",
				Name: "primary",
				Type: "generic",
				Chat: primarySrv.URL,
				Models: map[string]gwconfig.ModelOverride{
					"disabled-model": {Disabled: true},
				},
			},
			{
				ID:   "p_0000000002",
				Name: "backup",
				Type: "generic",
				Chat: backupSrv.URL,
			},
		},
		Entrypoints: []gwconfig.Entrypoint{{ID: "e_0000000001", Name: "default", Match: []string{"*"}, Routes: bindings("r_0000000001")}},
		Nodes: []gwconfig.Node{
			{
				ID:       "n_0000000001",
				Name:     "primary",
				Provider: "p_0000000001",
				Model:    "disabled-model",
			},
			{
				ID:       "n_0000000002",
				Name:     "backup",
				Provider: "p_0000000002",
				Backup:   true,
				Model:    "enabled-model",
			},
		},
		Routes: []gwconfig.Route{{ID: "r_0000000001", Name: "default", Nodes: []string{"n_0000000001", "n_0000000002"}}},
	}
	gw := startGateway(t, cfg)

	code := postModel(t, gw, "/v1/chat/completions", `{"model":"gpt-4.5","messages":[]}`)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200 from the backup node", code)
	}
	if got := primaryHits.get(); got != 0 {
		t.Fatalf("primary upstream hits = %d, want 0 (node targets a disabled model)", got)
	}
	if got := backupHits.get(); got != 1 {
		t.Fatalf("backup upstream hits = %d, want 1", got)
	}
}

func TestDisabledTargetModelCanReload(t *testing.T) {
	upstream, hits := countingUpstream(t)

	// Unique route/node names keep the per-build rewriter list assertions
	// independent of the other gateway tests.
	const (
		routeName = "md-reload"
		nodeName  = "md-node"
	)
	enabled := disabledModelConfig(upstream.URL)
	enabled.Providers[0].Models = nil
	enabled.Nodes[0].Name = nodeName
	enabled.Routes[0].Name = routeName

	gw := startGateway(t, enabled)
	if len(gw.rewriters) == 0 {
		t.Fatal("no rewriters registered for an enabled target")
	}

	if code := postModel(t, gw, "/v1/chat/completions", `{"model":"gpt-4.5","messages":[]}`); code != http.StatusOK {
		t.Fatalf("status before reload = %d, want 200", code)
	}
	if got := hits.get(); got != 1 {
		t.Fatalf("upstream hits before reload = %d, want 1", got)
	}

	disabled := disabledModelConfig(upstream.URL)
	disabled.Nodes[0].Name = nodeName
	disabled.Routes[0].Name = routeName
	if err := gw.Reload(disabled); err != nil {
		t.Fatalf("reload with a disabled target model must succeed: %v", err)
	}
	if len(gw.rewriters) != 0 {
		t.Fatalf("new build registered rewriters for a disabled target: %v", gw.rewriters)
	}

	if code := postModel(t, gw, "/v1/chat/completions", `{"model":"gpt-4.5","messages":[]}`); code < 500 {
		t.Fatalf("status after reload = %d, want 5xx (route has no usable node)", code)
	}
	if got := hits.get(); got != 1 {
		t.Fatalf("upstream hits after reload = %d, want 1 (no traffic to a disabled target)", got)
	}
}
