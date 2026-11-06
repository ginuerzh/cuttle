package stats

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/ginuerzh/llm-api-converter/engine"

	"github.com/go-gost/llm-gw/internal/capture"
)

func TestRecorderCopiesRewriteFields(t *testing.T) {
	s := newStore(t)
	facts := NewFacts()
	ctx := context.Background()
	sid := "rid"

	facts.AddInboundProtocol(sid, "anthropic")
	facts.AddRewriteError(sid, "stream_rewrite", "response", "end", 7)

	body, err := json.Marshal(map[string]any{
		"sid":  sid,
		"node": "route/provider/node",
		"time": time.Now().UTC(),
		"http": map[string]any{"statusCode": 200},
		"err":  "rewrite failed",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := NewRecorder(s, facts, nil, nil).Record(ctx, body); err != nil {
		t.Fatal(err)
	}

	got, err := s.Get(ctx, sid)
	if err != nil || got == nil {
		t.Fatalf("get = %+v err=%v", got, err)
	}
	if got.InboundProtocol != "anthropic" || got.ErrorStage != "stream_rewrite" ||
		got.ErrorDirection != "response" || got.SSEPhase != "end" || got.EventIndex != 7 {
		t.Fatalf("row = %+v", got)
	}
}

func TestRecorderUsesUpstreamErrorSummary(t *testing.T) {
	s := newStore(t)
	facts := NewFacts()
	ctx := context.Background()
	sid := "rid"

	facts.AddUpstreamError(sid, "rate limit exceeded", "rate_limit_error")
	body, err := json.Marshal(map[string]any{
		"sid":  sid,
		"node": "route/provider/node",
		"time": time.Now().UTC(),
		"http": map[string]any{"statusCode": 429},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := NewRecorder(s, facts, nil, nil).Record(ctx, body); err != nil {
		t.Fatal(err)
	}

	got, err := s.Get(ctx, sid)
	if err != nil || got == nil {
		t.Fatalf("get = %+v err=%v", got, err)
	}
	if got.Error != "rate limit exceeded" || got.ErrorClass != ErrorClassUpstreamQuota {
		t.Fatalf("row = %+v", got)
	}
}

func TestRecorderCopiesRequestContext(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	sid := "ctx"
	body, err := json.Marshal(map[string]any{
		"sid":    sid,
		"node":   "route/provider/node",
		"time":   time.Now().UTC(),
		"client": "10.0.0.5:12345",
		"http": map[string]any{
			"statusCode":  200,
			"method":      "POST",
			"uri":         "/chat/completions",
			"originalUri": "/v1/messages",
			"request": map[string]any{
				"header": map[string][]string{
					"User-Agent":      {"claude-code/1.0"},
					"X-Forwarded-For": {"203.0.113.9, 10.0.0.1"},
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := NewRecorder(s, NewFacts(), nil, nil).Record(ctx, body); err != nil {
		t.Fatal(err)
	}

	got, err := s.Get(ctx, sid)
	if err != nil || got == nil {
		t.Fatalf("get = %+v err=%v", got, err)
	}
	if got.URI != "/v1/messages" || got.Method != "POST" ||
		got.ClientIP != "203.0.113.9" || got.UserAgent != "claude-code/1.0" {
		t.Fatalf("row = %+v", got)
	}
}

func TestRecorderClientStatusForUpstreamFailure(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	sid := "timeout"
	body, err := json.Marshal(map[string]any{
		"sid":  sid,
		"node": "route/provider/node",
		"time": time.Now().UTC(),
		"err":  "read tcp 10.0.0.1:1234->1.2.3.4:443: i/o timeout",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := NewRecorder(s, NewFacts(), nil, nil).Record(ctx, body); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, sid)
	if err != nil || got == nil {
		t.Fatalf("get = %+v err=%v", got, err)
	}
	if got.ClientStatus != 502 || got.ErrorClass != ErrorClassGatewayTimeout {
		t.Fatalf("row = %+v", got)
	}
}

func TestRecorderMarksSilentStreamTruncation(t *testing.T) {
	for _, tc := range []struct {
		name      string
		finish    string
		done      bool
		truncated bool
	}{
		{"no terminal event", "", false, true},
		{"finish reason", "stop", false, false},
		{"done sentinel", "", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newStore(t)
			facts := NewFacts()
			ctx := context.Background()
			sid := "stream"
			facts.Add(sid, "p", "chat", engine.Result{Stream: true})
			facts.AddStreamState(sid, tc.finish, tc.done)
			body, err := json.Marshal(map[string]any{
				"sid":  sid,
				"node": "r/p/n",
				"time": time.Now().UTC(),
				"http": map[string]any{"statusCode": 200},
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := NewRecorder(s, facts, nil, nil).Record(ctx, body); err != nil {
				t.Fatal(err)
			}
			got, err := s.Get(ctx, sid)
			if err != nil || got == nil {
				t.Fatalf("get = %+v err=%v", got, err)
			}
			if got.Truncated != tc.truncated || got.SawDone != tc.done || got.FinishReason != tc.finish {
				t.Fatalf("row = %+v", got)
			}
		})
	}
}

func TestRecorderCaptureErrorMode(t *testing.T) {
	s := newStore(t)
	facts := NewFacts()
	mgr := capture.NewManager(capture.Policy{Mode: "errors", MaxBytes: 64})
	rec := NewRecorder(s, facts, nil, mgr)
	ctx := context.Background()

	for _, tc := range []struct {
		sid    string
		status int
		err    string
	}{
		{"ok", 200, ""},
		{"bad", 500, "boom"},
	} {
		if _, ok := mgr.Start(tc.sid, "r", "m"); !ok {
			t.Fatalf("%s not captured", tc.sid)
		}
		mgr.Offer(tc.sid, "request", []byte("in"), []byte("out"), "", 0)
		body, _ := json.Marshal(map[string]any{
			"sid": tc.sid, "node": "r/p/n", "time": time.Now().UTC(),
			"http": map[string]any{"statusCode": tc.status}, "err": tc.err,
		})
		if err := rec.Record(ctx, body); err != nil {
			t.Fatal(err)
		}
	}
	if c, _ := s.GetCapture(ctx, "ok"); c != nil {
		t.Fatalf("successful request kept capture: %+v", c)
	}
	if c, _ := s.GetCapture(ctx, "bad"); c == nil || c.RequestRewritten != "out" {
		t.Fatalf("failed request capture = %+v", c)
	}
}

func TestRecorderCaptureOffWritesNothing(t *testing.T) {
	s := newStore(t)
	mgr := capture.NewManager(capture.Policy{Mode: "off"})
	rec := NewRecorder(s, NewFacts(), nil, mgr)
	body, _ := json.Marshal(map[string]any{
		"sid": "off", "node": "r/p/n", "time": time.Now().UTC(),
		"http": map[string]any{"statusCode": 200},
	})
	if err := rec.Record(context.Background(), body); err != nil {
		t.Fatal(err)
	}
	if c, _ := s.GetCapture(context.Background(), "off"); c != nil {
		t.Fatalf("off mode wrote capture: %+v", c)
	}
}
