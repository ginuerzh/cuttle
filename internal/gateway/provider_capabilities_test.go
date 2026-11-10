package gateway

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	gwconfig "github.com/ginuerzh/cuttle/internal/config"
	"github.com/ginuerzh/cuttle/internal/stats"
)

const geminiCapabilityResponse = `{"candidates":[{"content":{"parts":[{"text":"ok"}],"role":"model"},"finishReason":"STOP","index":0}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1,"totalTokenCount":2}}`

// capabilityHit is one request observed by a fake upstream.
type capabilityHit struct {
	path   string
	header http.Header
	body   string
}

// capabilityUpstream responds in the format implied by the request path and
// records every request so assertions can inspect both the path and the
// provider auth headers.
type capabilityUpstream struct {
	mu   sync.Mutex
	hits []capabilityHit
	srv  *httptest.Server
}

func newCapabilityUpstream(t *testing.T) *capabilityUpstream {
	t.Helper()

	u := &capabilityUpstream{}
	u.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)

		u.mu.Lock()
		u.hits = append(u.hits, capabilityHit{
			path:   r.URL.Path,
			header: r.Header.Clone(),
			body:   string(b),
		})
		u.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/messages"):
			io.WriteString(w, anthropicResponse)
		case strings.HasSuffix(r.URL.Path, "/responses"):
			io.WriteString(w, `{"id":"resp_01","object":"response","status":"completed","output":[]}`)
		case strings.Contains(r.URL.Path, ":generateContent"):
			io.WriteString(w, geminiCapabilityResponse)
		default:
			io.WriteString(w, openAIChatResponse)
		}
	}))
	t.Cleanup(u.srv.Close)
	return u
}

func (u *capabilityUpstream) last(t *testing.T) capabilityHit {
	t.Helper()

	u.mu.Lock()
	defer u.mu.Unlock()
	if len(u.hits) == 0 {
		t.Fatal("upstream received no request")
	}
	return u.hits[len(u.hits)-1]
}

func TestProviderCapabilities(t *testing.T) {
	t.Run("same node uses the API selected for each route", func(t *testing.T) {
		up := newCapabilityUpstream(t)
		cfg := capabilityConfig()
		cfg.Stats.Path = filepath.Join(t.TempDir(), "stats.sqlite")
		cfg.Providers = []gwconfig.Provider{{
			ID:        "p_0000000001",
			Name:      "oc",
			Type:      "opencode-go",
			Chat:      up.srv.URL + "/v1",
			Anthropic: up.srv.URL,
			Keys:      []gwconfig.ProviderKey{{ID: "pk_0000000001", Name: "key-1", Key: "sk-oc"}},
		}}
		cfg.Nodes = []gwconfig.Node{
			{ID: "n_0000000002", Name: "n-fast", Provider: "p_0000000001", Model: "deepseek-v4-flash"},
			{ID: "n_0000000003", Name: "n-strong", Provider: "p_0000000001", Model: "glm-4.8"},
		}
		cfg.Routes = []gwconfig.Route{
			{ID: "r_0000000001", Name: "fast", Nodes: []string{"n_0000000002"}},
			{ID: "r_0000000002", Name: "strong", Nodes: []string{"n_0000000003"}},
		}
		gw := startGateway(t, cfg)

		if code := postModel(t, gw, "/v1/chat/completions", `{"model":"gpt-4.5","messages":[{"role":"user","content":"hi"}]}`); code != http.StatusOK {
			t.Fatalf("fast request status = %d", code)
		}
		fast := up.last(t)
		if fast.path != "/v1/chat/completions" {
			t.Fatalf("fast upstream path = %q, want /v1/chat/completions", fast.path)
		}
		if got := fast.header.Get("Authorization"); got != "Bearer sk-oc" {
			t.Fatalf("fast Authorization = %q, want Bearer sk-oc", got)
		}
		if got := fast.header.Get("x-api-key"); got != "sk-oc" {
			t.Fatalf("fast x-api-key = %q, want sk-oc", got)
		}
		if v := decodeBody(t, fast.body); v["model"] != "deepseek-v4-flash" {
			t.Fatalf("fast upstream model = %v, want deepseek-v4-flash", v["model"])
		}

		if code := postModel(t, gw, "/v1/chat/completions", `{"model":"claude-opus-4-5","messages":[{"role":"user","content":"hi"}]}`); code != http.StatusOK {
			t.Fatalf("strong request status = %d", code)
		}
		strong := up.last(t)
		if strong.path != "/v1/messages" {
			t.Fatalf("strong upstream path = %q, want /v1/messages", strong.path)
		}
		if got := strong.header.Get("x-api-key"); got != "sk-oc" {
			t.Fatalf("strong x-api-key = %q, want sk-oc", got)
		}
		if got := strong.header.Get("Authorization"); got != "" {
			t.Fatalf("strong Authorization = %q, want empty for the Anthropic API", got)
		}
		strongBody := decodeBody(t, strong.body)
		if strongBody["model"] != "glm-4.8" {
			t.Fatalf("strong upstream model = %v, want glm-4.8", strongBody["model"])
		}
		if _, ok := strongBody["max_tokens"]; !ok {
			t.Fatalf("strong upstream body is not Anthropic-shaped: %s", strong.body)
		}

		rows := waitCapabilityStats(t, gw, 2)
		protocols := make(map[string]string, len(rows))
		for _, row := range rows {
			protocols[row.ClientModel] = row.Protocol
		}
		if got := protocols["gpt-4.5"]; got != "chat" {
			t.Fatalf("fast stats protocol = %q, want chat (rows %+v)", got, rows)
		}
		if got := protocols["claude-opus-4-5"]; got != "anthropic" {
			t.Fatalf("strong stats protocol = %q, want anthropic (rows %+v)", got, rows)
		}
	})

	t.Run("inbound protocols are rewritten to the selected API", func(t *testing.T) {
		up := newCapabilityUpstream(t)
		cfg := capabilityConfig()
		cfg.Providers = []gwconfig.Provider{{
			ID:   "p_0000000001",
			Name: "ds",
			Type: "deepseek",
			Chat: up.srv.URL + "/v1",
			Keys: []gwconfig.ProviderKey{{ID: "pk_0000000001", Name: "key-1", Key: "sk-ds"}},
		}}
		cfg.Nodes = []gwconfig.Node{{
			ID:       "n_0000000001",
			Name:     "n",
			Provider: "p_0000000001",
			Model:    "deepseek-v4-flash",
		}}
		gw := startGateway(t, cfg)

		if code := postModel(t, gw, "/v1/responses", `{"model":"gpt-4.5","input":"hi"}`); code != http.StatusOK {
			t.Fatalf("responses inbound status = %d", code)
		}
		if got := up.last(t).path; got != "/v1/chat/completions" {
			t.Fatalf("responses inbound path = %q, want /v1/chat/completions", got)
		}

		if code := postModel(t, gw, "/v1/messages", `{"model":"gpt-4.5","max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`); code != http.StatusOK {
			t.Fatalf("messages inbound status = %d", code)
		}
		hit := up.last(t)
		if hit.path != "/v1/chat/completions" {
			t.Fatalf("messages inbound path = %q, want /v1/chat/completions", hit.path)
		}
		if v := decodeBody(t, hit.body); v["model"] != "deepseek-v4-flash" {
			t.Fatalf("messages inbound upstream model = %v, want deepseek-v4-flash", v["model"])
		}
	})

	t.Run("deepseek uses the first configured model API", func(t *testing.T) {
		up := newCapabilityUpstream(t)
		cfg := capabilityConfig()
		cfg.Providers = []gwconfig.Provider{{
			ID:        "p_0000000001",
			Name:      "ds",
			Type:      "deepseek",
			Chat:      up.srv.URL + "/v1",
			Responses: up.srv.URL + "/v1",
			Anthropic: up.srv.URL + "/anthropic",
			Keys:      []gwconfig.ProviderKey{{ID: "pk_0000000001", Name: "key-1", Key: "sk-ds"}},
		}}
		cfg.Nodes = []gwconfig.Node{{
			ID:       "n_0000000001",
			Name:     "n",
			Provider: "p_0000000001",
			Model:    "deepseek-v4-flash",
		}}
		gw := startGateway(t, cfg)

		if code := postModel(t, gw, "/v1/chat/completions", `{"model":"gpt-4.5","messages":[{"role":"user","content":"hi"}]}`); code != http.StatusOK {
			t.Fatalf("status = %d", code)
		}
		if got := up.last(t).path; got != "/v1/chat/completions" {
			t.Fatalf("upstream path = %q, want the first configured model API (chat)", got)
		}
	})

	t.Run("deepseek falls through to the next configured model API", func(t *testing.T) {
		up := newCapabilityUpstream(t)
		cfg := capabilityConfig()
		cfg.Providers = []gwconfig.Provider{{
			ID:        "p_0000000001",
			Name:      "ds",
			Type:      "deepseek",
			Responses: up.srv.URL + "/v1",
			Keys:      []gwconfig.ProviderKey{{ID: "pk_0000000001", Name: "key-1", Key: "sk-ds"}},
		}}
		cfg.Nodes = []gwconfig.Node{{
			ID:       "n_0000000001",
			Name:     "n",
			Provider: "p_0000000001",
			Model:    "deepseek-v4-flash",
		}}
		gw := startGateway(t, cfg)

		if code := postModel(t, gw, "/v1/messages", `{"model":"claude-opus-4-5","max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`); code != http.StatusOK {
			t.Fatalf("status = %d", code)
		}
		if got := up.last(t).path; got != "/v1/responses" {
			t.Fatalf("upstream path = %q, want /v1/responses (anthropic has no base URL)", got)
		}
	})

	t.Run("commandcode claude uses anthropic with bearer auth", func(t *testing.T) {
		up := newCapabilityUpstream(t)
		cfg := capabilityConfig()
		cfg.Providers = []gwconfig.Provider{{
			ID:        "p_0000000001",
			Name:      "cc",
			Type:      "commandcode",
			Chat:      up.srv.URL + "/provider/v1",
			Anthropic: up.srv.URL + "/provider",
			Keys:      []gwconfig.ProviderKey{{ID: "pk_0000000001", Name: "key-1", Key: "sk-cc"}},
		}}
		cfg.Nodes = []gwconfig.Node{{
			ID:       "n_0000000001",
			Name:     "n",
			Provider: "p_0000000001",
			Model:    "claude-sonnet-4",
		}}
		gw := startGateway(t, cfg)

		if code := postModel(t, gw, "/v1/chat/completions", `{"model":"claude-opus-4-5","messages":[{"role":"user","content":"hi"}]}`); code != http.StatusOK {
			t.Fatalf("status = %d", code)
		}
		hit := up.last(t)
		if hit.path != "/provider/v1/messages" {
			t.Fatalf("upstream path = %q, want /provider/v1/messages", hit.path)
		}
		if got := hit.header.Get("Authorization"); got != "Bearer sk-cc" {
			t.Fatalf("Authorization = %q, want Bearer sk-cc", got)
		}
		if got := hit.header.Get("x-api-key"); got != "" {
			t.Fatalf("x-api-key = %q, want empty for commandcode", got)
		}
	})

	t.Run("gemini target path contains the mapped model", func(t *testing.T) {
		up := newCapabilityUpstream(t)
		cfg := capabilityConfig()
		cfg.Providers = []gwconfig.Provider{{
			ID:     "p_0000000001",
			Name:   "gm",
			Type:   "generic",
			Gemini: up.srv.URL + "/v1beta",
			Keys:   []gwconfig.ProviderKey{{ID: "pk_0000000001", Name: "key-1", Key: "gk"}},
		}}
		cfg.Nodes = []gwconfig.Node{{
			ID:       "n_0000000001",
			Name:     "n",
			Provider: "p_0000000001",
			Model:    "gemini-2.5-pro",
		}}
		gw := startGateway(t, cfg)

		if code := postModel(t, gw, "/v1/chat/completions", `{"model":"gpt-4.5","messages":[{"role":"user","content":"hi"}]}`); code != http.StatusOK {
			t.Fatalf("status = %d", code)
		}
		hit := up.last(t)
		if hit.path != "/v1beta/models/gemini-2.5-pro:generateContent" {
			t.Fatalf("upstream path = %q, want /v1beta/models/gemini-2.5-pro:generateContent", hit.path)
		}
		if got := hit.header.Get("x-goog-api-key"); got != "gk" {
			t.Fatalf("x-goog-api-key = %q, want gk", got)
		}
	})

	t.Run("unknown model falls back to the first configured API", func(t *testing.T) {
		up := newCapabilityUpstream(t)
		cfg := capabilityConfig()
		cfg.Providers = []gwconfig.Provider{{
			ID:        "p_0000000001",
			Name:      "oc",
			Type:      "opencode-go",
			Chat:      up.srv.URL + "/v1",
			Anthropic: up.srv.URL,
			Keys:      []gwconfig.ProviderKey{{ID: "pk_0000000001", Name: "key-1", Key: "sk-oc"}},
		}}
		cfg.Nodes = []gwconfig.Node{{
			ID:       "n_0000000001",
			Name:     "n",
			Provider: "p_0000000001",
			Model:    "model-not-in-the-catalog",
		}}
		gw := startGateway(t, cfg)

		if code := postModel(t, gw, "/v1/chat/completions", `{"model":"gpt-4.5","messages":[{"role":"user","content":"hi"}]}`); code != http.StatusOK {
			t.Fatalf("status = %d, want the unknown model to still route", code)
		}
		hit := up.last(t)
		if hit.path != "/v1/chat/completions" {
			t.Fatalf("upstream path = %q, want the first configured API (chat)", hit.path)
		}
		if v := decodeBody(t, hit.body); v["model"] != "model-not-in-the-catalog" {
			t.Fatalf("upstream model = %v, want model-not-in-the-catalog", v["model"])
		}
	})

	t.Run("auth extra applies without keys", func(t *testing.T) {
		up := newCapabilityUpstream(t)
		cfg := &gwconfig.Config{
			Gateway: gwconfig.Gateway{Listen: "127.0.0.1:0"},
			Stats:   gwconfig.Stats{Path: filepath.Join(t.TempDir(), "stats.sqlite")},
			Providers: []gwconfig.Provider{{
				ID:   "p_0000000001",
				Name: "gm",
				Type: "generic",
				Chat: up.srv.URL + "/v1",
				Auth: gwconfig.Auth{Extra: map[string]string{"X-Org": "acme"}},
			}},
			Nodes: []gwconfig.Node{{
				ID:       "n_0000000001",
				Name:     "n",
				Provider: "p_0000000001",
				Model:    "chat-model",
			}},
			Entrypoints: []gwconfig.Entrypoint{{ID: "e_0000000001", Name: "fast", Match: []string{"fast"}, Route: "r_0000000001"}},
			Routes:      []gwconfig.Route{{ID: "r_0000000001", Name: "fast", Nodes: []string{"n_0000000001"}}},
		}
		gw := startGateway(t, cfg)

		if code := postModel(t, gw, "/v1/chat/completions", `{"model":"fast","messages":[{"role":"user","content":"hi"}]}`); code != http.StatusOK {
			t.Fatalf("status = %d", code)
		}
		hit := up.last(t)
		if got := hit.header.Get("X-Org"); got != "acme" {
			t.Fatalf("X-Org = %q, want acme", got)
		}
		if got := hit.header.Get("Authorization"); got != "" {
			t.Fatalf("Authorization = %q, want empty for a keyless provider", got)
		}
	})

	t.Run("generic routing matches the inbound protocol when the base exists", func(t *testing.T) {
		up := newCapabilityUpstream(t)
		cfg := &gwconfig.Config{
			Gateway: gwconfig.Gateway{Listen: "127.0.0.1:0"},
			Stats:   gwconfig.Stats{Path: filepath.Join(t.TempDir(), "stats.sqlite")},
			Providers: []gwconfig.Provider{{
				ID:        "p_0000000001",
				Name:      "gm",
				Type:      "generic",
				Responses: up.srv.URL + "/v1",
				Chat:      up.srv.URL + "/v1",
				Keys:      []gwconfig.ProviderKey{{ID: "pk_0000000001", Name: "key-1", Key: "sk-gm"}},
			}},
			Nodes: []gwconfig.Node{{
				ID:       "n_0000000001",
				Name:     "n",
				Provider: "p_0000000001",
				Model:    "unknown-model",
			}},
			Entrypoints: []gwconfig.Entrypoint{{ID: "e_0000000001", Name: "fast", Match: []string{"fast"}, Route: "r_0000000001"}},
			Routes:      []gwconfig.Route{{ID: "r_0000000001", Name: "fast", Nodes: []string{"n_0000000001"}}},
		}
		gw := startGateway(t, cfg)

		if code := postModel(t, gw, "/v1/responses", `{"model":"fast","input":"hi"}`); code != http.StatusOK {
			t.Fatalf("status = %d", code)
		}
		if got := up.last(t).path; got != "/v1/responses" {
			t.Fatalf("upstream path = %q, want the inbound-matching responses API", got)
		}
	})

	t.Run("catch-all route with only a star mapping", func(t *testing.T) {
		up := newCapabilityUpstream(t)
		cfg := &gwconfig.Config{
			Gateway: gwconfig.Gateway{Listen: "127.0.0.1:0"},
			Stats:   gwconfig.Stats{Path: filepath.Join(t.TempDir(), "stats.sqlite")},
			Providers: []gwconfig.Provider{{
				ID:   "p_0000000001",
				Name: "gm",
				Type: "generic",
				Chat: up.srv.URL + "/v1",
				Keys: []gwconfig.ProviderKey{{ID: "pk_0000000001", Name: "key-1", Key: "sk-gm"}},
			}},
			Nodes: []gwconfig.Node{{
				ID:       "n_0000000001",
				Name:     "n",
				Provider: "p_0000000001",
				Model:    "deepseek-v4-flash",
			}},
			Entrypoints: []gwconfig.Entrypoint{{ID: "e_0000000001", Name: "all", Match: []string{"*"}, Route: "r_0000000001"}},
			Routes:      []gwconfig.Route{{ID: "r_0000000001", Name: "all", Nodes: []string{"n_0000000001"}}},
		}
		gw := startGateway(t, cfg)

		if code := postModel(t, gw, "/v1/chat/completions", `{"model":"anything","messages":[{"role":"user","content":"hi"}]}`); code != http.StatusOK {
			t.Fatalf("status = %d", code)
		}
		hit := up.last(t)
		if hit.path != "/v1/chat/completions" {
			t.Fatalf("upstream path = %q, want /v1/chat/completions", hit.path)
		}
		if v := decodeBody(t, hit.body); v["model"] != "deepseek-v4-flash" {
			t.Fatalf("upstream model = %v, want the star mapping target", v["model"])
		}

		rows := waitCapabilityStats(t, gw, 1)
		if got := rows[0].Protocol; got != "chat" {
			t.Fatalf("stats protocol = %q, want chat (rows %+v)", got, rows)
		}
		if got := rows[0].Model; got != "deepseek-v4-flash" {
			t.Fatalf("stats model = %q, want the star mapping target (rows %+v)", got, rows)
		}
	})

	t.Run("glob route with an exact node mapping keeps path and protocol aligned", func(t *testing.T) {
		up := newCapabilityUpstream(t)
		cfg := &gwconfig.Config{
			Gateway: gwconfig.Gateway{Listen: "127.0.0.1:0"},
			Stats:   gwconfig.Stats{Path: filepath.Join(t.TempDir(), "stats.sqlite")},
			Providers: []gwconfig.Provider{{
				ID:        "p_0000000001",
				Name:      "oc",
				Type:      "opencode-go",
				Chat:      up.srv.URL + "/v1",
				Anthropic: up.srv.URL,
				Keys:      []gwconfig.ProviderKey{{ID: "pk_0000000001", Name: "key-1", Key: "sk-oc"}},
			}},
			Nodes: []gwconfig.Node{{
				ID:       "n_0000000001",
				Name:     "n",
				Provider: "p_0000000001",
				Model:    "glm-4.8",
			}},
			Entrypoints: []gwconfig.Entrypoint{{ID: "e_0000000001", Name: "claude", Match: []string{"claude-*"}, Route: "r_0000000001"}},
			Routes:      []gwconfig.Route{{ID: "r_0000000001", Name: "claude", Nodes: []string{"n_0000000001"}}},
		}
		gw := startGateway(t, cfg)

		if code := postModel(t, gw, "/v1/chat/completions", `{"model":"claude-opus-4-5","messages":[{"role":"user","content":"hi"}]}`); code != http.StatusOK {
			t.Fatalf("status = %d", code)
		}
		hit := up.last(t)
		if hit.path != "/v1/messages" {
			t.Fatalf("upstream path = %q, want /v1/messages", hit.path)
		}

		rows := waitCapabilityStats(t, gw, 1)
		if got := rows[0].Protocol; got != "anthropic" {
			t.Fatalf("stats protocol = %q, want anthropic to match the path (rows %+v)", got, rows)
		}
		if got := rows[0].Model; got != "glm-4.8" {
			t.Fatalf("stats model = %q, want glm-4.8 (rows %+v)", got, rows)
		}
		if v := decodeBody(t, hit.body); v["model"] != "glm-4.8" {
			t.Fatalf("upstream model = %v, want glm-4.8", v["model"])
		}
	})

	t.Run("route-targeted responses API records the selected protocol", func(t *testing.T) {
		up := newCapabilityUpstream(t)
		cfg := &gwconfig.Config{
			Gateway: gwconfig.Gateway{Listen: "127.0.0.1:0"},
			Stats:   gwconfig.Stats{Path: filepath.Join(t.TempDir(), "stats.sqlite")},
			Providers: []gwconfig.Provider{{
				ID:        "p_0000000001",
				Name:      "ds",
				Type:      "deepseek",
				Responses: up.srv.URL + "/v1",
				Keys:      []gwconfig.ProviderKey{{ID: "pk_0000000001", Name: "key-1", Key: "sk-ds"}},
			}},
			Nodes: []gwconfig.Node{{
				ID:       "n_0000000001",
				Name:     "n",
				Provider: "p_0000000001",
				Model:    "deepseek-v4-flash",
			}},
			Entrypoints: []gwconfig.Entrypoint{{ID: "e_0000000001", Name: "fast", Match: []string{"fast"}, Route: "r_0000000001"}},
			Routes:      []gwconfig.Route{{ID: "r_0000000001", Name: "fast", Nodes: []string{"n_0000000001"}}},
		}
		gw := startGateway(t, cfg)

		if code := postModel(t, gw, "/v1/messages", `{"model":"fast","max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`); code != http.StatusOK {
			t.Fatalf("status = %d", code)
		}
		hit := up.last(t)
		if hit.path != "/v1/responses" {
			t.Fatalf("upstream path = %q, want /v1/responses", hit.path)
		}
		if v := decodeBody(t, hit.body); v["model"] != "deepseek-v4-flash" {
			t.Fatalf("upstream model = %v, want deepseek-v4-flash", v["model"])
		}

		rows := waitCapabilityStats(t, gw, 1)
		if got := rows[0].Protocol; got != "responses" {
			t.Fatalf("stats protocol = %q, want responses (rows %+v)", got, rows)
		}
	})

	t.Run("non-chat inbound path records the route protocol", func(t *testing.T) {
		up := newCapabilityUpstream(t)
		cfg := &gwconfig.Config{
			Gateway: gwconfig.Gateway{Listen: "127.0.0.1:0"},
			Stats:   gwconfig.Stats{Path: filepath.Join(t.TempDir(), "stats.sqlite")},
			Providers: []gwconfig.Provider{{
				ID:        "p_0000000001",
				Name:      "oc",
				Type:      "opencode-go",
				Chat:      up.srv.URL + "/v1",
				Anthropic: up.srv.URL,
				Keys:      []gwconfig.ProviderKey{{ID: "pk_0000000001", Name: "key-1", Key: "sk-oc"}},
			}},
			Nodes: []gwconfig.Node{{
				ID:       "n_0000000001",
				Name:     "n",
				Provider: "p_0000000001",
				Model:    "glm-4.8",
			}},
			Entrypoints: []gwconfig.Entrypoint{{ID: "e_0000000001", Name: "claude", Match: []string{"claude-*"}, Route: "r_0000000001"}},
			Routes:      []gwconfig.Route{{ID: "r_0000000001", Name: "claude", Nodes: []string{"n_0000000001"}}},
		}
		gw := startGateway(t, cfg)

		body := `{"model":"claude-opus-4-5","messages":[{"role":"user","content":"hi"}]}`
		if code := postModel(t, gw, "/v1/messages/count_tokens", body); code != http.StatusOK {
			t.Fatalf("status = %d", code)
		}
		if got := up.last(t).path; got != "/v1/messages/count_tokens" {
			t.Fatalf("upstream path = %q, want the pass-through count_tokens path", got)
		}

		rows := waitCapabilityStats(t, gw, 1)
		if got := rows[0].Protocol; got != "anthropic" {
			t.Fatalf("stats protocol = %q, want the route's selected API anthropic (rows %+v)", got, rows)
		}
	})
}

func TestBuildNodesSkipsEmptyHeaderWhenKeyless(t *testing.T) {
	p := gwconfig.Provider{Name: "p", Type: "generic", Chat: "https://example.com/v1"}
	n := gwconfig.Node{Name: "n", Provider: "p"}
	target := upstreamTarget{
		baseURL:     "https://example.com/v1",
		api:         "chat",
		targetModel: "target-model",
		auth: gwconfig.Auth{
			Header: "Authorization",
			Prefix: "Bearer ",
			Extra:  map[string]string{"X-Org": "acme"},
		},
	}
	keys, keyErr := resolveNodeKeys(p, n)
	if keyErr != nil {
		t.Fatal(keyErr)
	}
	nodes, err := buildNodes(p, n, keys, gwconfig.Route{ID: "route"}, target, "r/route/default/n")
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 {
		t.Fatalf("nodes = %d, want 1", len(nodes))
	}
	headers := nodes[0].HTTP.RequestHeader
	if _, ok := headers[""]; ok {
		t.Fatalf("keyless node wrote an empty header name: %+v", headers)
	}
	if got := headers["X-Org"]; got != "acme" {
		t.Fatalf("X-Org = %q, want acme", got)
	}
}

func TestBuildNodesExtraWinsOverKeyHeaders(t *testing.T) {
	p := gwconfig.Provider{Name: "p", Type: "generic", Chat: "https://example.com/v1", Keys: []gwconfig.ProviderKey{{ID: "pk_0000000001", Name: "key-1", Key: "sk-test"}}}
	n := gwconfig.Node{Name: "n", Provider: "p"}
	target := upstreamTarget{
		baseURL:     "https://example.com/v1",
		api:         "chat",
		targetModel: "target-model",
		auth: gwconfig.Auth{
			Header:     "Authorization",
			Prefix:     "Bearer ",
			KeyHeaders: []gwconfig.KeyHeader{{Header: "x-api-key"}},
			Extra: map[string]string{
				"Authorization": "Token explicit",
				"x-api-key":     "explicit",
			},
		},
	}
	keys, keyErr := resolveNodeKeys(p, n)
	if keyErr != nil {
		t.Fatal(keyErr)
	}
	nodes, err := buildNodes(p, n, keys, gwconfig.Route{ID: "route"}, target, "r/route/default/n")
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 {
		t.Fatalf("nodes = %d, want 1", len(nodes))
	}
	headers := nodes[0].HTTP.RequestHeader
	if got := headers["Authorization"]; got != "Token explicit" {
		t.Fatalf("Authorization = %q, want auth.extra to win over the key header", got)
	}
	if got := headers["x-api-key"]; got != "explicit" {
		t.Fatalf("x-api-key = %q, want auth.extra to win over keyHeaders", got)
	}
}

func TestRewriterNameCannotCollide(t *testing.T) {
	if got := rewriterName("r", "default", "n"); got != "r/r/default/n" {
		t.Fatalf("rewriterName(r, default, n) = %q, want r/r/default/n", got)
	}
	// ':' was ambiguous as a separator; with '/', which route/node names
	// forbid, these two pairs necessarily differ.
	if rewriterName("a:b", "default", "c") == rewriterName("a", "default", "b:c") {
		t.Fatal("rewriter names collide for names containing ':'")
	}
}

func capabilityConfig() *gwconfig.Config {
	return &gwconfig.Config{
		Gateway: gwconfig.Gateway{Listen: "127.0.0.1:0"},
		Entrypoints: []gwconfig.Entrypoint{
			{ID: "e_0000000001", Name: "fast", Match: []string{"gpt-4.5*"}, Route: "r_0000000001"},
			{ID: "e_0000000002", Name: "strong", Match: []string{"claude-*"}, Route: "r_0000000002"},
		},
		Routes: []gwconfig.Route{
			{ID: "r_0000000001", Name: "fast", Nodes: []string{"n_0000000001"}},
			{ID: "r_0000000002", Name: "strong", Nodes: []string{"n_0000000001"}},
		},
	}
}

func waitCapabilityStats(t *testing.T, gw *Gateway, n int) []stats.Request {
	t.Helper()

	deadline := time.Now().Add(3 * time.Second)
	for {
		rows, err := gw.Stats().Query(context.Background(), stats.Filter{})
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) >= n {
			return rows
		}
		if time.Now().After(deadline) {
			t.Fatalf("stats rows = %d, want %d", len(rows), n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
