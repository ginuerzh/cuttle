package gateway

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	gwconfig "github.com/ginuerzh/cuttle/internal/config"
	"github.com/ginuerzh/cuttle/internal/provider"
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

	cfg := matrixConfig(upstream.URL, provider.Chat)
	cfg.Providers[0].Keys = []string{"k1", "k2"}
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

	cfg := matrixConfig(primary.URL, provider.Chat)
	cfg.Providers = append(cfg.Providers, gwconfig.Provider{
		Name: "backup",
		Type: "generic",
		Chat: backup.URL,
	})
	cfg.Nodes = append(cfg.Nodes, gwconfig.Node{Name: "backup", Provider: "backup", Model: "backup-model", Backup: true})
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

func TestDisabledProviderHasNoRoute(t *testing.T) {
	up, hit := captureUpstream(t)
	cfg := matrixConfig(up.URL, provider.Chat)
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
