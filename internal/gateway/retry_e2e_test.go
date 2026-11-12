package gateway_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	xlogger "github.com/go-gost/x/logger"

	"github.com/ginuerzh/cuttle/internal/config"
	"github.com/ginuerzh/cuttle/internal/gateway"
)

// TestGatewayRetriesRetryableUpstream proves the whole retry path end to end:
// the route policy reaches the x forwarder, the retry re-selects a node in the
// same route, the final attempt is the one recorded in stats, and the client
// keeps one request identity across attempts.
func TestGatewayRetriesRetryableUpstream(t *testing.T) {
	var firstHits, secondHits atomic.Int32

	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		firstHits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		io.WriteString(w, `{"error":{"message":"rate limited","type":"rate_limit_error"}}`)
	}))
	defer first.Close()

	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secondHits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"x","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`)
	}))
	defer second.Close()

	cfg := &config.Config{}
	cfg.Gateway.Listen = "127.0.0.1:0"
	cfg.Stats.Path = filepath.Join(t.TempDir(), "stats.sqlite")
	cfg.Entrypoints = []config.Entrypoint{{ID: "e_0000000001", Name: "all", Match: []string{"*"}, Routes: bindings("r_0000000001")}}
	cfg.Providers = []config.Provider{
		{
			ID:   "p_0000000001",
			Name: "a",
			Type: "generic",
			Chat: first.URL,
			Keys: []config.ProviderKey{{ID: "pk_0000000001", Name: "ka", Key: "sk-a"}},
		},
		{
			ID:   "p_0000000002",
			Name: "b",
			Type: "generic",
			Chat: second.URL,
			Keys: []config.ProviderKey{{ID: "pk_0000000002", Name: "kb", Key: "sk-b"}},
		},
	}
	cfg.Nodes = []config.Node{
		{ID: "n_0000000001", Name: "na", Provider: "p_0000000001", Model: "deepseek-chat"},
		{ID: "n_0000000002", Name: "nb", Provider: "p_0000000002", Model: "deepseek-chat"},
	}
	cfg.Routes = []config.Route{{
		ID:       "r_0000000001",
		Name:     "r",
		Nodes:    []string{"n_0000000001", "n_0000000002"},
		Strategy: "fifo",
		Retries:  1,
		// gateway.New does not apply config.Load defaults, so pin the codes.
		RetryCodes: "429,5xx",
	}}

	gw, err := gateway.New(cfg, xlogger.Nop())
	if err != nil {
		t.Fatal(err)
	}
	if err := gw.Start(); err != nil {
		t.Fatal(err)
	}
	defer gw.Close()

	req, _ := http.NewRequest(http.MethodPost, "http://"+gw.Addr().String()+"/v1/chat/completions",
		strings.NewReader(`{"model":"deepseek-chat","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if got := firstHits.Load(); got != 1 {
		t.Fatalf("first upstream hits = %d, want 1", got)
	}
	if got := secondHits.Load(); got != 1 {
		t.Fatalf("second upstream hits = %d, want 1", got)
	}

	row := waitRow(t, gw)
	if row.Retries != 1 {
		t.Fatalf("retries = %d, want 1 (row %+v)", row.Retries, row)
	}
	if row.Status != http.StatusOK {
		t.Fatalf("status = %d, want %d (row %+v)", row.Status, http.StatusOK, row)
	}
	if row.Node != cfg.Nodes[1].ID {
		t.Fatalf("node = %q, want the second node id %q (row %+v)", row.Node, cfg.Nodes[1].ID, row)
	}

	// One identity across attempts: the header the client sees is the stats row.
	if got := resp.Header.Get("X-Cuttle-Request-ID"); got != row.ID {
		t.Fatalf("request id header = %q, stats row id = %q", got, row.ID)
	}
}

// TestGatewayRetryUsesProviderCredentials pins the credential boundary of a
// retry across providers. The replay snapshot is the pristine client request,
// so each attempt must re-apply the selected node's provider key: the first
// upstream sees provider a's key, the second provider b's, and the client's
// inbound credential reaches neither.
func TestGatewayRetryUsesProviderCredentials(t *testing.T) {
	var mu sync.Mutex
	var firstAuth, secondAuth []string

	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		firstAuth = append(firstAuth, r.Header.Get("Authorization"))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		io.WriteString(w, `{"error":{"message":"rate limited","type":"rate_limit_error"}}`)
	}))
	defer first.Close()

	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		secondAuth = append(secondAuth, r.Header.Get("Authorization"))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"x","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`)
	}))
	defer second.Close()

	cfg := &config.Config{}
	cfg.Gateway.Listen = "127.0.0.1:0"
	cfg.Stats.Path = filepath.Join(t.TempDir(), "stats.sqlite")
	cfg.Entrypoints = []config.Entrypoint{{ID: "e_0000000001", Name: "all", Match: []string{"*"}, Routes: bindings("r_0000000001")}}
	cfg.Providers = []config.Provider{
		{
			ID:   "p_0000000001",
			Name: "a",
			Type: "generic",
			Chat: first.URL,
			Keys: []config.ProviderKey{{ID: "pk_0000000001", Name: "ka", Key: "sk-a"}},
		},
		{
			ID:   "p_0000000002",
			Name: "b",
			Type: "generic",
			Chat: second.URL,
			Keys: []config.ProviderKey{{ID: "pk_0000000002", Name: "kb", Key: "sk-b"}},
		},
	}
	cfg.Nodes = []config.Node{
		{ID: "n_0000000001", Name: "na", Provider: "p_0000000001", Model: "deepseek-chat"},
		{ID: "n_0000000002", Name: "nb", Provider: "p_0000000002", Model: "deepseek-chat"},
	}
	cfg.Routes = []config.Route{{
		ID:         "r_0000000001",
		Name:       "r",
		Nodes:      []string{"n_0000000001", "n_0000000002"},
		Strategy:   "fifo",
		Retries:    1,
		RetryCodes: "429,5xx",
	}}

	gw, err := gateway.New(cfg, xlogger.Nop())
	if err != nil {
		t.Fatal(err)
	}
	if err := gw.Start(); err != nil {
		t.Fatal(err)
	}
	defer gw.Close()

	req, _ := http.NewRequest(http.MethodPost, "http://"+gw.Addr().String()+"/v1/chat/completions",
		strings.NewReader(`{"model":"deepseek-chat","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer client-key")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	mu.Lock()
	firstGot, secondGot := append([]string(nil), firstAuth...), append([]string(nil), secondAuth...)
	mu.Unlock()

	if len(firstGot) != 1 || firstGot[0] != "Bearer sk-a" {
		t.Fatalf("first upstream authorization = %q, want [\"Bearer sk-a\"]", firstGot)
	}
	if len(secondGot) != 1 || secondGot[0] != "Bearer sk-b" {
		t.Fatalf("second upstream authorization = %q, want [\"Bearer sk-b\"]", secondGot)
	}
	for _, got := range append(firstGot, secondGot...) {
		if strings.Contains(got, "client-key") {
			t.Fatalf("upstream received the client credential %q", got)
		}
	}
}
