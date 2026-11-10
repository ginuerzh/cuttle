package gateway_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
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
	cfg.Entrypoints = []config.Entrypoint{{Name: "all", Match: []string{"*"}, Route: "r"}}
	cfg.Providers = []config.Provider{
		{
			Name: "a",
			Type: "generic",
			Chat: first.URL,
			Keys: []config.ProviderKey{{Name: "ka", Key: "sk-a"}},
		},
		{
			Name: "b",
			Type: "generic",
			Chat: second.URL,
			Keys: []config.ProviderKey{{Name: "kb", Key: "sk-b"}},
		},
	}
	cfg.Nodes = []config.Node{
		{Name: "na", Provider: "a", Model: "deepseek-chat"},
		{Name: "nb", Provider: "b", Model: "deepseek-chat"},
	}
	cfg.Routes = []config.Route{{
		Name:     "r",
		Nodes:    []string{"na", "nb"},
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
	if row.Node != "nb" {
		t.Fatalf("node = %q, want nb (row %+v)", row.Node, row)
	}

	// One identity across attempts: the header the client sees is the stats row.
	if got := resp.Header.Get("X-Cuttle-Request-ID"); got != row.ID {
		t.Fatalf("request id header = %q, stats row id = %q", got, row.ID)
	}
}
