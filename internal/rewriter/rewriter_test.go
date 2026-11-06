package rewriter

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/ginuerzh/llm-api-converter/engine"
	corerewriter "github.com/go-gost/core/rewriter"
)

type sinkStub struct {
	sid, message, typ string
	finish            string
	done              bool
}

func (s *sinkStub) Add(string, string, string, engine.Result)           {}
func (s *sinkStub) AddInboundProtocol(string, string)                   {}
func (s *sinkStub) AddRewriteError(string, string, string, string, int) {}
func (s *sinkStub) AddUpstreamError(sid, message, typ string) {
	s.sid, s.message, s.typ = sid, message, typ
}

func (s *sinkStub) AddStreamState(sid, finishReason string, sawDone bool) {
	s.finish, s.done = finishReason, sawDone
}

func TestRewriteReportsStreamState(t *testing.T) {
	sink := &sinkStub{}
	rw := New(engine.New(engine.Options{}), Options{Sink: sink, Protocol: "chat"})
	meta := func() corerewriter.RewriteOption {
		return corerewriter.MetadataRewriteOption(map[string]any{
			"sid":       "sid-1",
			"direction": "response",
			"uri":       "/v1/chat/completions",
			"sse_phase": "event",
		})
	}
	if _, err := rw.Rewrite(context.Background(), []byte(`data: {"choices":[{"finish_reason":"stop"}]}`), meta()); err != nil {
		t.Fatal(err)
	}
	if sink.finish != "stop" || sink.done {
		t.Fatalf("sink = %+v", sink)
	}
	if _, err := rw.Rewrite(context.Background(), []byte(`data: [DONE]`), meta()); err != nil {
		t.Fatal(err)
	}
	if !sink.done {
		t.Fatalf("sink = %+v", sink)
	}
}

func TestRewriteReportsUpstreamError(t *testing.T) {
	sink := &sinkStub{}
	rw := New(engine.New(engine.Options{}), Options{Sink: sink, Protocol: "chat"})
	_, err := rw.Rewrite(context.Background(),
		[]byte(`{"error":{"message":"boom","type":"overloaded_error"}}`),
		corerewriter.MetadataRewriteOption(map[string]any{
			"sid":       "sid-1",
			"direction": "response",
			"uri":       "/v1/chat/completions",
		}))
	if err != nil {
		t.Fatal(err)
	}
	if sink.message != "boom" || sink.typ != "overloaded_error" || sink.sid != "sid-1" {
		t.Fatalf("sink = %+v", sink)
	}
}

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
