package gateway_test

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	xlogger "github.com/go-gost/x/logger"

	"github.com/ginuerzh/cuttle/internal/config"
	"github.com/ginuerzh/cuttle/internal/gateway"
	"github.com/ginuerzh/cuttle/internal/stats"
)

func statsConfig(t *testing.T, upstreamURL string) *config.Config {
	t.Helper()

	cfg := &config.Config{}
	cfg.Gateway.Listen = "127.0.0.1:0"
	cfg.Stats.Path = filepath.Join(t.TempDir(), "stats.sqlite")
	cfg.Entrypoints = []config.Entrypoint{
		{Name: "fast", Match: []string{"gpt-4.5*"}, Route: "r"},
		{Name: "claude", Match: []string{"claude-*"}, Route: "r"},
	}
	cfg.Providers = []config.Provider{{
		Name: "p",
		Type: "generic",
		Chat: upstreamURL,
		Keys: []config.ProviderKey{{Name: "key-1", Key: "sk-secret"}},
	}}
	cfg.Nodes = []config.Node{{
		Name:     "p",
		Provider: "p",
		Model:    "deepseek-v4-flash",
	}}
	cfg.Routes = []config.Route{{Name: "r", Nodes: []string{"p"}}}
	return withIDs(t, cfg)
}

func waitRow(t *testing.T, gw *gateway.Gateway) stats.Request {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for {
		rows, err := gw.Stats().Query(context.Background(), stats.Filter{})
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) > 0 {
			return rows[0]
		}
		if time.Now().After(deadline) {
			t.Fatal("no stats row recorded")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestStatsRecordsNonStreaming(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"x","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`)
	}))
	defer upstream.Close()

	cfg := statsConfig(t, upstream.URL)
	gw, err := gateway.New(cfg, xlogger.Nop())
	if err != nil {
		t.Fatal(err)
	}
	if err := gw.Start(); err != nil {
		t.Fatal(err)
	}
	defer gw.Close()

	req, _ := http.NewRequest(http.MethodPost, "http://"+gw.Addr().String()+"/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-4.5","reasoning_effort":"high","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer client-key-must-not-be-stored")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	row := waitRow(t, gw)
	if row.Provider != cfg.Providers[0].ID || row.Model != "deepseek-v4-flash" || row.Effort != "high" {
		t.Fatalf("row = %+v", row)
	}
	if row.Route != cfg.Routes[0].ID || row.ClientModel != "gpt-4.5" {
		t.Fatalf("row = %+v", row)
	}
	if row.InputTokens != 10 || row.OutputTokens != 5 {
		t.Fatalf("tokens = %d/%d, want 10/5", row.InputTokens, row.OutputTokens)
	}
	if row.Status != http.StatusOK || row.Stream {
		t.Fatalf("status/stream = %d/%v", row.Status, row.Stream)
	}

	raw := strings.Join([]string{row.ID, row.SID, row.Model, row.Provider, row.Protocol, row.Error}, " ")
	if strings.Contains(raw, "client-key") || strings.Contains(raw, "sk-secret") {
		t.Fatalf("sensitive data leaked into stats: %q", raw)
	}
}

func TestStatsRecordsExplicitClientEffortLevel(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"x","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`)
	}))
	defer upstream.Close()

	gw, err := gateway.New(statsConfig(t, upstream.URL), xlogger.Nop())
	if err != nil {
		t.Fatal(err)
	}
	if err := gw.Start(); err != nil {
		t.Fatal(err)
	}
	defer gw.Close()

	req, _ := http.NewRequest(http.MethodPost, "http://"+gw.Addr().String()+"/v1/messages",
		strings.NewReader(`{"model":"claude-sonnet-5","max_tokens":16,"output_config":{"effort":"max"},"messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	row := waitRow(t, gw)
	if row.Effort != "max" {
		t.Fatalf("effort = %q, want max (the client-selected level, not the normalized band)", row.Effort)
	}
	if row.ClientModel != "claude-sonnet-5" {
		t.Fatalf("client model = %q, want claude-sonnet-5", row.ClientModel)
	}
}

func TestStatsRecordsStreamingUsage(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		f := w.(http.Flusher)
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n")
		f.Flush()
		io.WriteString(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":7,\"completion_tokens\":3}}\n\n")
		f.Flush()
	}))
	defer upstream.Close()

	gw, err := gateway.New(statsConfig(t, upstream.URL), xlogger.Nop())
	if err != nil {
		t.Fatal(err)
	}
	if err := gw.Start(); err != nil {
		t.Fatal(err)
	}
	defer gw.Close()

	resp, err := http.Post("http://"+gw.Addr().String()+"/v1/chat/completions", "application/json",
		strings.NewReader(`{"model":"gpt-4.5","stream":true,"messages":[{"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
	}
	resp.Body.Close()

	row := waitRow(t, gw)
	if row.InputTokens != 7 || row.OutputTokens != 3 {
		t.Fatalf("stream tokens = %d/%d, want 7/3 (row %+v)", row.InputTokens, row.OutputTokens, row)
	}
	if !row.Stream {
		t.Fatalf("stream = false, want true: %+v", row)
	}
}

func TestAuthAttributesKeyID(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"x","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`)
	}))
	defer upstream.Close()

	cfg := statsConfig(t, upstream.URL)
	cfg.Gateway.Keys = []config.GatewayKey{{ID: "gk_0000000001", Name: "alpha", Key: "secret-key"}}
	gw, err := gateway.New(cfg, xlogger.Nop())
	if err != nil {
		t.Fatal(err)
	}
	if err := gw.Start(); err != nil {
		t.Fatal(err)
	}
	defer gw.Close()

	req, _ := http.NewRequest(http.MethodPost, "http://"+gw.Addr().String()+"/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-4.5","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer secret-key")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	row := waitRow(t, gw)
	if row.GatewayKey != "gk_0000000001" {
		t.Fatalf("gateway key = %q, want the key id gk_0000000001 (row %+v)", row.GatewayKey, row)
	}
}

func TestStatsRecordsStreamTTFT(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		f := w.(http.Flusher)
		time.Sleep(30 * time.Millisecond)
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n")
		f.Flush()
		time.Sleep(500 * time.Millisecond)
		io.WriteString(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":7,\"completion_tokens\":3}}\n\n")
		f.Flush()
	}))
	defer upstream.Close()

	gw, err := gateway.New(statsConfig(t, upstream.URL), xlogger.Nop())
	if err != nil {
		t.Fatal(err)
	}
	if err := gw.Start(); err != nil {
		t.Fatal(err)
	}
	defer gw.Close()

	resp, err := http.Post("http://"+gw.Addr().String()+"/v1/chat/completions", "application/json",
		strings.NewReader(`{"model":"gpt-4.5","stream":true,"messages":[{"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
	}
	resp.Body.Close()

	row := waitRow(t, gw)
	if !row.Stream {
		t.Fatalf("stream = false, want true: %+v", row)
	}
	if row.TTFTMS <= 0 || row.TTFTMS > row.DurationMS {
		t.Fatalf("ttft = %d, duration = %d", row.TTFTMS, row.DurationMS)
	}
	if row.TTFTMS >= 250 {
		t.Fatalf("ttft = %d, want the first event, not the last", row.TTFTMS)
	}
}

func TestStatsRecordsDialFailure(t *testing.T) {
	// Close a listener so its port is deterministically refused.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	cfg := statsConfig(t, "http://"+addr)
	gw, err := gateway.New(cfg, xlogger.Nop())
	if err != nil {
		t.Fatal(err)
	}
	if err := gw.Start(); err != nil {
		t.Fatal(err)
	}
	defer gw.Close()

	req, _ := http.NewRequest(http.MethodPost, "http://"+gw.Addr().String()+"/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-4.5","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusServiceUnavailable)
	}

	row := waitRow(t, gw)
	if row.Route != cfg.Routes[0].ID || row.Provider != cfg.Providers[0].ID || row.Node != cfg.Nodes[0].ID {
		t.Fatalf("route/provider/node = %q/%q/%q, want %q/%q/%q",
			row.Route, row.Provider, row.Node, cfg.Routes[0].ID, cfg.Providers[0].ID, cfg.Nodes[0].ID)
	}
	if row.Status != http.StatusServiceUnavailable || row.ClientStatus != http.StatusServiceUnavailable {
		t.Fatalf("status/clientStatus = %d/%d, want 503/503", row.Status, row.ClientStatus)
	}
	if row.Error == "" {
		t.Fatal("error should be recorded")
	}
	if row.ErrorClass != stats.ErrorClassUpstreamUnavailable {
		t.Fatalf("errorClass = %q, want %q", row.ErrorClass, stats.ErrorClassUpstreamUnavailable)
	}
	if row.URI != "/v1/chat/completions" || row.Method != http.MethodPost {
		t.Fatalf("uri/method = %q/%q", row.URI, row.Method)
	}
}

func TestReloadAppliesCaptureRetain(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	gw, err := gateway.New(statsConfig(t, upstream.URL), xlogger.Nop())
	if err != nil {
		t.Fatal(err)
	}
	defer gw.Close()

	cfg := statsConfig(t, upstream.URL)
	cfg.Stats.Capture.Retain = config.Duration(48 * time.Hour)
	if err := gw.Reload(cfg); err != nil {
		t.Fatal(err)
	}
	if got := gw.Stats().CaptureRetain(); got != 48*time.Hour {
		t.Fatalf("CaptureRetain = %v, want 48h", got)
	}
}
