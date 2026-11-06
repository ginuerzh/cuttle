package gateway

import (
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestAnthropicStreamConversionWithCompressedUpstream reproduces the DeepSeek
// failure mode: the client advertises Accept-Encoding, the upstream compresses
// the SSE body, and the gateway used to forward it untouched because the x
// forwarder cannot rewrite compressed SSE. Claude Code then sees OpenAI chunks
// instead of Anthropic events and reports "Stream ended without receiving any
// events".
func TestAnthropicStreamConversionWithCompressedUpstream(t *testing.T) {
	chunks := []string{
		`{"id":"1","object":"chat.completion.chunk","created":1,"model":"deepseek-chat","choices":[{"index":0,"delta":{"role":"assistant","content":null,"reasoning_content":""},"finish_reason":null}],"usage":null}`,
		`{"id":"1","object":"chat.completion.chunk","created":1,"model":"deepseek-chat","choices":[{"index":0,"delta":{"content":"Hi"},"finish_reason":null}],"usage":null}`,
		`{"id":"1","object":"chat.completion.chunk","created":1,"model":"deepseek-chat","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}}`,
	}

	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		var out io.Writer = w
		var gz *gzip.Writer
		if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			w.Header().Set("Content-Encoding", "gzip")
			gz = gzip.NewWriter(w)
			defer gz.Close()
			out = gz
		}
		fl, _ := w.(http.Flusher)
		for _, c := range chunks {
			fmt.Fprintf(out, "data: %s\n\n", c)
			if gz != nil {
				gz.Flush()
			}
			if fl != nil {
				fl.Flush()
			}
		}
		fmt.Fprint(out, "data: [DONE]\n\n")
	}))
	defer up.Close()

	gw := startGateway(t, testConfig(up.URL))
	body := `{"model":"claude-sonnet-4-5","max_tokens":64,"stream":true,"messages":[{"role":"user","content":"hi"}]}`
	req, err := http.NewRequest(http.MethodPost, "http://"+gw.Addr().String()+"/v1/messages", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	// Set the header explicitly so net/http does not transparently decompress
	// the response for us: the gateway must not forward it upstream.
	req.Header.Set("Accept-Encoding", "gzip")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if ce := resp.Header.Get("Content-Encoding"); ce != "" {
		t.Fatalf("response is still compressed (%s); the rewriter was bypassed", ce)
	}
	if !strings.Contains(string(got), "event: message_start") {
		t.Fatalf("response is not Anthropic SSE:\n%s", got)
	}
	if strings.Contains(string(got), `"object":"chat.completion.chunk"`) {
		t.Fatalf("response still contains OpenAI chunks:\n%s", got)
	}
}
