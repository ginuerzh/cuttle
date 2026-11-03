package stats

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/go-gost/core/logger"
	"github.com/go-gost/core/recorder"
	xlogger "github.com/go-gost/x/logger"
	xrecorder "github.com/go-gost/x/recorder"
)

// Recorder implements core/recorder.Recorder for the forward handler.
type Recorder struct {
	store *Store
	facts *Facts
	log   logger.Logger
}

// NewRecorder creates a recorder that writes one request row per call.
func NewRecorder(store *Store, facts *Facts, log logger.Logger) *Recorder {
	if log == nil {
		log = xlogger.Nop()
	}
	return &Recorder{store: store, facts: facts, log: log}
}

// Record implements recorder.Recorder.
func (r *Recorder) Record(ctx context.Context, b []byte, opts ...recorder.RecordOption) error {
	if r == nil || r.store == nil {
		return nil
	}

	var ro xrecorder.HandlerRecorderObject
	if err := json.Unmarshal(b, &ro); err != nil {
		return err
	}

	status := 0
	if ro.HTTP != nil {
		status = ro.HTTP.StatusCode
	}
	req := Request{
		ID:         ro.SID,
		SID:        ro.SID,
		TS:         ro.Time,
		Provider:   ro.Node,
		Status:     status,
		Error:      ro.Err,
		DurationMS: ro.Duration.Milliseconds(),
		BytesIn:    ro.InputBytes,
		BytesOut:   ro.OutputBytes,
	}
	if req.ID == "" {
		req.ID = fmt.Sprintf("%d", time.Now().UnixNano())
	}

	if fact, ok := r.facts.Take(ro.SID); ok {
		if fact.Provider != "" {
			req.Provider = fact.Provider
		}
		req.Protocol = fact.Protocol
		req.Model = fact.Model
		req.Effort = fact.Effort
		req.InputTokens = int64(fact.InputTokens)
		req.OutputTokens = int64(fact.OutputTokens)
		req.CachedTokens = int64(fact.CachedTokens)
		req.Stream = fact.Stream
	}

	if err := r.store.Insert(ctx, req); err != nil {
		r.log.Errorf("stats: insert %s: %v", req.ID, err)
		return err
	}
	return nil
}
