package stats

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/ginuerzh/llm-api-converter/engine"

	"github.com/ginuerzh/cuttle/internal/capture"
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

func TestRecorderRecordsGatewayKey(t *testing.T) {
	s := newStore(t)
	facts := NewFacts()
	sid := "gateway-key"

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
	if err := NewRecorder(s, facts, nil, nil).Record(WithGatewayKey(context.Background(), "alpha"), body); err != nil {
		t.Fatal(err)
	}

	got, err := s.Get(context.Background(), sid)
	if err != nil || got == nil {
		t.Fatalf("get = %+v err=%v", got, err)
	}
	if got.GatewayKey != "alpha" {
		t.Fatalf("gateway key = %q, want alpha", got.GatewayKey)
	}
}

func TestRecorderStoresStreamTTFT(t *testing.T) {
	s := newStore(t)
	facts := NewFacts()
	ctx := context.Background()
	sid := "ttft-rec"
	start := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)

	facts.Add(sid, "", "", engine.Result{Stream: true})
	facts.MarkFirstResponse(sid, start.Add(400*time.Millisecond))

	body, err := json.Marshal(map[string]any{
		"sid": sid, "node": "route/provider/node",
		"time": start, "duration": 2 * time.Second,
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
	if !got.Stream || got.TTFTMS != 400 {
		t.Fatalf("stream/ttft = %v/%d, want true/400", got.Stream, got.TTFTMS)
	}
}

func TestRecorderSkipsTTFTForNonStreaming(t *testing.T) {
	s := newStore(t)
	facts := NewFacts()
	ctx := context.Background()
	sid := "ttft-plain"
	start := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)

	facts.MarkFirstResponse(sid, start.Add(200*time.Millisecond))
	body, err := json.Marshal(map[string]any{
		"sid": sid, "node": "route/provider/node",
		"time": start, "duration": time.Second,
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
	if got.Stream || got.TTFTMS != 0 {
		t.Fatalf("stream/ttft = %v/%d, want false/0", got.Stream, got.TTFTMS)
	}
}

func TestRecorderClampsStreamTTFT(t *testing.T) {
	start := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name     string
		first    time.Time
		duration time.Duration
		want     int64
	}{
		{"before-start", start.Add(-time.Second), 2 * time.Second, 0},
		{"after-end", start.Add(3 * time.Second), 2 * time.Second, 2000},
		{"sub-millisecond", start.Add(500 * time.Microsecond), time.Second, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newStore(t)
			facts := NewFacts()
			ctx := context.Background()
			sid := "ttft-" + tc.name

			facts.Add(sid, "", "", engine.Result{Stream: true})
			facts.MarkFirstResponse(sid, tc.first)
			body, err := json.Marshal(map[string]any{
				"sid": sid, "node": "route/provider/node",
				"time": start, "duration": tc.duration,
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
			if got.TTFTMS != tc.want {
				t.Fatalf("ttft = %d, want %d", got.TTFTMS, tc.want)
			}
		})
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

func TestRecorderMarksUpstreamTruncationWithoutStream(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	sid := "truncated-no-stream"
	body, err := json.Marshal(map[string]any{
		"sid":  sid,
		"node": "r/p/n",
		"time": time.Now().UTC(),
		"err":  "unexpected EOF",
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
	if !got.Truncated || got.ErrorClass != ErrorClassUpstreamTruncated || got.ClientStatus != 502 {
		t.Fatalf("row = %+v", got)
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
			"http": map[string]any{
				"statusCode": tc.status,
				"request": map[string]any{"header": map[string][]string{
					"User-Agent":    {"cli"},
					"Authorization": {"Bearer secret"},
				}},
				"response": map[string]any{"header": map[string][]string{
					"Retry-After": {"30"},
					"Set-Cookie":  {"session=secret"},
				}},
			},
			"err": tc.err,
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
	} else {
		if c.RequestHeaders["user-agent"] != "cli" || c.ResponseHeaders["retry-after"] != "30" {
			t.Fatalf("capture headers = %+v / %+v", c.RequestHeaders, c.ResponseHeaders)
		}
		if _, ok := c.RequestHeaders["authorization"]; ok {
			t.Fatalf("captured authorization header: %+v", c.RequestHeaders)
		}
		if _, ok := c.ResponseHeaders["set-cookie"]; ok {
			t.Fatalf("captured set-cookie header: %+v", c.ResponseHeaders)
		}
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
