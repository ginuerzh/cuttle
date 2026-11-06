package stats

import (
	"sync"
	"time"

	"github.com/ginuerzh/llm-api-converter/engine"
)

const factTTL = 10 * time.Minute

// Fact holds the per-request facts observed by the protocol engine.
type Fact struct {
	Provider        string
	Protocol        string
	InboundProtocol string
	ErrorStage      string
	ErrorDirection  string
	UpstreamError   string
	UpstreamErrorTy string
	FinishReason    string
	SawDone         bool
	SSEPhase        string
	EventIndex      int
	ClientModel     string
	Model           string
	Effort          string
	InputTokens     int
	OutputTokens    int
	CachedTokens    int
	Stream          bool
	Updated         time.Time
}

// Facts correlates engine observations with the recorder by session id.
type Facts struct {
	mu sync.Mutex
	m  map[string]Fact
}

// NewFacts creates an empty fact store.
func NewFacts() *Facts {
	return &Facts{m: make(map[string]Fact)}
}

// Add merges one engine result into the fact for sid.
func (f *Facts) Add(sid, provider, protocol string, res engine.Result) {
	if f == nil || sid == "" {
		return
	}
	now := time.Now()

	f.mu.Lock()
	defer f.mu.Unlock()

	fact := f.m[sid]
	if provider != "" {
		fact.Provider = provider
	}
	if protocol != "" {
		fact.Protocol = protocol
	}
	if res.ClientModel != "" {
		fact.ClientModel = res.ClientModel
	}
	if res.TargetModel != "" {
		fact.Model = res.TargetModel
	}
	if res.Effort != "" {
		fact.Effort = res.Effort
	}
	fact.InputTokens = max(fact.InputTokens, res.InputTokens)
	fact.OutputTokens = max(fact.OutputTokens, res.OutputTokens)
	fact.CachedTokens = max(fact.CachedTokens, res.CachedTokens)
	fact.Stream = fact.Stream || res.Stream
	fact.Updated = now
	f.m[sid] = fact

	for k, v := range f.m {
		if now.Sub(v.Updated) > factTTL {
			delete(f.m, k)
		}
	}
}

// AddInboundProtocol records the client-facing protocol for sid.
func (f *Facts) AddInboundProtocol(sid, protocol string) {
	if f == nil || sid == "" || protocol == "" {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()

	fact := f.m[sid]
	fact.InboundProtocol = protocol
	fact.Updated = time.Now()
	f.m[sid] = fact
}

// AddRewriteError records where a body rewrite failed.
func (f *Facts) AddRewriteError(sid, stage, direction, ssePhase string, eventIndex int) {
	if f == nil || sid == "" {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()

	fact := f.m[sid]
	fact.ErrorStage = stage
	fact.ErrorDirection = direction
	fact.SSEPhase = ssePhase
	fact.EventIndex = eventIndex
	fact.Updated = time.Now()
	f.m[sid] = fact
}

// AddUpstreamError records a sanitized error summary observed in an upstream
// response body. The recorder uses it when the transport error is empty.
func (f *Facts) AddUpstreamError(sid, message, typ string) {
	if f == nil || sid == "" || message == "" {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()

	fact := f.m[sid]
	fact.UpstreamError = message
	fact.UpstreamErrorTy = typ
	fact.Updated = time.Now()
	f.m[sid] = fact
}

// AddStreamState records the terminal state observed in a response stream.
func (f *Facts) AddStreamState(sid, finishReason string, sawDone bool) {
	if f == nil || sid == "" {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()

	fact := f.m[sid]
	if finishReason != "" {
		fact.FinishReason = finishReason
	}
	fact.SawDone = fact.SawDone || sawDone
	fact.Updated = time.Now()
	f.m[sid] = fact
}

// Take removes and returns the fact for sid.
func (f *Facts) Take(sid string) (Fact, bool) {
	if f == nil || sid == "" {
		return Fact{}, false
	}
	f.mu.Lock()
	defer f.mu.Unlock()

	fact, ok := f.m[sid]
	if ok {
		delete(f.m, sid)
	}
	return fact, ok
}
