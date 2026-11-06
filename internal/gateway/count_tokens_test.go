package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	gwconfig "github.com/go-gost/llm-gw/internal/config"
	"github.com/go-gost/llm-gw/internal/provider"
	"github.com/go-gost/llm-gw/internal/stats"
)

const countTokensBody = `{"model":"claude-opus-4-5","messages":[{"role":"user","content":"hi"}]}`

func postGateway(t *testing.T, gw *Gateway, path, body string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, "http://"+gw.Addr().String()+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func inputTokens(t *testing.T, body string) int {
	t.Helper()
	var v struct {
		InputTokens int `json:"input_tokens"`
	}
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		t.Fatalf("body = %s: %v", body, err)
	}
	return v.InputTokens
}

func TestCountTokensEstimatesForChatTarget(t *testing.T) {
	up, got := captureUpstream(t)
	gw := startGateway(t, matrixConfig(up.URL, provider.Chat))

	status, body := postGateway(t, gw, "/v1/messages/count_tokens", countTokensBody)
	if status != http.StatusOK {
		t.Fatalf("status = %d body = %s", status, body)
	}
	if n := inputTokens(t, body); n < 1 {
		t.Fatalf("input_tokens = %d, want >= 1", n)
	}
	if got.path != "" {
		t.Fatalf("upstream was called: %s", got.path)
	}
}

func TestCountTokensPassesThroughForAnthropicTarget(t *testing.T) {
	got := &capturedRequest{}
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got.path, got.body = r.URL.Path, string(b)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"input_tokens":42}`)
	}))
	t.Cleanup(up.Close)

	gw := startGateway(t, matrixConfig(up.URL, provider.Anthropic))
	status, body := postGateway(t, gw, "/v1/messages/count_tokens", countTokensBody)
	if status != http.StatusOK {
		t.Fatalf("status = %d body = %s", status, body)
	}
	if n := inputTokens(t, body); n != 42 {
		t.Fatalf("input_tokens = %d, want 42", n)
	}
	if got.path != "/v1/messages/count_tokens" {
		t.Fatalf("upstream path = %q", got.path)
	}
	var forwarded map[string]any
	if err := json.Unmarshal([]byte(got.body), &forwarded); err != nil {
		t.Fatalf("upstream body is not JSON: %v\n%s", err, got.body)
	}
	if forwarded["model"] != "deepseek-v4-flash" {
		t.Fatalf("upstream model = %v, want the node model deepseek-v4-flash", forwarded["model"])
	}
	if _, ok := forwarded["messages"]; !ok {
		t.Fatalf("upstream body lost messages: %s", got.body)
	}
}

func TestCountTokensMixedRouteEstimates(t *testing.T) {
	chatUp, chatGot := captureUpstream(t)
	anthUp, anthGot := captureUpstream(t)

	cfg := matrixConfig(chatUp.URL, provider.Chat)
	cfg.Providers = append(cfg.Providers, gwconfig.Provider{
		Name: "anth", Type: "generic", Keys: []string{"k"}, Anthropic: anthUp.URL,
	})
	cfg.Nodes = append(cfg.Nodes, gwconfig.Node{Name: "anth", Provider: "anth", Model: "deepseek-v4-flash"})
	cfg.Routes[0].Nodes = append(cfg.Routes[0].Nodes, "anth")

	gw := startGateway(t, cfg)
	status, body := postGateway(t, gw, "/v1/messages/count_tokens", countTokensBody)
	if status != http.StatusOK {
		t.Fatalf("status = %d body = %s", status, body)
	}
	if n := inputTokens(t, body); n < 1 {
		t.Fatalf("input_tokens = %d, want >= 1", n)
	}
	if chatGot.path != "" || anthGot.path != "" {
		t.Fatalf("upstream was called: chat=%q anth=%q", chatGot.path, anthGot.path)
	}
}

func TestCountTokensQueryString(t *testing.T) {
	up, got := captureUpstream(t)
	gw := startGateway(t, matrixConfig(up.URL, provider.Chat))

	status, body := postGateway(t, gw, "/v1/messages/count_tokens?beta=true", countTokensBody)
	if status != http.StatusOK {
		t.Fatalf("status = %d body = %s", status, body)
	}
	if n := inputTokens(t, body); n < 1 {
		t.Fatalf("input_tokens = %d, want >= 1", n)
	}
	if got.path != "" {
		t.Fatalf("upstream was called: %s", got.path)
	}
}

func TestCountTokensInvalidBodyPassesThroughOnChatTarget(t *testing.T) {
	up, got := captureUpstream(t)
	gw := startGateway(t, matrixConfig(up.URL, provider.Chat))

	status, _ := postGateway(t, gw, "/v1/messages/count_tokens", "not-json")
	if status != http.StatusOK {
		t.Fatalf("status = %d", status)
	}
	if got.body != "not-json" {
		t.Fatalf("upstream body = %q, want the invalid body unchanged", got.body)
	}
}

func TestCountTokensSequentialRequests(t *testing.T) {
	up, got := captureUpstream(t)
	gw := startGateway(t, matrixConfig(up.URL, provider.Chat))

	for i := 0; i < 2; i++ {
		status, body := postGateway(t, gw, "/v1/messages/count_tokens", countTokensBody)
		if status != http.StatusOK {
			t.Fatalf("request %d: status = %d body = %s", i, status, body)
		}
	}
	if got.path != "" {
		t.Fatalf("upstream was called: %s", got.path)
	}
}

func TestCountTokensWritesNoStats(t *testing.T) {
	up, _ := captureUpstream(t)
	cfg := matrixConfig(up.URL, provider.Chat)
	cfg.Stats = gwconfig.Stats{Path: filepath.Join(t.TempDir(), "stats.sqlite")}
	gw := startGateway(t, cfg)

	if status, body := postGateway(t, gw, "/v1/messages/count_tokens", countTokensBody); status != http.StatusOK {
		t.Fatalf("status = %d body = %s", status, body)
	}
	rows, err := gw.Stats().Query(context.Background(), stats.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("stats rows = %d, want 0", len(rows))
	}
}

func TestCountTokensReloadFlipsPolicy(t *testing.T) {
	chatUp, chatGot := captureUpstream(t)
	anthGot := &capturedRequest{}
	anthUp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		anthGot.path, anthGot.body = r.URL.Path, string(b)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"input_tokens":42}`)
	}))
	t.Cleanup(anthUp.Close)

	gw := startGateway(t, matrixConfig(chatUp.URL, provider.Chat))
	if status, body := postGateway(t, gw, "/v1/messages/count_tokens", countTokensBody); status != http.StatusOK {
		t.Fatalf("before reload: status = %d body = %s", status, body)
	}
	if chatGot.path != "" {
		t.Fatalf("before reload: upstream was called: %s", chatGot.path)
	}

	if err := gw.Reload(matrixConfig(anthUp.URL, provider.Anthropic)); err != nil {
		t.Fatal(err)
	}
	status, body := postGateway(t, gw, "/v1/messages/count_tokens", countTokensBody)
	if status != http.StatusOK {
		t.Fatalf("after reload: status = %d body = %s", status, body)
	}
	if n := inputTokens(t, body); n != 42 {
		t.Fatalf("after reload: input_tokens = %d, want 42", n)
	}
	if anthGot.path != "/v1/messages/count_tokens" {
		t.Fatalf("after reload: upstream path = %q", anthGot.path)
	}
}
