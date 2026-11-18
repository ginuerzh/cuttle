package rewriter

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ginuerzh/llm-api-converter/engine"
	corerewriter "github.com/go-gost/core/rewriter"

	"github.com/ginuerzh/cuttle/internal/capture"
)

func TestClampMaxTokensProtocols(t *testing.T) {
	cases := []struct {
		name, protocol, body string
		field                []string
		wantSibling          string
	}{
		{"anthropic", "anthropic", `{"max_tokens":256000,"system":"keep <anthropic> & more","messages":[]}`, []string{"max_tokens"}, "keep <anthropic> & more"},
		{"chat max_tokens", "chat", `{"max_tokens":256000,"system":"keep <chat> & more","messages":[]}`, []string{"max_tokens"}, "keep <chat> & more"},
		{"chat completion tokens", "chat", `{"max_completion_tokens":256000,"system":"keep <completion> & more","messages":[]}`, []string{"max_completion_tokens"}, "keep <completion> & more"},
		{"responses", "responses", `{"max_output_tokens":256000,"system":"keep <responses> & more","messages":[]}`, []string{"max_output_tokens"}, "keep <responses> & more"},
		{"gemini", "gemini", `{"generationConfig":{"maxOutputTokens":256000},"system":"keep <gemini> & more"}`, []string{"generationConfig", "maxOutputTokens"}, "keep <gemini> & more"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := clampMaxTokens([]byte(tc.body), tc.protocol, 131072)
			if err != nil {
				t.Fatalf("clampMaxTokens(%s, %q, 131072) error = %v", tc.body, tc.protocol, err)
			}
			if got := nestedInt(t, out, tc.field); got != 131072 {
				t.Fatalf("clampMaxTokens(%s, %q, 131072) field %v = %d, want 131072; body %s", tc.body, tc.protocol, tc.field, got, out)
			}
			if got := nestedString(t, out, []string{"system"}); got != tc.wantSibling {
				t.Fatalf("clampMaxTokens(%s, %q, 131072) system = %q, want %q; body %s", tc.body, tc.protocol, got, tc.wantSibling, out)
			}
		})
	}
}

func TestClampMaxTokensNoops(t *testing.T) {
	cases := []struct {
		name, protocol, body string
		cap                  int
	}{
		{"value below cap", "chat", `{"max_tokens":8192}`, 131072},
		{"exact cap", "chat", `{"max_tokens":131072}`, 131072},
		{"missing field", "chat", `{"messages":[]}`, 131072},
		{"string value", "chat", `{"max_tokens":"big"}`, 131072},
		{"fractional value", "chat", `{"max_tokens":256000.0}`, 131072},
		{"negative value", "anthropic", `{"max_tokens":-1}`, 131072},
		{"first candidate wins", "chat", `{"max_tokens":8192,"max_completion_tokens":256000}`, 131072},
		{"malformed gemini config", "gemini", `{"generationConfig":"bad"}`, 131072},
		{"non-integer gemini value", "gemini", `{"generationConfig":{"maxOutputTokens":"big"}}`, 131072},
		{"unknown protocol", "unknown", `{"max_tokens":256000}`, 131072},
		{"invalid JSON", "chat", `nope`, 131072},
		{"zero cap", "chat", `{"max_tokens":256000}`, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := clampMaxTokens([]byte(tc.body), tc.protocol, tc.cap)
			if err != nil {
				t.Fatalf("clampMaxTokens(%s, %q, %d) error = %v", tc.body, tc.protocol, tc.cap, err)
			}
			if string(out) != tc.body {
				t.Fatalf("clampMaxTokens(%s, %q, %d) = %s, want %s", tc.body, tc.protocol, tc.cap, out, tc.body)
			}
		})
	}
}

func TestRewriteClampsBeforeCapture(t *testing.T) {
	capMgr := capture.NewManager(capture.Policy{Mode: "errors", MaxBytes: 1 << 20})
	eng := engine.New(engine.Options{
		Model:        "glm-5.3",
		ResolveModel: func(string, string) (string, string, string) { return "glm-5.3", "anthropic", "" },
	})
	rw := New(eng, Options{Protocol: "anthropic", MaxOutputTokens: 131072, Capture: capMgr, Sink: &sinkStub{}})
	body := `{"model":"claude-opus-5-5","max_tokens":256000,"messages":[{"role":"user","content":"hi"}]}`
	out, err := rw.Rewrite(context.Background(), []byte(body),
		corerewriter.MetadataRewriteOption(map[string]any{"sid": "s1", "direction": "request", "uri": "/v1/chat/completions"}))
	if err != nil {
		t.Fatalf("Rewrite(%s) error = %v", body, err)
	}
	if got := nestedInt(t, out, []string{"max_tokens"}); got != 131072 {
		t.Fatalf("Rewrite(%s) max_tokens = %d, want 131072; body %s", body, got, out)
	}
	cap, ok := capMgr.Finish("s1")
	if !ok {
		t.Fatalf("Rewrite(%s) capture missing", body)
	}
	if !strings.Contains(cap.RequestRewritten, `"max_tokens":131072`) {
		t.Fatalf("Rewrite(%s) captured rewritten = %q, want clamped max_tokens", body, cap.RequestRewritten)
	}
}

func TestRewriteClampSkipsResponseAndCountTokens(t *testing.T) {
	cases := []struct {
		name, direction, uri, body string
	}{
		{"response", "response", "/v1/chat/completions", `{"max_tokens":256000}`},
		{"count tokens", "request", "/v1/messages/count_tokens", `{"model":"claude-opus-5-5","max_tokens":256000,"messages":[{"role":"user","content":"hi"}]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			eng := engine.New(engine.Options{
				ResolveModel: func(string, string) (string, string, string) { return "glm-5.3", "anthropic", "" },
			})
			rw := New(eng, Options{Protocol: "anthropic", MaxOutputTokens: 131072})
			out, err := rw.Rewrite(context.Background(), []byte(tc.body),
				corerewriter.MetadataRewriteOption(map[string]any{"sid": "s1", "direction": tc.direction, "uri": tc.uri}))
			if err != nil {
				t.Fatalf("Rewrite(%s, %q) error = %v", tc.body, tc.uri, err)
			}
			if string(out) != tc.body {
				t.Fatalf("Rewrite(%s, %q) = %s, want %s", tc.body, tc.uri, out, tc.body)
			}
			if got := nestedInt(t, out, []string{"max_tokens"}); got != 256000 {
				t.Fatalf("Rewrite(%s, %q) max_tokens = %d, want 256000; body %s", tc.body, tc.uri, got, out)
			}
		})
	}
}

func nestedString(t *testing.T, body []byte, path []string) string {
	t.Helper()
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	for _, key := range path {
		obj, ok := v.(map[string]any)
		if !ok {
			t.Fatalf("body %s: %q is not an object", body, key)
		}
		v, ok = obj[key]
		if !ok {
			t.Fatalf("body %s: field %q missing", body, key)
		}
	}
	s, ok := v.(string)
	if !ok {
		t.Fatalf("body %s: field %v = %v, want string", body, path, v)
	}
	return s
}

func nestedInt(t *testing.T, body []byte, path []string) int64 {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	for _, key := range path {
		obj, ok := v.(map[string]any)
		if !ok {
			t.Fatalf("body %s: %q is not an object", body, key)
		}
		v, ok = obj[key]
		if !ok {
			t.Fatalf("body %s: field %q missing", body, key)
		}
	}
	num, ok := v.(json.Number)
	if !ok {
		t.Fatalf("body %s: field %v = %v, want integer", body, path, v)
	}
	n, err := num.Int64()
	if err != nil {
		t.Fatalf("body %s: field %v = %v, want integer: %v", body, path, num, err)
	}
	return n
}
