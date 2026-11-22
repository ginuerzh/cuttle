package gateway

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

// chatStreamRequest is the client shape opencode uses against the gateway: an
// OpenAI chat stream that asks for the usage chunk.
const chatStreamRequest = `{"model":"claude-sonnet-4-5","stream":true,"stream_options":{"include_usage":true},"messages":[{"role":"user","content":"hi"}]}`

const (
	chatRoleChunk    = `{"id":"1","object":"chat.completion.chunk","created":1,"model":"deepseek-chat","choices":[{"index":0,"delta":{"role":"assistant","content":null},"finish_reason":null}],"usage":null}`
	chatContentChunk = `{"id":"1","object":"chat.completion.chunk","created":1,"model":"deepseek-chat","choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":null}],"usage":null}`
	chatFinishChunk  = `{"id":"1","object":"chat.completion.chunk","created":1,"model":"deepseek-chat","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":null}`
	chatUsageChunk   = `{"id":"1","object":"chat.completion.chunk","created":1,"model":"deepseek-chat","choices":[],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`
)

// chatStream posts the opencode-shaped request and returns the whole SSE body.
// Accept-Encoding: gzip is set explicitly so the gateway's strip-and-forward
// behaviour is exercised together with the end-phase events.
func chatStream(t *testing.T, gw *Gateway) string {
	t.Helper()

	req, err := http.NewRequest(http.MethodPost, "http://"+gw.Addr().String()+"/v1/chat/completions", strings.NewReader(chatStreamRequest))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept-Encoding", "gzip")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200: %s", resp.StatusCode, body)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

// TestChatStreamEndsWithDone pins the shape production upstreams use: the
// finish chunk and a usage-only chunk arrive, the upstream sentinel does not.
// The client must still see exactly one [DONE].
func TestChatStreamEndsWithDone(t *testing.T) {
	up := testUpstream(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl, _ := w.(http.Flusher)
		for _, c := range []string{chatRoleChunk, chatContentChunk, chatFinishChunk, chatUsageChunk} {
			fmt.Fprintf(w, "data: %s\n\n", c)
			if fl != nil {
				fl.Flush()
			}
		}
	}))
	defer up.Close()

	gw := startGateway(t, testConfig(up.URL))
	body := chatStream(t, gw)

	if got := strings.Count(body, "data: [DONE]"); got != 1 {
		t.Fatalf("[DONE] count = %d, want 1:\n%s", got, body)
	}
	if !strings.Contains(body, `"finish_reason":"stop"`) {
		t.Fatalf("missing finish_reason in:\n%s", body)
	}
	if strings.Contains(body, "stream_truncated") {
		t.Fatalf("healthy stream reported as truncated:\n%s", body)
	}
}

// TestChatTruncatedStreamReportsError pins the upstream cut: the body ends
// after a content chunk, with no terminal marker. The client must get the
// truncation error instead of a stream that looks finished.
func TestChatTruncatedStreamReportsError(t *testing.T) {
	up := testUpstream(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl, _ := w.(http.Flusher)
		for _, c := range []string{chatRoleChunk, chatContentChunk} {
			fmt.Fprintf(w, "data: %s\n\n", c)
			if fl != nil {
				fl.Flush()
			}
		}
	}))
	defer up.Close()

	gw := startGateway(t, testConfig(up.URL))
	body := chatStream(t, gw)

	if !strings.Contains(body, `"type":"stream_truncated"`) {
		t.Fatalf("missing truncation error in:\n%s", body)
	}
	if !strings.Contains(body, "upstream stream ended without a terminal event") {
		t.Fatalf("missing truncation message in:\n%s", body)
	}
	if strings.Contains(body, "data: [DONE]") {
		t.Fatalf("truncated stream must not carry [DONE]:\n%s", body)
	}
}

// The gateway closes the client connection after every response, so the
// response must say so. A client that pools the socket otherwise reuses a dead
// connection and loses the next stream.
func TestChatStreamAsksForConnectionClose(t *testing.T) {
	up := testUpstream(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: %s\n\n", chatFinishChunk)
	}))
	defer up.Close()

	gw := startGateway(t, testConfig(up.URL))
	req, err := http.NewRequest(http.MethodPost, "http://"+gw.Addr().String()+"/v1/chat/completions", strings.NewReader(chatStreamRequest))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	// net/http turns the header into resp.Close; check the semantics the
	// client acts on, not the raw header text.
	if !resp.Close {
		t.Fatalf("resp.Close = false (Connection header %q), want a non-reusable connection", resp.Header.Get("Connection"))
	}
}
