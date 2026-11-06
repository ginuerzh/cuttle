package rewriter

import "testing"

func TestInboundProtocol(t *testing.T) {
	cases := map[string]string{
		"/v1/chat/completions":                      "openai-chat",
		"/v1/chat/completions?stream=true":          "openai-chat",
		"/v1/responses":                             "openai-responses",
		"/v1/messages":                              "anthropic",
		"/v1beta/models/gemini-2.0:generateContent": "gemini",
		"/v1beta/models/x:streamGenerateContent":    "gemini",
		"/unknown":                                  "",
		"":                                          "",
	}
	for uri, want := range cases {
		if got := inboundProtocol(uri); got != want {
			t.Fatalf("inboundProtocol(%q) = %q, want %q", uri, got, want)
		}
	}
}

func TestRewriteStage(t *testing.T) {
	if got := rewriteStage("request", ""); got != "request_rewrite" {
		t.Fatalf("request stage = %q", got)
	}
	if got := rewriteStage("response", ""); got != "response_rewrite" {
		t.Fatalf("response stage = %q", got)
	}
	if got := rewriteStage("response", "event"); got != "stream_rewrite" {
		t.Fatalf("stream stage = %q", got)
	}
}
