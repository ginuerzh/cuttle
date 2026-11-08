package rewriter

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/ginuerzh/llm-api-converter/engine"
	corerewriter "github.com/go-gost/core/rewriter"
)

type sinkStub struct {
	sid, message, typ string
	effort            string
	finish            string
	done              bool
	marked            int
	markedAt          time.Time
}

func (s *sinkStub) Add(_, _, _ string, res engine.Result)               { s.effort = res.Effort }
func (s *sinkStub) AddInboundProtocol(string, string)                   {}
func (s *sinkStub) AddRewriteError(string, string, string, string, int) {}
func (s *sinkStub) AddUpstreamError(sid, message, typ string) {
	s.sid, s.message, s.typ = sid, message, typ
}

func (s *sinkStub) AddStreamState(sid, finishReason string, sawDone bool) {
	s.finish, s.done = finishReason, sawDone
}

func (s *sinkStub) MarkFirstResponse(sid string, at time.Time) {
	if s.marked == 0 {
		s.markedAt = at
	}
	s.marked++
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

func TestRewriteMarksFirstResponseOnce(t *testing.T) {
	sink := &sinkStub{}
	rw := New(engine.New(engine.Options{}), Options{Sink: sink, Protocol: "chat"})
	meta := func(direction, phase string) corerewriter.RewriteOption {
		return corerewriter.MetadataRewriteOption(map[string]any{
			"sid": "sid-1", "direction": direction,
			"uri": "/v1/chat/completions", "sse_phase": phase,
		})
	}

	if _, err := rw.Rewrite(context.Background(), []byte(`data: {"choices":[]}`), meta("response", "start")); err != nil {
		t.Fatal(err)
	}
	first := sink.markedAt
	if sink.marked != 1 || first.IsZero() {
		t.Fatalf("after start: marked=%d at=%v", sink.marked, first)
	}

	if _, err := rw.Rewrite(context.Background(), []byte(`data: {"choices":[]}`), meta("response", "event")); err != nil {
		t.Fatal(err)
	}
	if sink.marked != 2 || !sink.markedAt.Equal(first) {
		t.Fatalf("after event: marked=%d at=%v, want first %v", sink.marked, sink.markedAt, first)
	}

	if _, err := rw.Rewrite(context.Background(), nil, meta("response", "end")); err != nil {
		t.Fatal(err)
	}
	if _, err := rw.Rewrite(context.Background(), []byte(`{}`), meta("request", "")); err != nil {
		t.Fatal(err)
	}
	if sink.marked != 2 {
		t.Fatalf("end/request marked first response: %d", sink.marked)
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

func TestRewritePreservesExplicitClientEffortLevel(t *testing.T) {
	cases := []struct {
		name string
		uri  string
		body string
		want string
	}{
		{
			name: "anthropic max",
			uri:  "/v1/messages",
			body: `{"model":"claude-sonnet-5","output_config":{"effort":"max"},"messages":[{"role":"user","content":"hi"}]}`,
			want: "max",
		},
		{
			name: "anthropic xhigh",
			uri:  "/v1/messages",
			body: `{"model":"claude-sonnet-5","output_config":{"effort":"xhigh"},"messages":[{"role":"user","content":"hi"}]}`,
			want: "xhigh",
		},
		{
			name: "openai reasoning_effort max",
			uri:  "/v1/chat/completions",
			body: `{"model":"gpt-5","reasoning_effort":"max","messages":[{"role":"user","content":"hi"}]}`,
			want: "max",
		},
		{
			name: "reasoning effort max",
			uri:  "/v1/responses",
			body: `{"model":"gpt-5","reasoning":{"effort":"max"},"messages":[{"role":"user","content":"hi"}]}`,
			want: "max",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sink := &sinkStub{}
			rw := New(engine.New(engine.Options{}), Options{Sink: sink, Protocol: "chat"})
			_, err := rw.Rewrite(context.Background(), []byte(tc.body),
				corerewriter.MetadataRewriteOption(map[string]any{
					"sid": "sid-1", "direction": "request", "uri": tc.uri,
				}))
			if err != nil {
				t.Fatal(err)
			}
			if sink.effort != tc.want {
				t.Fatalf("effort = %q, want %q", sink.effort, tc.want)
			}
		})
	}
}

func TestRewriteKeepsEngineEffortBand(t *testing.T) {
	sink := &sinkStub{}
	rw := New(engine.New(engine.Options{}), Options{Sink: sink, Protocol: "chat"})
	_, err := rw.Rewrite(context.Background(),
		[]byte(`{"model":"claude-sonnet","thinking":{"type":"enabled","budget_tokens":20000},"messages":[{"role":"user","content":"hi"}]}`),
		corerewriter.MetadataRewriteOption(map[string]any{
			"sid": "sid-1", "direction": "request", "uri": "/v1/messages",
		}))
	if err != nil {
		t.Fatal(err)
	}
	if sink.effort != "high" {
		t.Fatalf("effort = %q, want high", sink.effort)
	}
}

func TestRequestEffort(t *testing.T) {
	cases := []struct {
		name      string
		direction string
		body      string
		want      string
	}{
		{"output_config max", "request", `{"output_config":{"effort":"max"}}`, "max"},
		{"output_config xhigh", "request", `{"output_config":{"effort":"xhigh"}}`, "xhigh"},
		{"reasoning_effort high", "request", `{"reasoning_effort":"high"}`, "high"},
		{"minimal becomes low", "request", `{"reasoning_effort":"minimal"}`, "low"},
		{"gemini high", "request", `{"generationConfig":{"thinkingConfig":{"thinkingLevel":"high"}}}`, "high"},
		{"unknown", "request", `{"reasoning_effort":"extreme"}`, ""},
		{"response direction", "response", `{"output_config":{"effort":"max"}}`, ""},
		{"not json", "request", `nope`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := requestEffort(tc.direction, []byte(tc.body)); got != tc.want {
				t.Fatalf("requestEffort = %q, want %q", got, tc.want)
			}
		})
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
