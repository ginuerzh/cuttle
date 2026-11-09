// Package capture decides which requests get body previews and buffers the
// original/rewritten payloads for the stats recorder.
package capture

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"math/rand/v2"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	MaxRuleLimit  = 1000
	MaxRuleTTL    = time.Hour
	maxSessions   = 1000
	maxTotalBytes = 64 << 20
)

// Preview field names recorded in Capture.TruncatedFields. They are the JSON
// names of the corresponding Capture fields so the admin console can map them
// without a second translation table.
const (
	FieldRequestOriginal   = "requestOriginal"
	FieldRequestRewritten  = "requestRewritten"
	FieldResponseOriginal  = "responseOriginal"
	FieldResponseRewritten = "responseRewritten"
	FieldSSEFirstOriginal  = "sseFirstOriginal"
	FieldSSEFirstRewritten = "sseFirstRewritten"
	FieldSSELastOriginal   = "sseLastOriginal"
	FieldSSELastRewritten  = "sseLastRewritten"
)

// Policy is the global capture policy.
type Policy struct {
	Mode       string
	SampleRate float64
	MaxBytes   int
	Retain     time.Duration
}

// Rule is one in-memory on-demand capture rule.
type Rule struct {
	ID          string        `json:"id"`
	Route       string        `json:"route,omitempty"`
	ClientModel string        `json:"clientModel,omitempty"`
	Reason      string        `json:"reason"`
	Limit       int           `json:"limit"`
	TTL         time.Duration `json:"ttl"`
	Created     time.Time     `json:"created"`
	Expires     time.Time     `json:"expires"`
	Used        int           `json:"used"`
}

// Capture is the bounded preview set persisted for one request.
type Capture struct {
	RequestOriginal   string            `json:"requestOriginal,omitempty"`
	RequestRewritten  string            `json:"requestRewritten,omitempty"`
	ResponseOriginal  string            `json:"responseOriginal,omitempty"`
	ResponseRewritten string            `json:"responseRewritten,omitempty"`
	SSEFirstOriginal  string            `json:"sseFirstOriginal,omitempty"`
	SSEFirstRewritten string            `json:"sseFirstRewritten,omitempty"`
	SSELastOriginal   string            `json:"sseLastOriginal,omitempty"`
	SSELastRewritten  string            `json:"sseLastRewritten,omitempty"`
	RequestSHA256     string            `json:"requestSha256,omitempty"`
	ResponseSHA256    string            `json:"responseSha256,omitempty"`
	RequestHeaders    map[string]string `json:"requestHeaders,omitempty"`
	ResponseHeaders   map[string]string `json:"responseHeaders,omitempty"`
	// TruncatedFields lists the preview fields clipped to Policy.MaxBytes.
	TruncatedFields []string  `json:"truncatedFields,omitempty"`
	SSIEvents       int       `json:"sseEvents,omitempty"`
	Reason          string    `json:"reason,omitempty"`
	OnError         bool      `json:"-"`
	CreatedAt       time.Time `json:"createdAt,omitempty"`
}

type session struct {
	capture      Capture
	reqHash      hash.Hash
	respHash     hash.Hash
	reqHashUsed  bool
	respHashUsed bool
	sseSeen      bool
	bytes        int
	truncated    map[string]bool
}

// Manager owns the policy, on-demand rules and per-SID buffers.
type Manager struct {
	mu         sync.Mutex
	policy     Policy
	rules      []Rule
	sessions   map[string]*session
	nextID     int
	totalBytes int
	now        func() time.Time
	rand       func() float64
}

// NewManager creates a manager with the given policy.
func NewManager(p Policy) *Manager {
	if p.MaxBytes <= 0 {
		p.MaxBytes = 1048576
	}
	return &Manager{
		policy:   p,
		sessions: make(map[string]*session),
		now:      time.Now,
		rand:     rand.Float64,
	}
}

// SetPolicy updates the global policy; active sessions keep their current
// settings.
func (m *Manager) SetPolicy(p Policy) {
	if p.MaxBytes <= 0 {
		p.MaxBytes = 1048576
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.policy = p
}

// AddRule creates a bounded on-demand rule.
func (m *Manager) AddRule(route, clientModel, reason string, limit int, ttl time.Duration) (Rule, error) {
	if limit <= 0 || limit > MaxRuleLimit {
		return Rule{}, fmt.Errorf("limit must be between 1 and %d", MaxRuleLimit)
	}
	if ttl <= 0 || ttl > MaxRuleTTL {
		return Rule{}, fmt.Errorf("ttl must be between 1s and %s", MaxRuleTTL)
	}
	if strings.TrimSpace(reason) == "" {
		return Rule{}, fmt.Errorf("reason is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.nextID++
	now := m.now()
	r := Rule{
		ID:          fmt.Sprintf("cap-%d", m.nextID),
		Route:       strings.TrimSpace(route),
		ClientModel: strings.TrimSpace(clientModel),
		Reason:      reason,
		Limit:       limit,
		TTL:         ttl,
		Created:     now,
		Expires:     now.Add(ttl),
	}
	m.rules = append(m.rules, r)
	return r, nil
}

// Rules returns active rules, pruning expired and exhausted entries.
func (m *Manager) Rules() []Rule {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pruneRulesLocked()
	return append([]Rule(nil), m.rules...)
}

// RemoveRule deletes one rule.
func (m *Manager) RemoveRule(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.rules {
		if m.rules[i].ID == id {
			m.rules = append(m.rules[:i], m.rules[i+1:]...)
			return true
		}
	}
	return false
}

// Start decides whether the request identified by sid is captured.
func (m *Manager) Start(sid, route, clientModel string) (string, bool) {
	if sid == "" {
		return "", false
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if s, ok := m.sessions[sid]; ok {
		return s.capture.Reason, true
	}
	m.pruneRulesLocked()

	for i := range m.rules {
		r := &m.rules[i]
		if r.Used >= r.Limit {
			continue
		}
		if r.Route != "" && r.Route != route {
			continue
		}
		if r.ClientModel != "" && !globMatch(r.ClientModel, clientModel) {
			continue
		}
		if len(m.sessions) >= maxSessions || m.totalBytes >= maxTotalBytes {
			return "", false
		}
		r.Used++
		reason := "rule:" + r.ID
		sessionReason := r.Reason
		if sessionReason == "" {
			sessionReason = reason
		}
		m.sessions[sid] = newSession(sessionReason, false, m.now())
		return reason, true
	}

	switch strings.ToLower(strings.TrimSpace(m.policy.Mode)) {
	case "errors":
		if len(m.sessions) >= maxSessions || m.totalBytes >= maxTotalBytes {
			return "", false
		}
		m.sessions[sid] = newSession("mode:errors", true, m.now())
		return "mode:errors", true
	case "sample":
		if m.policy.SampleRate > 0 && m.rand() < m.policy.SampleRate {
			if len(m.sessions) >= maxSessions || m.totalBytes >= maxTotalBytes {
				return "", false
			}
			m.sessions[sid] = newSession("mode:sample", false, m.now())
			return "mode:sample", true
		}
	}
	return "", false
}

// Offer buffers one rewrite call. Rewritten is nil when the engine failed.
func (m *Manager) Offer(sid, direction string, original, rewritten []byte, ssePhase string, eventIndex int) {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, ok := m.sessions[sid]
	if !ok {
		return
	}
	if m.totalBytes+len(original)+len(rewritten) > maxTotalBytes {
		m.deleteLocked(sid)
		return
	}
	m.totalBytes += len(original) + len(rewritten)
	s.bytes += len(original) + len(rewritten)
	maxBytes := m.policy.MaxBytes

	if strings.EqualFold(strings.TrimSpace(direction), "request") {
		var trunc bool
		s.capture.RequestOriginal, trunc = appendPreview(s.capture.RequestOriginal, original, maxBytes)
		s.markTruncated(FieldRequestOriginal, trunc)
		s.capture.RequestRewritten, trunc = appendPreview(s.capture.RequestRewritten, rewritten, maxBytes)
		s.markTruncated(FieldRequestRewritten, trunc)
		if len(rewritten) > 0 {
			s.reqHashUsed = true
			s.reqHash.Write(rewritten)
		}
		return
	}

	if strings.TrimSpace(ssePhase) != "" || eventIndex > 0 {
		if len(original) > 0 {
			s.capture.SSIEvents++
		}
		if len(original) > 0 {
			var trunc bool
			if !s.sseSeen {
				s.capture.SSEFirstOriginal, trunc = clip(original, maxBytes)
				s.markTruncated(FieldSSEFirstOriginal, trunc)
				s.capture.SSEFirstRewritten, trunc = clip(rewritten, maxBytes)
				s.markTruncated(FieldSSEFirstRewritten, trunc)
				s.sseSeen = true
			}
			s.capture.SSELastOriginal, trunc = clip(original, maxBytes)
			s.markTruncated(FieldSSELastOriginal, trunc)
			s.capture.SSELastRewritten, trunc = clip(rewritten, maxBytes)
			s.markTruncated(FieldSSELastRewritten, trunc)
		}
		if len(rewritten) > 0 {
			s.respHashUsed = true
			s.respHash.Write(rewritten)
		}
		return
	}

	var trunc bool
	s.capture.ResponseOriginal, trunc = appendPreview(s.capture.ResponseOriginal, original, maxBytes)
	s.markTruncated(FieldResponseOriginal, trunc)
	s.capture.ResponseRewritten, trunc = appendPreview(s.capture.ResponseRewritten, rewritten, maxBytes)
	s.markTruncated(FieldResponseRewritten, trunc)
	if len(rewritten) > 0 {
		s.respHashUsed = true
		s.respHash.Write(rewritten)
	}
}

// Finish removes and returns the capture for sid.
func (m *Manager) Finish(sid string) (*Capture, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, ok := m.sessions[sid]
	if !ok {
		return nil, false
	}
	m.deleteLocked(sid)

	out := s.capture
	out.TruncatedFields = truncatedFields(s.truncated)
	if s.reqHashUsed {
		out.RequestSHA256 = hex.EncodeToString(s.reqHash.Sum(nil))
	}
	if s.respHashUsed {
		out.ResponseSHA256 = hex.EncodeToString(s.respHash.Sum(nil))
	}
	return &out, true
}

func newSession(reason string, onError bool, now time.Time) *session {
	return &session{
		capture:  Capture{Reason: reason, OnError: onError, CreatedAt: now},
		reqHash:  sha256.New(),
		respHash: sha256.New(),
	}
}

func (s *session) markTruncated(field string, trunc bool) {
	if !trunc {
		return
	}
	if s.truncated == nil {
		s.truncated = make(map[string]bool)
	}
	s.truncated[field] = true
}

func truncatedFields(m map[string]bool) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for field := range m {
		out = append(out, field)
	}
	sort.Strings(out)
	return out
}

func (m *Manager) deleteLocked(sid string) {
	s, ok := m.sessions[sid]
	if !ok {
		return
	}
	m.totalBytes -= s.bytes
	if m.totalBytes < 0 {
		m.totalBytes = 0
	}
	delete(m.sessions, sid)
}

func (m *Manager) pruneRulesLocked() {
	if len(m.rules) == 0 {
		return
	}
	now := m.now()
	out := m.rules[:0]
	for _, r := range m.rules {
		if r.Used >= r.Limit || !now.Before(r.Expires) {
			continue
		}
		out = append(out, r)
	}
	m.rules = out
}

func globMatch(pattern, value string) bool {
	pattern = strings.ToLower(strings.TrimSpace(pattern))
	value = strings.ToLower(strings.TrimSpace(value))
	if pattern == "" {
		return true
	}
	expr := "^" + strings.ReplaceAll(regexp.QuoteMeta(pattern), `\*`, ".*") + "$"
	re, err := regexp.Compile(expr)
	if err != nil {
		return false
	}
	return re.MatchString(value)
}

func clip(b []byte, max int) (string, bool) {
	if max <= 0 || len(b) <= max {
		return string(b), false
	}
	return string(b[:max]), true
}

func appendPreview(dst string, b []byte, max int) (string, bool) {
	if len(b) == 0 {
		return dst, false
	}
	if len(dst) >= max {
		return dst, true
	}
	if remaining := max - len(dst); len(b) > remaining {
		return dst + string(b[:remaining]), true
	}
	return dst + string(b), false
}
