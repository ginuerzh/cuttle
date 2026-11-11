package stats

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-gost/core/logger"
	"github.com/go-gost/core/recorder"
	xlogger "github.com/go-gost/x/logger"
	xrecorder "github.com/go-gost/x/recorder"

	"github.com/ginuerzh/cuttle/internal/capture"
)

// Recorder implements core/recorder.Recorder for the forward handler.
type Recorder struct {
	store   *Store
	facts   *Facts
	log     logger.Logger
	capture *capture.Manager
}

// recordedAttempt, recordedHTTP and recordedAttemptHost decode the discarded
// attempts an upstream retry leaves in the recorder payload. The mapping is
// local on purpose: cuttle is pinned to an x version whose recorder package has
// no attempt type, and a payload from an older x simply has no http.attempts.
type recordedAttempt struct {
	Attempt        int                 `json:"attempt"`
	Node           string              `json:"node"`
	StatusCode     int                 `json:"statusCode"`
	Error          string              `json:"error,omitempty"`
	DurationMS     int64               `json:"durationMs"`
	BytesIn        int64               `json:"bytesIn"`
	BytesOut       int64               `json:"bytesOut"`
	ResponseHeader map[string][]string `json:"responseHeader,omitempty"`
	ResponseBody   []byte              `json:"responseBody,omitempty"`
}

type recordedHTTP struct {
	Attempts []recordedAttempt `json:"attempts"`
}

type recordedAttemptHost struct {
	HTTP *recordedHTTP `json:"http"`
}

// decodeAttempts maps the recorded attempts onto the parent request, falling
// back to its route, node and provider for any component an attempt did not
// name.
func decodeAttempts(req Request, b []byte) []Attempt {
	var host recordedAttemptHost
	if err := json.Unmarshal(b, &host); err != nil || host.HTTP == nil || len(host.HTTP.Attempts) == 0 {
		return nil
	}
	out := make([]Attempt, 0, len(host.HTTP.Attempts))
	for _, a := range host.HTTP.Attempts {
		route, provider, node := parseNodeName(a.Node)
		if route == "" {
			route = req.Route
		}
		if provider == "" {
			provider = req.Provider
		}
		if node == "" {
			node = req.Node
		}
		msg := firstNonEmpty(a.Error, attemptMessage(a.ResponseBody))
		out = append(out, Attempt{
			RequestID:       req.ID,
			Attempt:         a.Attempt,
			TS:              req.TS,
			Route:           route,
			Node:            node,
			Provider:        provider,
			GatewayKey:      req.GatewayKey,
			Status:          a.StatusCode,
			Error:           msg,
			ErrorClass:      ClassifyError(a.StatusCode, msg, ""),
			DurationMS:      a.DurationMS,
			BytesIn:         a.BytesIn,
			BytesOut:        a.BytesOut,
			ResponseHeaders: captureHeaders(http.Header(a.ResponseHeader)),
			BodyPreview:     string(a.ResponseBody),
		})
	}
	return out
}

// NewRecorder creates a recorder that writes one request row per call.
func NewRecorder(store *Store, facts *Facts, log logger.Logger, captureMgr *capture.Manager) *Recorder {
	if log == nil {
		log = xlogger.Nop()
	}
	return &Recorder{store: store, facts: facts, log: log, capture: captureMgr}
}

// Record implements recorder.Recorder.
// parseNodeName splits <route>/<provider>/<node>[/<key>].
func parseNodeName(name string) (route, provider, node string) {
	parts := strings.Split(name, "/")
	if len(parts) == 0 || parts[0] == "" {
		return "", name, ""
	}
	if len(parts) == 1 {
		return "", parts[0], ""
	}
	route, provider = parts[0], parts[1]
	if len(parts) >= 3 {
		node = parts[2]
	}
	return route, provider, node
}

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
	route, provider, node := parseNodeName(ro.Node)
	req := Request{
		ID:         ro.SID,
		SID:        ro.SID,
		TS:         ro.Time,
		Provider:   provider,
		Route:      route,
		Node:       node,
		Status:     status,
		Error:      ro.Err,
		DurationMS: ro.Duration.Milliseconds(),
		BytesIn:    ro.InputBytes,
		BytesOut:   ro.OutputBytes,
		Retries:    ro.Retries,
	}
	req.URI, req.Method, req.ClientIP, req.UserAgent, req.Upstream = requestContext(&ro)
	req.GatewayKey = GatewayKeyIDFromContext(ctx)
	if req.ID == "" {
		req.ID = fmt.Sprintf("%d", time.Now().UnixNano())
	}

	if fact, ok := r.facts.Take(ro.SID); ok {
		if fact.Provider != "" {
			req.Provider = fact.Provider
		}
		req.Protocol = fact.Protocol
		req.InboundProtocol = fact.InboundProtocol
		req.ErrorStage = fact.ErrorStage
		req.ErrorDirection = fact.ErrorDirection
		req.SSEPhase = fact.SSEPhase
		req.EventIndex = fact.EventIndex
		req.ClientModel = fact.ClientModel
		req.Model = fact.Model
		req.Effort = fact.Effort
		req.InputTokens = int64(fact.InputTokens)
		req.OutputTokens = int64(fact.OutputTokens)
		req.CachedTokens = int64(fact.CachedTokens)
		req.Stream = fact.Stream
		if fact.Stream && !fact.FirstResponseAt.IsZero() {
			ttft := fact.FirstResponseAt.Sub(ro.Time)
			if ttft < 0 {
				ttft = 0
			}
			if ttft > ro.Duration {
				ttft = ro.Duration
			}
			req.TTFTMS = ttft.Milliseconds()
		}
		req.FinishReason = fact.FinishReason
		req.SawDone = fact.SawDone
		if req.Error == "" && fact.UpstreamError != "" {
			req.Error = fact.UpstreamError
		}
	}
	req.ErrorClass = ClassifyError(req.Status, req.Error, req.ErrorStage)
	req.ClientStatus = req.Status
	if req.ClientStatus == 0 && req.Error != "" {
		// The forward handler writes a 502 when the upstream produced no
		// response at all; record what the client actually saw.
		req.ClientStatus = 502
	}
	req.Truncated = isTruncation(strings.ToLower(req.Error)) ||
		(req.Stream && req.Error == "" && req.Status == 200 && !req.SawDone && req.FinishReason == "")
	req.Attempts = decodeAttempts(req, b)

	var cap *capture.Capture
	if r.capture != nil {
		if c, ok := r.capture.Finish(req.SID); ok {
			cap = c
			if ro.HTTP != nil {
				cap.RequestHeaders = captureHeaders(clientRequestHeaders(&ro))
				cap.ResponseHeaders = captureHeaders(ro.HTTP.Response.Header)
			}
		}
	}
	failed := req.Status >= 400 || req.Error != ""
	if cap != nil && cap.OnError && !failed {
		cap = nil
	}

	if err := r.store.Insert(ctx, req); err != nil {
		r.log.Errorf("stats: insert %s: %v", req.ID, err)
		return err
	}
	if cap != nil {
		if err := r.store.PutCapture(ctx, req.ID, cap); err != nil {
			r.log.Errorf("stats: capture %s: %v", req.ID, err)
		}
	}
	return nil
}
