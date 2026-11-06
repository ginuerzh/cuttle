package gateway_test

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	xlogger "github.com/go-gost/x/logger"

	"github.com/go-gost/llm-gw/internal/config"
	"github.com/go-gost/llm-gw/internal/gateway"
	"github.com/go-gost/llm-gw/internal/stats"
)

func statsConfig(t *testing.T, upstreamURL string) *config.Config {
	t.Helper()

	cfg := &config.Config{}
	cfg.Gateway.Listen = "127.0.0.1:0"
	cfg.Stats.Path = filepath.Join(t.TempDir(), "stats.sqlite")
	cfg.Entrypoints = []config.Entrypoint{
		{Name: "fast", Match: []string{"gpt-4.5*"}, Route: "r"},
	}
	cfg.Providers = []config.Provider{{
		Name: "p",
		Type: "generic",
		Chat: upstreamURL,
		Keys: []string{"sk-secret"},
	}}
	cfg.Nodes = []config.Node{{
		Name:     "p",
		Provider: "p",
		Model:    "deepseek-v4-flash",
	}}
	cfg.Routes = []config.Route{{Name: "r", Nodes: []string{"p"}}}
	return cfg
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

	gw, err := gateway.New(statsConfig(t, upstream.URL), xlogger.Nop())
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
	if row.Provider != "p" || row.Model != "deepseek-v4-flash" || row.Effort != "high" {
		t.Fatalf("row = %+v", row)
	}
	if row.Route != "r" || row.ClientModel != "gpt-4.5" {
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
