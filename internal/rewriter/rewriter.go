// Package rewriter adapts engine.Engine to core/rewriter.Rewriter so gost's
// node-level body rewrites can run in-process.
package rewriter

import (
	"context"
	"encoding/json"
	"fmt"

	corerewriter "github.com/go-gost/core/rewriter"

	"llm-api-converter/engine"
)

// Sink receives per-request facts observed by the engine.
type Sink interface {
	Add(sid, provider, protocol string, res engine.Result)
}

// Rewriter is an in-process core/rewriter.Rewriter backed by an engine.
type Rewriter struct {
	eng      *engine.Engine
	provider string
	protocol string
	sink     Sink
}

// New creates a Rewriter.
func New(eng *engine.Engine, provider, protocol string, sink Sink) *Rewriter {
	return &Rewriter{eng: eng, provider: provider, protocol: protocol, sink: sink}
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
	res, err := r.eng.Rewrite(ctx, b, meta)
	if err != nil {
		return b, err
	}
	if r.sink != nil {
		r.sink.Add(meta.SID, r.provider, r.protocol, res)
	}
	return res.Data, nil
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
