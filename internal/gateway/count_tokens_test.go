package gateway

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-gost/core/handler"
	"github.com/go-gost/core/metadata"
	xlogger "github.com/go-gost/x/logger"

	gwconfig "github.com/ginuerzh/cuttle/internal/config"
	"github.com/ginuerzh/cuttle/internal/provider"
	"github.com/ginuerzh/cuttle/internal/stats"
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
		ID: "p_0000000002", Name: "anth", Type: "generic",
		Keys: []gwconfig.ProviderKey{{ID: "pk_0000000002", Name: "key-1", Key: "k"}}, Anthropic: anthUp.URL,
	})
	cfg.Nodes = append(cfg.Nodes, gwconfig.Node{ID: "n_0000000002", Name: "anth", Provider: "p_0000000002", Model: "deepseek-v4-flash"})
	cfg.Routes[0].Nodes = append(cfg.Routes[0].Nodes, "n_0000000002")

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

type blockingHandler struct{}

func (h *blockingHandler) Init(metadata.Metadata) error { return nil }

func (h *blockingHandler) Handle(context.Context, net.Conn, ...handler.HandleOption) error {
	return nil
}

func TestCountTokensHeaderReadTimeout(t *testing.T) {
	old := countTokensReadTimeout
	countTokensReadTimeout = 100 * time.Millisecond
	defer func() { countTokensReadTimeout = old }()

	server, client := net.Pipe()
	defer client.Close()

	var policy atomic.Pointer[countTokensPolicy]
	h := newCountTokensHandler(&blockingHandler{}, &policy, xlogger.Nop())

	done := make(chan error, 1)
	go func() { done <- h.Handle(context.Background(), server) }()

	if _, err := io.WriteString(client, "POST /v1/messages/count_tokens HTTP/1.1\r\n"); err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Handle returned nil, want a read timeout")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Handle blocked past the read deadline")
	}
}

func TestCountTokensBodyTooLarge(t *testing.T) {
	old := countTokensBodyLimit
	countTokensBodyLimit = 16
	defer func() { countTokensBodyLimit = old }()

	up, got := captureUpstream(t)
	gw := startGateway(t, matrixConfig(up.URL, provider.Chat))

	status, body := postGateway(t, gw, "/v1/messages/count_tokens", countTokensBody)
	if status != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d body = %s", status, body)
	}
	if got.path != "" {
		t.Fatalf("upstream was called: %s", got.path)
	}
}

func TestCountTokensExpectContinue(t *testing.T) {
	up, got := captureUpstream(t)
	gw := startGateway(t, matrixConfig(up.URL, provider.Chat))

	req, _ := http.NewRequest(http.MethodPost, "http://"+gw.Addr().String()+"/v1/messages/count_tokens", strings.NewReader(countTokensBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Expect", "100-continue")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d body = %s", resp.StatusCode, b)
	}
	if n := inputTokens(t, string(b)); n < 1 {
		t.Fatalf("input_tokens = %d, want >= 1", n)
	}
	if got.path != "" {
		t.Fatalf("upstream was called: %s", got.path)
	}
}

func TestCountTokensCompressedBodyPassesThrough(t *testing.T) {
	up, got := captureUpstream(t)
	gw := startGateway(t, matrixConfig(up.URL, provider.Chat))

	var compressed bytes.Buffer
	zw := gzip.NewWriter(&compressed)
	if _, err := zw.Write([]byte(countTokensBody)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	req, _ := http.NewRequest(http.MethodPost, "http://"+gw.Addr().String()+"/v1/messages/count_tokens", bytes.NewReader(compressed.Bytes()))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Encoding", "gzip")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	if got.path != "/v1/messages/count_tokens" {
		t.Fatalf("upstream path = %q, want the compressed request to pass through", got.path)
	}
	if got.body != compressed.String() {
		t.Fatalf("upstream body = %q, want the compressed bytes unchanged", got.body)
	}
}

func TestCountTokensChunkedBody(t *testing.T) {
	up, got := captureUpstream(t)
	gw := startGateway(t, matrixConfig(up.URL, provider.Chat))

	req, _ := http.NewRequest(http.MethodPost, "http://"+gw.Addr().String()+"/v1/messages/count_tokens", io.NopCloser(strings.NewReader(countTokensBody)))
	req.ContentLength = -1
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d body = %s", resp.StatusCode, b)
	}
	if n := inputTokens(t, string(b)); n < 1 {
		t.Fatalf("input_tokens = %d, want >= 1", n)
	}
	if got.path != "" {
		t.Fatalf("upstream was called: %s", got.path)
	}
}

func TestGatewayReplaysLargeNonCountBody(t *testing.T) {
	up, got := captureUpstream(t)
	gw := startGateway(t, matrixConfig(up.URL, provider.Chat))

	body := `{"model":"claude-opus-4-5","messages":[{"role":"user","content":"` + strings.Repeat("x", 200_000) + `"}]}`
	status, resp := postGateway(t, gw, "/v1/messages", body)
	if status != http.StatusOK {
		t.Fatalf("status = %d body = %s", status, resp)
	}
	if got.path != "/chat/completions" {
		t.Fatalf("upstream path = %q", got.path)
	}
	if len(got.body) < 200_000 {
		t.Fatalf("upstream body length = %d, want >= 200000 (truncated replay)", len(got.body))
	}
}

func TestCountTokensLocalResponseClosesConnection(t *testing.T) {
	up, _ := captureUpstream(t)
	gw := startGateway(t, matrixConfig(up.URL, provider.Chat))

	conn, err := net.Dial("tcp", gw.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	fmt.Fprintf(conn, "POST /v1/messages/count_tokens HTTP/1.1\r\nHost: %s\r\nContent-Type: application/json\r\nContent-Length: %d\r\n\r\n%s",
		gw.Addr().String(), len(countTokensBody), countTokensBody)

	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if !resp.Close {
		t.Fatal("local count_tokens response does not advertise Connection: close")
	}
	io.Copy(io.Discard, resp.Body)

	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := conn.Read(make([]byte, 1)); err == nil {
		t.Fatal("connection stayed open after a local count_tokens response")
	}
}

func TestGatewayAdvertisesConnectionClose(t *testing.T) {
	up, _ := captureUpstream(t)
	gw := startGateway(t, matrixConfig(up.URL, provider.Chat))

	conn, err := net.Dial("tcp", gw.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	body := `{"model":"claude-opus-4-5","messages":[{"role":"user","content":"hi"}]}`
	fmt.Fprintf(conn, "POST /v1/messages HTTP/1.1\r\nHost: %s\r\nContent-Type: application/json\r\nContent-Length: %d\r\n\r\n%s",
		gw.Addr().String(), len(body), body)

	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if !resp.Close {
		t.Fatal("node-forwarded response does not advertise Connection: close")
	}
}
