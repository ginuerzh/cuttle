package stats

import (
	"sync"
	"time"

	"llm-api-converter/engine"
)

const factTTL = 10 * time.Minute

// Fact holds the per-request facts observed by the protocol engine.
type Fact struct {
	Provider     string
	Protocol     string
	ClientModel  string
	Model        string
	Effort       string
	InputTokens  int
	OutputTokens int
	CachedTokens int
	Stream       bool
	Updated      time.Time
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
