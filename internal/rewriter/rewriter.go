// Package rewriter adapts engine.Engine to core/rewriter.Rewriter so gost's
// node-level body rewrites can run in-process.
package rewriter

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/go-gost/core/logger"
	corerewriter "github.com/go-gost/core/rewriter"
	xlogger "github.com/go-gost/x/logger"

	"github.com/ginuerzh/cuttle/internal/capture"

	"github.com/ginuerzh/llm-api-converter/engine"
)

// Sink receives per-request facts observed by the engine.
type Sink interface {
	Add(sid, provider, protocol string, res engine.Result)
	AddInboundProtocol(sid, protocol string)
	AddRewriteError(sid, stage, direction, ssePhase string, eventIndex int)
	AddUpstreamError(sid, message, typ string)
	AddStreamState(sid, finishReason string, sawDone bool)
	MarkFirstResponse(sid string, at time.Time)
}

// Options configures one route-scoped body rewriter.
type Options struct {
	Provider    string
	Protocol    string
	Route       string
	Node        string
	TargetModel string
	Sink        Sink
	Capture     *capture.Manager
	Logger      logger.Logger
}

// Rewriter is an in-process core/rewriter.Rewriter backed by an engine.
type Rewriter struct {
	eng  *engine.Engine
	opts Options
}

// New creates a Rewriter.
func New(eng *engine.Engine, opts Options) *Rewriter {
	if opts.Logger == nil {
		opts.Logger = xlogger.Nop()
	}
	return &Rewriter{eng: eng, opts: opts}
}

// Rewrite implements core/rewriter.Rewriter.
func (r *Rewriter) Rewrite(ctx context.Context, b []byte, opts ...corerewriter.RewriteOption) ([]byte, error) {
	var ro corerewriter.RewriteOptions
	for _, opt := range opts {
		opt(&ro)
	}

	md, _ := ro.Metadata.(map[string]any)
	meta := engine.Meta{
		SID:        stringValue(md["sid"]),
		Direction:  stringValue(md["direction"]),
		URI:        stringValue(md["uri"]),
		SSEPhase:   stringValue(md["sse_phase"]),
		EventIndex: intValue(md["event_index"]),
	}
	inbound := inboundProtocol(meta.URI)
	if inbound != "" && r.opts.Sink != nil {
		r.opts.Sink.AddInboundProtocol(meta.SID, inbound)
	}
	if meta.Direction == "request" && r.opts.Protocol == "anthropic" && r.opts.TargetModel != "" && isCountTokensURI(meta.URI) {
		b = replaceModelField(b, r.opts.TargetModel)
	}

	if strings.EqualFold(meta.Direction, "response") &&
		meta.SSEPhase != "" &&
		!strings.EqualFold(meta.SSEPhase, "end") &&
		r.opts.Sink != nil {
		r.opts.Sink.MarkFirstResponse(meta.SID, time.Now())
	}

	res, err := r.eng.Rewrite(ctx, b, meta)
	if strings.EqualFold(meta.Direction, "response") && r.opts.Sink != nil {
		if message, typ := extractErrorSummary(b); message != "" {
			r.opts.Sink.AddUpstreamError(meta.SID, message, typ)
		}
		if meta.SSEPhase != "" {
			if reason, done := extractStreamState(b); reason != "" || done {
				r.opts.Sink.AddStreamState(meta.SID, reason, done)
			}
		}
	}
	if err != nil {
		r.captureRewrite(meta, b, nil, res)
		stage := rewriteStage(meta.Direction, meta.SSEPhase)
		if r.opts.Sink != nil {
			r.opts.Sink.AddRewriteError(meta.SID, stage, meta.Direction, meta.SSEPhase, meta.EventIndex)
		}
		r.opts.Logger.WithFields(map[string]any{
			"sid":              meta.SID,
			"route":            r.opts.Route,
			"node":             r.opts.Node,
			"provider":         r.opts.Provider,
			"inbound_protocol": inbound,
			"protocol":         r.opts.Protocol,
			"error_stage":      stage,
			"direction":        meta.Direction,
			"uri":              meta.URI,
			"sse_phase":        meta.SSEPhase,
			"event_index":      meta.EventIndex,
		}).Errorf("rewrite %s: %v", meta.Direction, err)
		return b, err
	}
	r.captureRewrite(meta, b, res.Data, res)
	if level := requestEffort(meta.Direction, b); level != "" {
		res.Effort = level
	}
	if r.opts.Sink != nil {
		r.opts.Sink.Add(meta.SID, r.opts.Provider, r.opts.Protocol, res)
	}
	return res.Data, nil
}

func (r *Rewriter) captureRewrite(meta engine.Meta, original, rewritten []byte, res engine.Result) {
	if r.opts.Capture == nil || meta.SID == "" {
		return
	}
	if strings.EqualFold(strings.TrimSpace(meta.Direction), "request") {
		model := res.ClientModel
		if model == "" {
			model = requestModel(meta.URI, original)
		}
		if _, ok := r.opts.Capture.Start(meta.SID, r.opts.Route, model); !ok {
			return
		}
		r.opts.Capture.Offer(meta.SID, "request", original, rewritten, "", 0)
		return
	}
	r.opts.Capture.Offer(meta.SID, "response", original, rewritten, meta.SSEPhase, meta.EventIndex)
}

// requestEffort returns the explicit effort level on a request body, preserving
// levels such as xhigh and max that the engine normalizes into a band for
// routing. Stats show the level the client actually selected.
func requestEffort(direction string, body []byte) string {
	if !strings.EqualFold(strings.TrimSpace(direction), "request") || len(body) == 0 {
		return ""
	}
	var v struct {
		ReasoningEffort string `json:"reasoning_effort"`
		Reasoning       struct {
			Effort string `json:"effort"`
		} `json:"reasoning"`
		OutputConfig struct {
			Effort string `json:"effort"`
		} `json:"output_config"`
		GenerationConfig struct {
			ThinkingConfig struct {
				ThinkingLevel string `json:"thinkingLevel"`
			} `json:"thinkingConfig"`
		} `json:"generationConfig"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		return ""
	}
	for _, s := range []string{
		v.ReasoningEffort,
		v.Reasoning.Effort,
		v.OutputConfig.Effort,
		v.GenerationConfig.ThinkingConfig.ThinkingLevel,
	} {
		if level := effortLevel(s); level != "" {
			return level
		}
	}
	return ""
}

// effortLevel normalizes an explicit effort string. "minimal" is an OpenAI
// level below "low" and stays reported as low, matching the engine band.
func effortLevel(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "minimal", "low":
		return "low"
	case "medium":
		return "medium"
	case "high":
		return "high"
	case "xhigh":
		return "xhigh"
	case "max":
		return "max"
	}
	return ""
}

func requestModel(uri string, body []byte) string {
	var v struct {
		Model string `json:"model"`
	}
	if len(body) > 0 && json.Unmarshal(body, &v) == nil && v.Model != "" {
		return v.Model
	}
	const prefix = "/v1beta/models/"
	if i := strings.Index(uri, prefix); i >= 0 {
		rest := uri[i+len(prefix):]
		if j := strings.IndexByte(rest, ':'); j > 0 {
			return rest[:j]
		}
	}
	return ""
}

func inboundProtocol(uri string) string {
	if i := strings.IndexByte(uri, '?'); i >= 0 {
		uri = uri[:i]
	}
	switch {
	case uri == "/v1/chat/completions":
		return "openai-chat"
	case uri == "/v1/responses":
		return "openai-responses"
	case uri == "/v1/messages":
		return "anthropic"
	case uri == "/v1/messages/count_tokens":
		return "anthropic"
	case strings.HasPrefix(uri, "/v1beta/models/"):
		return "gemini"
	default:
		return ""
	}
}

// isCountTokensURI reports the Anthropic token-counting path, ignoring a query
// string.
func isCountTokensURI(uri string) bool {
	if i := strings.IndexByte(uri, '?'); i >= 0 {
		uri = uri[:i]
	}
	return uri == "/v1/messages/count_tokens"
}

// replaceModelField replaces only the top-level model field. The body is
// returned unchanged when it is not JSON, has no model field, or the target
// model is empty.
func replaceModelField(body []byte, model string) []byte {
	if len(body) == 0 || model == "" {
		return body
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return body
	}
	if _, ok := raw["model"]; !ok {
		return body
	}
	v, err := json.Marshal(model)
	if err != nil {
		return body
	}
	raw["model"] = v
	out, err := json.Marshal(raw)
	if err != nil {
		return body
	}
	return out
}

func rewriteStage(direction, ssePhase string) string {
	if strings.EqualFold(strings.TrimSpace(direction), "request") {
		return "request_rewrite"
	}
	if strings.TrimSpace(ssePhase) != "" {
		return "stream_rewrite"
	}
	return "response_rewrite"
}

func stringValue(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case json.Number:
		return t.String()
	case fmt.Stringer:
		return t.String()
	}
	return ""
}

func intValue(v any) int {
	switch t := v.(type) {
	case int:
		return t
	case int64:
		return int(t)
	case float64:
		return int(t)
	case json.Number:
		if n, err := t.Int64(); err == nil {
			return int(n)
		}
	}
	return 0
}
