package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gwconfig "github.com/go-gost/llm-gw/internal/config"
	"github.com/go-gost/llm-gw/internal/provider"
)

type capturedRequest struct {
	path string
	body string
}

const openAIChatResponse = `{"id":"x","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`

const anthropicResponse = `{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],"model":"m","stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`

func captureUpstream(t *testing.T) (*httptest.Server, *capturedRequest) {
	t.Helper()

	got := &capturedRequest{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got.path = r.URL.Path
		got.body = string(b)

		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/messages") {
			io.WriteString(w, anthropicResponse)
			return
		}
		io.WriteString(w, openAIChatResponse)
	}))
	t.Cleanup(srv.Close)
	return srv, got
}

func matrixConfig(upstreamURL string, api provider.API) *gwconfig.Config {
	p := gwconfig.Provider{
		Name: "up",
		Type: "generic",
		Keys: []string{"sk-test"},
	}
	switch api {
	case provider.Anthropic:
		p.Anthropic = upstreamURL
	default:
		p.Chat = upstreamURL
	}
	return &gwconfig.Config{
		Gateway: gwconfig.Gateway{Listen: "127.0.0.1:0"},
		Aliases: []gwconfig.Alias{
			{Match: "claude-opus*", Model: "strong"},
			{Match: "gpt-5.5*", Model: "strong"},
			{Match: "gpt-4.5*", Model: "fast"},
			{Match: "*", Model: "fast"},
		},
		Providers: []gwconfig.Provider{p},
		Nodes: []gwconfig.Node{{
			Name:     "up",
			Provider: "up",
			Models: map[string]string{
				"strong": "deepseek-v4-pro",
				"fast":   "deepseek-v4-flash",
				"*":      "deepseek-v4-flash",
			},
		}},
		Routes: []gwconfig.Route{{Name: "r", Nodes: []string{"up"}}},
	}
}

func postThroughGateway(t *testing.T, gw *Gateway, path, body string) {
	t.Helper()

	req, _ := http.NewRequest(http.MethodPost, "http://"+gw.Addr().String()+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
}

func decodeBody(t *testing.T, body string) map[string]any {
	t.Helper()

	var v map[string]any
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		t.Fatalf("upstream body is not JSON: %v\n%s", err, body)
	}
	return v
}

func TestProtocolMatrix(t *testing.T) {
	const anthropicBody = `{"model":"claude-opus-4-5","max_tokens":32,"messages":[{"role":"user","content":"hi"}]}`
	const openAIBody = `{"model":"gpt-4.5","messages":[{"role":"user","content":"hi"}]}`
	const responsesBody = `{"model":"gpt-5.5","input":"hi"}`

	t.Run("anthropic client to openai wire", func(t *testing.T) {
		up, got := captureUpstream(t)
		gw := startGateway(t, matrixConfig(up.URL, provider.Chat))

		postThroughGateway(t, gw, "/v1/messages", anthropicBody)

		if got.path != "/chat/completions" {
			t.Fatalf("path = %q", got.path)
		}
		v := decodeBody(t, got.body)
		if v["model"] != "deepseek-v4-flash" {
			t.Fatalf("model = %v, want deepseek-v4-flash (catch-all * mapping)", v["model"])
		}
		if _, ok := v["messages"]; !ok {
			t.Fatalf("want OpenAI chat body, got %s", got.body)
		}
	})

	t.Run("anthropic client to anthropic wire", func(t *testing.T) {
		up, got := captureUpstream(t)
		gw := startGateway(t, matrixConfig(up.URL, provider.Anthropic))

		postThroughGateway(t, gw, "/v1/messages", anthropicBody)

		if got.path != "/v1/messages" {
			t.Fatalf("path = %q", got.path)
		}
		v := decodeBody(t, got.body)
		if v["model"] != "deepseek-v4-flash" {
			t.Fatalf("model = %v, want deepseek-v4-flash even on passthrough", v["model"])
		}
		if _, ok := v["max_tokens"]; !ok {
			t.Fatalf("want Anthropic body, got %s", got.body)
		}
	})

	t.Run("openai client to anthropic wire", func(t *testing.T) {
		up, got := captureUpstream(t)
		gw := startGateway(t, matrixConfig(up.URL, provider.Anthropic))

		postThroughGateway(t, gw, "/v1/chat/completions", openAIBody)

		if got.path != "/v1/messages" {
			t.Fatalf("path = %q", got.path)
		}
		v := decodeBody(t, got.body)
		if v["model"] != "deepseek-v4-flash" {
			t.Fatalf("model = %v, want deepseek-v4-flash", v["model"])
		}
		if _, ok := v["max_tokens"]; !ok {
			t.Fatalf("want Anthropic body, got %s", got.body)
		}
	})

	t.Run("responses client to openai wire", func(t *testing.T) {
		up, got := captureUpstream(t)
		gw := startGateway(t, matrixConfig(up.URL, provider.Chat))

		postThroughGateway(t, gw, "/v1/responses", responsesBody)

		if got.path != "/chat/completions" {
			t.Fatalf("path = %q", got.path)
		}
		v := decodeBody(t, got.body)
		if v["model"] != "deepseek-v4-flash" {
			t.Fatalf("model = %v, want deepseek-v4-flash (catch-all * mapping)", v["model"])
		}
		if _, ok := v["messages"]; !ok {
			t.Fatalf("want Chat Completions body (input converted), got %s", got.body)
		}
	})

	t.Run("count_tokens is not converted", func(t *testing.T) {
		up, got := captureUpstream(t)
		gw := startGateway(t, matrixConfig(up.URL, provider.Chat))

		body := `{"model":"claude-opus-4-5","messages":[{"role":"user","content":"hi"}]}`
		postThroughGateway(t, gw, "/v1/messages/count_tokens", body)

		if got.path != "/v1/messages/count_tokens" {
			t.Fatalf("path = %q, want pass-through", got.path)
		}
		if got.body != body {
			t.Fatalf("body changed for count_tokens: %s", got.body)
		}
	})

	t.Run("provider without models keeps the client model", func(t *testing.T) {
		up, got := captureUpstream(t)
		cfg := matrixConfig(up.URL, provider.Chat)
		cfg.Aliases = nil
		cfg.Nodes[0].Models = nil
		gw := startGateway(t, cfg)

		body := `{"model":"gpt-4.5","messages":[{"role":"user","content":"hi"}]}`
		postThroughGateway(t, gw, "/v1/chat/completions", body)

		v := decodeBody(t, got.body)
		if v["model"] != "gpt-4.5" {
			t.Fatalf("model = %v, want gpt-4.5", v["model"])
		}
	})
}
