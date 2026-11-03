package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	gwconfig "github.com/go-gost/llm-gw/internal/config"
)

func postModel(t *testing.T, gw *Gateway, path, body string) int {
	t.Helper()

	resp, err := http.Post("http://"+gw.Addr().String()+path, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

func TestEffortTierRouting(t *testing.T) {
	fastSrv, fastHit := captureUpstream(t)
	strongSrv, strongHit := captureUpstream(t)

	cfg := matrixConfig(fastSrv.URL, "openai", nil)
	cfg.Nodes[0].Models = map[string]string{"*": "fast-model"}
	cfg.Providers = append(cfg.Providers, gwconfig.Provider{
		Name:     "strong",
		BaseURL:  strongSrv.URL,
		Protocol: "openai",
	})
	cfg.Nodes = append(cfg.Nodes, gwconfig.Node{
		Name:     "strong",
		Provider: "strong",
		Models:   map[string]string{"*": "strong-model"},
		Tier:     "high",
	})
	cfg.Routes[0].Nodes = []string{"up", "strong"}

	gw := startGateway(t, cfg)

	reset := func() { fastHit.path, strongHit.path = "", "" }

	if code := postModel(t, gw, "/v1/chat/completions",
		`{"model":"gpt-4.5","reasoning_effort":"high","messages":[{"role":"user","content":"hi"}]}`); code != 200 {
		t.Fatalf("high status = %d", code)
	}
	if strongHit.path == "" {
		t.Fatal("high effort did not select the strong provider")
	}
	if fastHit.path != "" {
		t.Fatal("high effort also hit the fast provider")
	}

	reset()
	postModel(t, gw, "/v1/chat/completions", `{"model":"gpt-4.5","messages":[{"role":"user","content":"hi"}]}`)
	if fastHit.path == "" || strongHit.path != "" {
		t.Fatalf("no effort should use the base provider: fast=%q strong=%q", fastHit.path, strongHit.path)
	}

	reset()
	postModel(t, gw, "/v1/chat/completions",
		`{"model":"gpt-4.5","thinking":{"type":"enabled","budget_tokens":20000},"messages":[{"role":"user","content":"hi"}]}`)
	if strongHit.path == "" {
		t.Fatal("large thinking budget did not select the strong provider")
	}

	reset()
	postModel(t, gw, "/v1/chat/completions",
		`{"model":"gpt-4.5","generationConfig":{"thinkingConfig":{"thinkingBudget":20000}},"messages":[{"role":"user","content":"hi"}]}`)
	if strongHit.path == "" {
		t.Fatal("Gemini thinkingBudget did not select the strong provider")
	}

	// Effort-specific model mapping still applies on the tier node.
	var v map[string]any
	if err := json.Unmarshal([]byte(strongHit.body), &v); err != nil {
		t.Fatal(err)
	}
	if v["model"] != "strong-model" {
		t.Fatalf("tier model = %v, want strong-model", v["model"])
	}
}

func TestRoutePrioritySpecificBeforeCatchAll(t *testing.T) {
	specificSrv, specificHit := captureUpstream(t)
	catchAllSrv, catchAllHit := captureUpstream(t)

	cfg := matrixConfig(catchAllSrv.URL, "openai", nil)
	cfg.Providers = append(cfg.Providers, gwconfig.Provider{
		Name:     "specific",
		BaseURL:  specificSrv.URL,
		Protocol: "openai",
	})
	cfg.Nodes = append(cfg.Nodes, gwconfig.Node{Name: "specific", Provider: "specific"})
	cfg.Routes = []gwconfig.Route{
		{Name: "specific", Nodes: []string{"specific"}, Match: gwconfig.Match{Model: "gpt-5.5"}},
		{Name: "default", Nodes: []string{"up"}},
	}
	gw := startGateway(t, cfg)

	postModel(t, gw, "/v1/chat/completions", `{"model":"gpt-5.5","messages":[]}`)
	if specificHit.path == "" || catchAllHit.path != "" {
		t.Fatalf("specific route lost: specific=%q catchall=%q", specificHit.path, catchAllHit.path)
	}

	postModel(t, gw, "/v1/chat/completions", `{"model":"gpt-4.5","messages":[]}`)
	if catchAllHit.path == "" {
		t.Fatal("catch-all route did not serve the unmatched model")
	}
}

func TestMultiKey429Rotation(t *testing.T) {
	var mu sync.Mutex
	var seen []string

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("Authorization")
		mu.Lock()
		seen = append(seen, key)
		mu.Unlock()

		if key == "Bearer k1" {
			w.WriteHeader(http.StatusTooManyRequests)
			io.WriteString(w, `{"error":{"message":"rate limited"}}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"x","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
	}))
	defer upstream.Close()

	cfg := matrixConfig(upstream.URL, "openai", nil)
	cfg.Providers[0].Keys = []string{"k1", "k2"}
	cfg.Nodes[0].Models = nil
	cfg.Aliases = nil
	cfg.Routes[0].Strategy = "round"
	gw := startGateway(t, cfg)

	codes := make([]int, 0, 3)
	for i := 0; i < 3; i++ {
		codes = append(codes, postModel(t, gw, "/v1/chat/completions", `{"model":"gpt-4.5","messages":[]}`))
	}

	mu.Lock()
	got := append([]string(nil), seen...)
	mu.Unlock()

	k1 := 0
	for _, k := range got {
		if k == "Bearer k1" {
			k1++
		}
	}
	if k1 > 1 {
		t.Fatalf("failed key was not rotated out: %v (codes %v)", got, codes)
	}
	if codes[len(codes)-1] != http.StatusOK {
		t.Fatalf("last request status = %d, want 200 (codes %v, keys %v)", codes[len(codes)-1], codes, got)
	}
}

func TestBackupProviderUsedOnlyAfterPrimaryFails(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer primary.Close()

	var mu sync.Mutex
	backupHits := 0
	backup := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		backupHits++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"x","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
	}))
	defer backup.Close()

	cfg := matrixConfig(primary.URL, "openai", nil)
	cfg.Nodes[0].Models = nil
	cfg.Aliases = nil
	cfg.Providers = append(cfg.Providers, gwconfig.Provider{
		Name:     "backup",
		BaseURL:  backup.URL,
		Protocol: "openai",
	})
	cfg.Nodes = append(cfg.Nodes, gwconfig.Node{Name: "backup", Provider: "backup", Backup: true})
	cfg.Routes[0].Nodes = []string{"up", "backup"}
	gw := startGateway(t, cfg)

	first := postModel(t, gw, "/v1/chat/completions", `{"model":"gpt-4.5","messages":[]}`)
	if first != http.StatusInternalServerError {
		t.Fatalf("first status = %d, want 500 from the primary", first)
	}
	second := postModel(t, gw, "/v1/chat/completions", `{"model":"gpt-4.5","messages":[]}`)
	if second != http.StatusOK {
		t.Fatalf("second status = %d, want 200 from the backup", second)
	}

	mu.Lock()
	defer mu.Unlock()
	if backupHits != 1 {
		t.Fatalf("backup hits = %d, want 1 (only after the primary failed)", backupHits)
	}
}

func TestEffortFallbackWhenTierFails(t *testing.T) {
	fastSrv, fastHit := captureUpstream(t)
	strong := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer strong.Close()

	cfg := matrixConfig(fastSrv.URL, "openai", nil)
	cfg.Nodes[0].Models = nil
	cfg.Aliases = nil
	cfg.Providers = append(cfg.Providers, gwconfig.Provider{
		Name:     "strong",
		BaseURL:  strong.URL,
		Protocol: "openai",
	})
	cfg.Nodes = append(cfg.Nodes, gwconfig.Node{Name: "strong", Provider: "strong", Tier: "high"})
	cfg.Routes[0].Nodes = []string{"up", "strong"}
	gw := startGateway(t, cfg)

	body := `{"model":"gpt-4.5","reasoning_effort":"high","messages":[]}`
	if code := postModel(t, gw, "/v1/chat/completions", body); code != http.StatusBadGateway {
		t.Fatalf("first status = %d, want 502 from the high tier", code)
	}
	if code := postModel(t, gw, "/v1/chat/completions", body); code != http.StatusOK {
		t.Fatalf("second status = %d, want 200 after falling back", code)
	}
	if fastHit.path == "" {
		t.Fatal("high effort did not fall back to the base provider after the tier failed")
	}
}

func TestDisabledProviderHasNoRoute(t *testing.T) {
	up, hit := captureUpstream(t)
	cfg := matrixConfig(up.URL, "openai", nil)
	off := false
	cfg.Providers[0].Enabled = &off

	gw, err := New(cfg, nil)
	if err != nil {
		t.Fatalf("gateway with only disabled providers should still start: %v", err)
	}
	if err := gw.Start(); err != nil {
		t.Fatal(err)
	}
	defer gw.Close()

	if code := postModel(t, gw, "/v1/chat/completions", `{"model":"gpt-4.5","messages":[]}`); code < 500 {
		t.Fatalf("status = %d, want 5xx when the route has no enabled nodes", code)
	}
	if hit.path != "" {
		t.Fatal("disabled provider received traffic")
	}
}
