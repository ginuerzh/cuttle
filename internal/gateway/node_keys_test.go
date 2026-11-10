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
)

func keyNamed(name, value string) gwconfig.ProviderKey {
	return gwconfig.ProviderKey{Name: name, Key: value}
}

func keyDisabled(name, value string) gwconfig.ProviderKey {
	disabled := false
	return gwconfig.ProviderKey{Name: name, Key: value, Enabled: &disabled}
}

func TestResolveNodeKeysPinned(t *testing.T) {
	p := gwconfig.Provider{Name: "p", Keys: []gwconfig.ProviderKey{keyNamed("account-a", "sk-a"), keyNamed("account-b", "sk-b")}}
	n := gwconfig.Node{Name: "n", Provider: "p", Key: "account-a"}

	keys, err := resolveNodeKeys(p, n)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 || keys[0].Name != "account-a" {
		t.Fatalf("keys = %+v, want [account-a]", keys)
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
	p := gwconfig.Provider{Name: "p", Keys: []gwconfig.ProviderKey{
		keyNamed("a", "sk-a"),
		keyDisabled("b", "sk-b"),
	}}
	n := gwconfig.Node{Name: "n", Provider: "p", Key: "b"}

	if _, err := resolveNodeKeys(p, n); !errors.Is(err, errNodeKeyDisabled) {
		t.Fatalf("err = %v, want errNodeKeyDisabled", err)
	}
}

func TestResolveNodeKeysPinnedMissing(t *testing.T) {
	p := gwconfig.Provider{Name: "p", Keys: []gwconfig.ProviderKey{keyNamed("a", "sk-a")}}
	n := gwconfig.Node{Name: "n", Provider: "p", Key: "nope"}

	_, err := resolveNodeKeys(p, n)
	if err == nil || errors.Is(err, errNodeKeyDisabled) {
		t.Fatalf("err = %v, want a regular error naming the key", err)
	}
	if !strings.Contains(err.Error(), "nope") {
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
		Entrypoints: []gwconfig.Entrypoint{{Name: "default", Match: []string{"*"}, Route: "default"}},
		Providers: []gwconfig.Provider{{
			Name: "up",
			Type: "generic",
			Chat: upstreamURL,
			Keys: []gwconfig.ProviderKey{keyNamed("account-a", "sk-a"), keyNamed("account-b", "sk-b")},
			Auth: gwconfig.Auth{Header: "Authorization", Prefix: "Bearer "},
		}},
		Nodes: []gwconfig.Node{{
			Name:     "n",
			Provider: "up",
			Model:    "m",
		}},
		Routes: []gwconfig.Route{{Name: "default", Nodes: []string{"n"}}},
	}
}

func TestBuildNodesSuffixUsesKeyName(t *testing.T) {
	p := gwconfig.Provider{Name: "p", Type: "generic", Chat: "https://example.com/v1"}
	n := gwconfig.Node{Name: "n", Provider: "p", Model: "m"}
	target := upstreamTarget{
		baseURL:     "https://example.com/v1",
		api:         "chat",
		targetModel: "m",
		auth:        gwconfig.Auth{Header: "Authorization", Prefix: "Bearer "},
	}
	keys := []gwconfig.ProviderKey{keyNamed("account-a", "sk-a"), keyNamed("account-b", "sk-b")}

	nodes, err := buildNodes(p, n, keys, "route", target, "r/route/default/n")
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 2 || nodes[0].Name != "route/p/n/account-a" || nodes[1].Name != "route/p/n/account-b" {
		t.Fatalf("node names = %q/%q, want route/p/n/account-a and route/p/n/account-b", nodes[0].Name, nodes[1].Name)
	}
}

func TestBuildNodesSingleKeyHasNoSuffix(t *testing.T) {
	p := gwconfig.Provider{Name: "p", Type: "generic", Chat: "https://example.com/v1"}
	n := gwconfig.Node{Name: "n", Provider: "p", Model: "m", Key: "account-a"}
	target := upstreamTarget{
		baseURL:     "https://example.com/v1",
		api:         "chat",
		targetModel: "m",
		auth:        gwconfig.Auth{Header: "Authorization", Prefix: "Bearer "},
	}

	nodes, err := buildNodes(p, n, []gwconfig.ProviderKey{keyNamed("account-a", "sk-a")}, "route", target, "r/route/default/n")
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 || nodes[0].Name != "route/p/n" {
		t.Fatalf("node name = %q, want route/p/n", nodes[0].Name)
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
	cfg.Nodes[0].Key = "account-b"
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
	cfg.Nodes[0].Key = "account-a"
	gw := startGateway(t, cfg)

	if code := postModel(t, gw, "/v1/chat/completions", `{"model":"gpt-4.5","messages":[]}`); code < 500 {
		t.Fatalf("status = %d, want 5xx when the pinned key is disabled", code)
	}
	if got := hits.get(); got != 0 {
		t.Fatalf("upstream hits = %d, want 0", got)
	}
}
