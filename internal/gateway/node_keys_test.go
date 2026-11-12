package gateway

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	gwconfig "github.com/ginuerzh/cuttle/internal/config"
	"github.com/ginuerzh/cuttle/internal/identity"
)

// keyFixtureID is the id a fixture key is addressed by: keys are ids, names are
// labels.
func keyFixtureID(name string) string {
	return identity.Derive(identity.ProviderKey, name)
}

func keyNamed(name, value string) gwconfig.ProviderKey {
	return gwconfig.ProviderKey{ID: keyFixtureID(name), Name: name, Key: value}
}

func keyDisabled(name, value string) gwconfig.ProviderKey {
	disabled := false
	return gwconfig.ProviderKey{ID: keyFixtureID(name), Name: name, Key: value, Enabled: &disabled}
}

func TestResolveNodeKeysPinned(t *testing.T) {
	p := gwconfig.Provider{ID: "p_0000000001", Name: "p", Keys: []gwconfig.ProviderKey{
		{ID: "pk_0000000001", Name: "account-a", Key: "sk-a"},
		{ID: "pk_0000000002", Name: "account-b", Key: "sk-b"},
	}}
	n := gwconfig.Node{Name: "n", Provider: p.ID, Key: "pk_0000000002"}

	keys, err := resolveNodeKeys(p, n)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 || keys[0].ID != "pk_0000000002" {
		t.Fatalf("keys = %+v, want [pk_0000000002]", keys)
	}
}

func TestResolveNodeKeysUnpinnedSkipsDisabled(t *testing.T) {
	p := gwconfig.Provider{Name: "p", Keys: []gwconfig.ProviderKey{
		keyNamed("a", "sk-a"),
		keyDisabled("b", "sk-b"),
		keyNamed("c", "sk-c"),
	}}
	n := gwconfig.Node{Name: "n", Provider: "p"}

	keys, err := resolveNodeKeys(p, n)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 || keys[0].Name != "a" || keys[1].Name != "c" {
		t.Fatalf("keys = %+v, want [a c]", keys)
	}
}

func TestResolveNodeKeysAllDisabled(t *testing.T) {
	p := gwconfig.Provider{Name: "p", Keys: []gwconfig.ProviderKey{
		keyDisabled("a", "sk-a"),
		keyDisabled("b", "sk-b"),
	}}
	n := gwconfig.Node{Name: "n", Provider: "p"}

	if _, err := resolveNodeKeys(p, n); !errors.Is(err, errNodeKeyDisabled) {
		t.Fatalf("err = %v, want errNodeKeyDisabled", err)
	}
}

func TestResolveNodeKeysPinnedDisabled(t *testing.T) {
	p := gwconfig.Provider{ID: "p_0000000001", Name: "p", Keys: []gwconfig.ProviderKey{
		{ID: "pk_0000000001", Name: "a", Key: "sk-a"},
		{ID: "pk_0000000002", Name: "b", Key: "sk-b", Enabled: boolPtr(false)},
	}}
	n := gwconfig.Node{Name: "n", Provider: p.ID, Key: "pk_0000000002"}

	if _, err := resolveNodeKeys(p, n); !errors.Is(err, errNodeKeyDisabled) {
		t.Fatalf("err = %v, want errNodeKeyDisabled", err)
	}
}

func TestResolveNodeKeysPinnedMissing(t *testing.T) {
	p := gwconfig.Provider{ID: "p_0000000001", Name: "p", Keys: []gwconfig.ProviderKey{
		{ID: "pk_0000000001", Name: "a", Key: "sk-a"},
	}}
	n := gwconfig.Node{Name: "n", Provider: p.ID, Key: "pk_0000000009"}

	_, err := resolveNodeKeys(p, n)
	if err == nil || errors.Is(err, errNodeKeyDisabled) {
		t.Fatalf("err = %v, want a regular error naming the key", err)
	}
	if !strings.Contains(err.Error(), "pk_0000000009") {
		t.Fatalf("err = %v, want it to name the missing key", err)
	}
}

func TestResolveNodeKeysKeyless(t *testing.T) {
	p := gwconfig.Provider{Name: "p"}
	n := gwconfig.Node{Name: "n", Provider: "p"}

	keys, err := resolveNodeKeys(p, n)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 || keys[0].Key != "" || keys[0].Name != "" {
		t.Fatalf("keys = %+v, want one keyless entry", keys)
	}
}

func nodeKeyBuildConfig(upstreamURL string) *gwconfig.Config {
	return &gwconfig.Config{
		Gateway:     gwconfig.Gateway{Listen: "127.0.0.1:0"},
		Entrypoints: []gwconfig.Entrypoint{{ID: "e_0000000001", Name: "default", Match: []string{"*"}, Routes: bindings("r_0000000001")}},
		Providers: []gwconfig.Provider{{
			ID:   "p_0000000001",
			Name: "up",
			Type: "generic",
			Chat: upstreamURL,
			Keys: []gwconfig.ProviderKey{keyNamed("account-a", "sk-a"), keyNamed("account-b", "sk-b")},
			Auth: gwconfig.Auth{Header: "Authorization", Prefix: "Bearer "},
		}},
		Nodes: []gwconfig.Node{{
			ID:       "n_0000000001",
			Name:     "n",
			Provider: "p_0000000001",
			Model:    "m",
		}},
		Routes: []gwconfig.Route{{ID: "r_0000000001", Name: "default", Nodes: []string{"n_0000000001"}}},
	}
}

func TestBuildNodesSuffixUsesKeyID(t *testing.T) {
	p := gwconfig.Provider{ID: "p_0000000001", Name: "provider-display", Type: "generic", Chat: "https://example.com/v1"}
	n := gwconfig.Node{ID: "n_0000000001", Name: "node-display", Provider: p.ID, Model: "m"}
	target := upstreamTarget{
		baseURL:     "https://example.com/v1",
		api:         "chat",
		targetModel: "m",
		auth:        gwconfig.Auth{Header: "Authorization", Prefix: "Bearer "},
	}
	keys := []gwconfig.ProviderKey{
		{ID: "pk_0000000001", Name: "account-a", Key: "sk-a"},
		{ID: "pk_0000000002", Name: "account-b", Key: "sk-b"},
	}

	nodes, err := buildNodes(p, n, keys, gwconfig.Route{ID: "r_0000000001", Name: "route-display"}, target, "r/r_0000000001/default/n_0000000001")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"r_0000000001/p_0000000001/n_0000000001/pk_0000000001",
		"r_0000000001/p_0000000001/n_0000000001/pk_0000000002",
	}
	if len(nodes) != len(want) || nodes[0].Name != want[0] || nodes[1].Name != want[1] {
		t.Fatalf("node names = %q/%q, want %v", nodes[0].Name, nodes[1].Name, want)
	}
}

func TestBuildNodesSingleKeyHasNoSuffix(t *testing.T) {
	p := gwconfig.Provider{ID: "p_0000000001", Name: "provider-display", Type: "generic", Chat: "https://example.com/v1"}
	n := gwconfig.Node{ID: "n_0000000001", Name: "node-display", Provider: p.ID, Model: "m", Key: "pk_0000000001"}
	target := upstreamTarget{
		baseURL:     "https://example.com/v1",
		api:         "chat",
		targetModel: "m",
		auth:        gwconfig.Auth{Header: "Authorization", Prefix: "Bearer "},
	}

	nodes, err := buildNodes(p, n, []gwconfig.ProviderKey{{ID: "pk_0000000001", Name: "account-a", Key: "sk-a"}}, gwconfig.Route{ID: "r_0000000001", Name: "route-display"}, target, "r/r_0000000001/default/n_0000000001")
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 || nodes[0].Name != "r_0000000001/p_0000000001/n_0000000001" {
		t.Fatalf("node name = %q, want the route, provider and node ids", nodes[0].Name)
	}
	if got, _ := nodes[0].Metadata["configNode"].(string); got != n.ID {
		t.Fatalf("configNode = %q, want %q", got, n.ID)
	}
	if got, _ := nodes[0].Metadata["configRoute"].(string); got != "r_0000000001" {
		t.Fatalf("configRoute = %q, want the route id", got)
	}
}

type authLog struct {
	mu   sync.Mutex
	seen []string
}

func (l *authLog) add(v string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.seen = append(l.seen, v)
}

func (l *authLog) all() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.seen...)
}

func namedKeyUpstream(t *testing.T) (*httptest.Server, *authLog) {
	t.Helper()
	log := &authLog{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.add(r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, openAIChatResponse)
	}))
	t.Cleanup(srv.Close)
	return srv, log
}

func TestPinnedNodeUsesOnlyPinnedKey(t *testing.T) {
	up, log := namedKeyUpstream(t)
	cfg := nodeKeyBuildConfig(up.URL)
	cfg.Nodes[0].Key = keyFixtureID("account-b")
	gw := startGateway(t, cfg)

	if code := postModel(t, gw, "/v1/chat/completions", `{"model":"gpt-4.5","messages":[]}`); code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	got := log.all()
	if len(got) != 1 || got[0] != "Bearer sk-b" {
		t.Fatalf("auth = %v, want [Bearer sk-b]", got)
	}
}

func TestUnpinnedNodeRotatesKeys(t *testing.T) {
	up, log := namedKeyUpstream(t)
	cfg := nodeKeyBuildConfig(up.URL)
	cfg.Routes[0].Strategy = "round"
	gw := startGateway(t, cfg)

	for i := 0; i < 2; i++ {
		if code := postModel(t, gw, "/v1/chat/completions", `{"model":"gpt-4.5","messages":[]}`); code != http.StatusOK {
			t.Fatalf("request %d status = %d, want 200", i, code)
		}
	}
	got := log.all()
	if len(got) != 2 || got[0] == got[1] {
		t.Fatalf("auth = %v, want two different keys", got)
	}
	for _, v := range got {
		if v != "Bearer sk-a" && v != "Bearer sk-b" {
			t.Fatalf("unexpected auth %q", v)
		}
	}
}

func TestDisabledPinnedKeySkipsNode(t *testing.T) {
	up, hits := countingUpstream(t)
	cfg := nodeKeyBuildConfig(up.URL)
	cfg.Providers[0].Keys[0] = keyDisabled("account-a", "sk-a")
	cfg.Nodes[0].Key = keyFixtureID("account-a")
	gw := startGateway(t, cfg)

	if code := postModel(t, gw, "/v1/chat/completions", `{"model":"gpt-4.5","messages":[]}`); code < 500 {
		t.Fatalf("status = %d, want 5xx when the pinned key is disabled", code)
	}
	if got := hits.get(); got != 0 {
		t.Fatalf("upstream hits = %d, want 0", got)
	}
}
