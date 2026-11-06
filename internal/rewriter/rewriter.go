// Package rewriter adapts engine.Engine to core/rewriter.Rewriter so gost's
// node-level body rewrites can run in-process.
package rewriter

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/go-gost/core/logger"
	corerewriter "github.com/go-gost/core/rewriter"
	xlogger "github.com/go-gost/x/logger"

	"llm-api-converter/engine"
)

// Sink receives per-request facts observed by the engine.
type Sink interface {
	Add(sid, provider, protocol string, res engine.Result)
	AddInboundProtocol(sid, protocol string)
	AddRewriteError(sid, stage, direction, ssePhase string, eventIndex int)
}

// Options configures one route-scoped body rewriter.
type Options struct {
	Provider string
	Protocol string
	Route    string
	Node     string
	Sink     Sink
	Logger   logger.Logger
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

	res, err := r.eng.Rewrite(ctx, b, meta)
	if err != nil {
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
	if r.opts.Sink != nil {
		r.opts.Sink.Add(meta.SID, r.opts.Provider, r.opts.Protocol, res)
	}
	return res.Data, nil
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
	case strings.HasPrefix(uri, "/v1beta/models/"):
		return "gemini"
	default:
		return ""
	}
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
