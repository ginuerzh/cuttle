package rewriter

import (
	"encoding/json"
	"testing"
)

func TestReplaceModelField(t *testing.T) {
	in := []byte(`{"model":"claude-sonnet","messages":[{"role":"user","content":"hi"}],"n":1}`)
	out := replaceModelField(in, "deepseek-v4-flash")

	var v map[string]json.RawMessage
	if err := json.Unmarshal(out, &v); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	var model string
	if err := json.Unmarshal(v["model"], &model); err != nil || model != "deepseek-v4-flash" {
		t.Fatalf("model = %q (err %v), want deepseek-v4-flash", model, err)
	}
	if string(v["messages"]) != `[{"role":"user","content":"hi"}]` {
		t.Fatalf("messages changed: %s", v["messages"])
	}
	if string(v["n"]) != "1" {
		t.Fatalf("n changed: %s", v["n"])
	}

	bad := []byte("not-json")
	if got := replaceModelField(bad, "target"); string(got) != string(bad) {
		t.Fatalf("invalid JSON changed: %s", got)
	}
	if got := replaceModelField(in, ""); string(got) != string(in) {
		t.Fatalf("empty target changed the body: %s", got)
	}
}

func TestIsCountTokensURI(t *testing.T) {
	for _, uri := range []string{"/v1/messages/count_tokens", "/v1/messages/count_tokens?beta=true"} {
		if !isCountTokensURI(uri) {
			t.Fatalf("isCountTokensURI(%q) = false, want true", uri)
		}
	}
	for _, uri := range []string{"/v1/messages", "/v1/chat/completions", "/v1/messages/count_tokens/extra", ""} {
		if isCountTokensURI(uri) {
			t.Fatalf("isCountTokensURI(%q) = true, want false", uri)
		}
	}
}
