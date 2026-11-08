// Package authfail keeps a bounded in-memory record of rejected data-plane
// gateway-key authentication attempts for the admin console. It never stores
// credentials.
package authfail

import (
	"sync"
	"time"
)

// DefaultLimit is the number of failures retained for the console.
const DefaultLimit = 50

// Failure is one rejected authentication attempt. Method and Path are already
// sanitized by the caller; UserAgent and Path are truncated before they reach
// the tracker.
type Failure struct {
	TS        time.Time `json:"ts"`
	Reason    string    `json:"reason"`
	Method    string    `json:"method,omitempty"`
	Path      string    `json:"path,omitempty"`
	ClientIP  string    `json:"clientIp,omitempty"`
	UserAgent string    `json:"userAgent,omitempty"`
}

// Snapshot is a copy of the retained failures. Recent is oldest first and is
// never nil. Truncated reports that older failures were dropped by the limit.
type Snapshot struct {
	Recent    []Failure `json:"recent"`
	Truncated bool      `json:"truncated,omitempty"`
}

// Tracker is a fixed-size ring of recent failures. It is safe for concurrent
// use by the data plane and the admin server.
type Tracker struct {
	mu      sync.Mutex
	limit   int
	entries []Failure
	total   uint64
}

// New creates a tracker retaining at most limit failures.
func New(limit int) *Tracker {
	if limit <= 0 {
		limit = DefaultLimit
	}
	return &Tracker{limit: limit, entries: make([]Failure, 0, limit)}
}

// Add records one failure. A zero timestamp is replaced with the current time;
// every timestamp is normalized to UTC.
func (t *Tracker) Add(f Failure) {
	if t == nil {
		return
	}
	if f.TS.IsZero() {
		f.TS = time.Now()
	}
	f.TS = f.TS.UTC()

	t.mu.Lock()
	defer t.mu.Unlock()

	t.total++
	if len(t.entries) == t.limit {
		copy(t.entries, t.entries[1:])
		t.entries[len(t.entries)-1] = f
		return
	}
	t.entries = append(t.entries, f)
}

// Clear drops every retained failure and resets the truncation flag. It keeps
// the configured limit for later failures.
func (t *Tracker) Clear() {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	t.entries = t.entries[:0]
	t.total = 0
}

// Snapshot returns a copy of the retained failures.
func (t *Tracker) Snapshot() Snapshot {
	if t == nil {
		return Snapshot{Recent: []Failure{}}
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	recent := make([]Failure, len(t.entries))
	copy(recent, t.entries)
	return Snapshot{
		Recent:    recent,
		Truncated: t.total > uint64(t.limit),
	}
}
