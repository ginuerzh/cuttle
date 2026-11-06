package stats

import (
	"context"
	"encoding/json"
	"testing"
	"time"
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
	if err := NewRecorder(s, facts, nil).Record(ctx, body); err != nil {
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
