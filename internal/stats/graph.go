package stats

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"time"
)

// NodeMetrics aggregates one graph node.
type NodeMetrics struct {
	Requests     int64 `json:"requests"`
	Errors       int64 `json:"errors"`
	P50MS        int64 `json:"p50Ms"`
	P95MS        int64 `json:"p95Ms"`
	InputTokens  int64 `json:"inputTokens"`
	OutputTokens int64 `json:"outputTokens"`
	CachedTokens int64 `json:"cachedTokens"`

	OutputTokensPerSecond float64 `json:"outputTokensPerSecond"`

	// Attempts counts discarded upstream attempts, RetriedRequests the distinct
	// parent requests behind them, and AttemptStatus those attempts by status.
	// FailureStatus counts final failed parent rows by status, with "0" for a
	// failed dial. Entrypoint nodes get the failure counts but no attempts: an
	// attempt carries no client model to attribute.
	Attempts        int64            `json:"attempts"`
	RetriedRequests int64            `json:"retriedRequests"`
	AttemptStatus   map[string]int64 `json:"attemptStatus,omitempty"`
	FailureStatus   map[string]int64 `json:"failureStatus,omitempty"`

	streamOutputTokens int64
	streamGenerationMS int64
}

// EdgeMetrics aggregates one route→node or node→provider edge.
type EdgeMetrics struct {
	Requests int64 `json:"requests"`
	Errors   int64 `json:"errors"`

	Attempts        int64 `json:"attempts"`
	RetriedRequests int64 `json:"retriedRequests"`
}

// ErrorRow is one recent failure for the monitor panel.
type ErrorRow struct {
	RequestID  string `json:"requestId"`
	TS         string `json:"ts"`
	Entrypoint string `json:"entrypoint,omitempty"`
	Route      string `json:"route"`
	Node       string `json:"node"`
	Provider   string `json:"provider"`
	Status     int    `json:"status"`
	Message    string `json:"message"`
}

// GraphMetrics is the payload of GET /api/stats/graph and /api/events.
type GraphMetrics struct {
	Nodes  map[string]NodeMetrics `json:"nodes"`
	Edges  map[string]EdgeMetrics `json:"edges"`
	Errors []ErrorRow             `json:"errors"`
}

// EntrypointResolver maps a client model to the entrypoint that handled it.
type EntrypointResolver func(clientModel string) string

// RowFilter reports whether a request row belongs to the current topology.
type RowFilter func(route, node, provider string) bool

// GraphRow is the subset of a request row needed by the live aggregator.
type GraphRow struct {
	ID           string
	TS           int64
	Route        string
	Node         string
	Provider     string
	ClientModel  string
	Status       int
	Error        string
	DurationMS   int64
	TTFTMS       int64
	InputTokens  int64
	OutputTokens int64
	CachedTokens int64
}

// GraphAttemptRow is the subset of a discarded attempt row needed by the live
// aggregator.
type GraphAttemptRow struct {
	RequestID string
	Attempt   int
	TS        int64
	Route     string
	Node      string
	Provider  string
	Status    int
}

type comboKey struct {
	route       string
	node        string
	provider    string
	clientModel string
}

type comboStats struct {
	requests     int64
	errors       int64
	failures     map[int]int64
	inputTokens  int64
	outputTokens int64
	cachedTokens int64
	durations    []int64

	streamOutputTokens int64
	streamGenerationMS int64
}

// attemptStats accumulates discarded attempts for one combo. retried holds the
// parent request ids so RetriedRequests stays a distinct count.
type attemptStats struct {
	attempts int64
	retried  map[string]struct{}
	status   map[int]int64
}

type errorSample struct {
	id          string
	ts          int64
	route       string
	node        string
	provider    string
	clientModel string
	status      int
	message     string
}

const (
	maxSeenRequestIDs = 200_000
	maxSeenAttempts   = 200_000
	maxErrorSamples   = 200
)

// seenSet remembers at most max keys and evicts them in insertion order, so a
// live session that runs for days keeps its deduplication memory bounded.
type seenSet struct {
	keys  map[string]struct{}
	order []string
	max   int
	next  int
}

// newSeenSet bounds the set to max keys. max must be positive.
func newSeenSet(max int) *seenSet {
	return &seenSet{
		keys: make(map[string]struct{}),
		max:  max,
	}
}

// add records key and reports whether it was new. A key evicted by the bound is
// reported as new again: the bound caps memory, not exactness over a session
// long enough to evict.
func (s *seenSet) add(key string) bool {
	if _, ok := s.keys[key]; ok {
		return false
	}
	s.keys[key] = struct{}{}
	if len(s.order) < s.max {
		s.order = append(s.order, key)
	} else if len(s.order) > 0 {
		evicted := s.order[s.next]
		delete(s.keys, evicted)
		s.order[s.next] = key
		s.next = (s.next + 1) % len(s.order)
	}
	return true
}

// Aggregator accumulates request rows by their raw dimensions. Entrypoint
// attribution and topology filtering are applied in Metrics, so a config
// change is reflected without rebuilding the aggregate.
type Aggregator struct {
	combos       map[comboKey]*comboStats
	attempts     map[comboKey]*attemptStats
	errors       []errorSample
	seen         *seenSet
	seenAttempts *seenSet
}

// NewAggregator returns an empty aggregator.
func NewAggregator() *Aggregator {
	return &Aggregator{
		combos: make(map[comboKey]*comboStats),
		errors: make([]errorSample, 0),
		seen:   newSeenSet(maxSeenRequestIDs),
	}
}

// Add accumulates one request row. A duplicate request ID is ignored; the
// recorder writes one row per request, but INSERT OR REPLACE can reinsert an
// existing row with a new rowid.
func (a *Aggregator) Add(row GraphRow) {
	if a.combos == nil {
		a.combos = make(map[comboKey]*comboStats)
	}
	if a.seen == nil {
		a.seen = newSeenSet(maxSeenRequestIDs)
	}

	if row.ID != "" && !a.seen.add(row.ID) {
		return
	}

	key := comboKey{
		route:       row.Route,
		node:        row.Node,
		provider:    row.Provider,
		clientModel: row.ClientModel,
	}
	c := a.combos[key]
	if c == nil {
		c = &comboStats{}
		a.combos[key] = c
	}
	c.requests++
	failed := row.Status >= 400 || row.Error != ""
	if failed {
		c.errors++
		if c.failures == nil {
			c.failures = make(map[int]int64)
		}
		c.failures[row.Status]++
	}
	c.inputTokens += row.InputTokens
	c.outputTokens += row.OutputTokens
	c.cachedTokens += row.CachedTokens
	c.durations = append(c.durations, row.DurationMS)
	if row.TTFTMS > 0 && row.OutputTokens > 0 && row.DurationMS > row.TTFTMS {
		c.streamOutputTokens += row.OutputTokens
		c.streamGenerationMS += row.DurationMS - row.TTFTMS
	}

	if failed {
		a.errors = append(a.errors, errorSample{
			id:          row.ID,
			ts:          row.TS,
			route:       row.Route,
			node:        row.Node,
			provider:    row.Provider,
			clientModel: row.ClientModel,
			status:      row.Status,
			message:     row.Error,
		})
		if len(a.errors) > maxErrorSamples {
			oldest := 0
			for i := 1; i < len(a.errors); i++ {
				if a.errors[i].ts < a.errors[oldest].ts {
					oldest = i
				}
			}
			a.errors = append(a.errors[:oldest], a.errors[oldest+1:]...)
		}
	}
}

// AddAttempt accumulates one discarded attempt row. Repeated (request ID,
// attempt index) deliveries are ignored: the recorder writes attempts beside
// their parent, and INSERT OR REPLACE reinserts both with a new sequence, so
// the live tail can hand the same attempt over twice.
func (a *Aggregator) AddAttempt(row GraphAttemptRow) {
	if a.attempts == nil {
		a.attempts = make(map[comboKey]*attemptStats)
	}
	if a.seenAttempts == nil {
		a.seenAttempts = newSeenSet(maxSeenAttempts)
	}
	if row.RequestID != "" && !a.seenAttempts.add(row.RequestID+"\x00"+strconv.Itoa(row.Attempt)) {
		return
	}

	key := comboKey{route: row.Route, node: row.Node, provider: row.Provider}
	s := a.attempts[key]
	if s == nil {
		s = &attemptStats{retried: make(map[string]struct{}), status: make(map[int]int64)}
		a.attempts[key] = s
	}
	s.attempts++
	s.status[row.Status]++
	if row.RequestID != "" {
		s.retried[row.RequestID] = struct{}{}
	}
}

// mergeStatus adds integer-keyed counters into a string-keyed status map,
// allocating dst on first use. Status maps serialize as JSON objects, so their
// keys are decimal strings.
func mergeStatus(dst map[string]int64, src map[int]int64) map[string]int64 {
	if len(src) == 0 {
		return dst
	}
	if dst == nil {
		dst = make(map[string]int64, len(src))
	}
	for status, count := range src {
		dst[strconv.Itoa(status)] += count
	}
	return dst
}

// Metrics expands the aggregate into graph nodes/edges. When
// recomputePercentiles is false, percentiles are copied from prev for keys
// that already exist; new keys are still computed.
func (a *Aggregator) Metrics(resolve EntrypointResolver, include RowFilter, prev GraphMetrics, recomputePercentiles bool) GraphMetrics {
	g := GraphMetrics{
		Nodes:  make(map[string]NodeMetrics),
		Edges:  make(map[string]EdgeMetrics),
		Errors: []ErrorRow{},
	}
	durations := make(map[string][]int64)
	needsPercentiles := func(key string) bool {
		if recomputePercentiles {
			return true
		}
		_, ok := prev.Nodes[key]
		return !ok
	}

	addNode := func(key string, c *comboStats) {
		n := g.Nodes[key]
		n.Requests += c.requests
		n.Errors += c.errors
		n.FailureStatus = mergeStatus(n.FailureStatus, c.failures)
		n.InputTokens += c.inputTokens
		n.OutputTokens += c.outputTokens
		n.CachedTokens += c.cachedTokens
		n.streamOutputTokens += c.streamOutputTokens
		n.streamGenerationMS += c.streamGenerationMS
		g.Nodes[key] = n
		if needsPercentiles(key) {
			durations[key] = append(durations[key], c.durations...)
		}
	}
	addEdge := func(key string, c *comboStats) {
		e := g.Edges[key]
		e.Requests += c.requests
		e.Errors += c.errors
		g.Edges[key] = e
	}
	// A parent request can appear in several combos (two nodes behind one
	// provider, two providers behind one route), so retriedRequests is the size
	// of the union per graph key, not a sum of combo counts.
	nodeParents := make(map[string]map[string]struct{})
	edgeParents := make(map[string]map[string]struct{})
	unionParents := func(dst map[string]map[string]struct{}, key string, src map[string]struct{}) {
		if len(src) == 0 {
			return
		}
		parents := dst[key]
		if parents == nil {
			parents = make(map[string]struct{}, len(src))
			dst[key] = parents
		}
		for id := range src {
			parents[id] = struct{}{}
		}
	}
	addAttemptNode := func(key string, s *attemptStats) {
		n := g.Nodes[key]
		n.Attempts += s.attempts
		n.AttemptStatus = mergeStatus(n.AttemptStatus, s.status)
		g.Nodes[key] = n
		unionParents(nodeParents, key, s.retried)
	}
	addAttemptEdge := func(key string, s *attemptStats) {
		e := g.Edges[key]
		e.Attempts += s.attempts
		g.Edges[key] = e
		unionParents(edgeParents, key, s.retried)
	}

	for key, c := range a.combos {
		if include != nil && !include(key.route, key.node, key.provider) {
			continue
		}

		addNode("provider:"+key.provider, c)
		if key.node != "" {
			addNode("node:"+key.node, c)
		}
		routeKey := "route:" + key.route
		if key.route == "" {
			routeKey = "unrouted"
		}
		addNode(routeKey, c)

		if resolve != nil {
			if match := resolve(key.clientModel); match != "" {
				entrypointKey := "entrypoint:" + match
				addNode(entrypointKey, c)
				if key.route == "" {
					addEdge(entrypointKey+"->unrouted", c)
				} else {
					addEdge(entrypointKey+"->route:"+key.route, c)
				}
			}
		}

		if key.route == "" {
			addEdge("unrouted|provider:"+key.provider, c)
		} else if key.node != "" {
			addEdge("route:"+key.route+"|node:"+key.node, c)
			addEdge("node:"+key.node+"|provider:"+key.provider, c)
		}
	}

	// Attempts mirror the request combos, minus entrypoint attribution: an
	// attempt carries no client model. They obey the same topology filter, so a
	// node removed from the graph cannot reappear through a stale attempt.
	for key, s := range a.attempts {
		if include != nil && !include(key.route, key.node, key.provider) {
			continue
		}

		addAttemptNode("provider:"+key.provider, s)
		if key.node != "" {
			addAttemptNode("node:"+key.node, s)
		}
		routeKey := "route:" + key.route
		if key.route == "" {
			routeKey = "unrouted"
		}
		addAttemptNode(routeKey, s)

		if key.route == "" {
			addAttemptEdge("unrouted|provider:"+key.provider, s)
		} else if key.node != "" {
			addAttemptEdge("route:"+key.route+"|node:"+key.node, s)
			addAttemptEdge("node:"+key.node+"|provider:"+key.provider, s)
		}
	}

	for key, n := range g.Nodes {
		if parents := nodeParents[key]; len(parents) > 0 {
			n.RetriedRequests = int64(len(parents))
			g.Nodes[key] = n
		}
	}
	for key, e := range g.Edges {
		if parents := edgeParents[key]; len(parents) > 0 {
			e.RetriedRequests = int64(len(parents))
			g.Edges[key] = e
		}
	}

	for key, n := range g.Nodes {
		if !recomputePercentiles {
			if p, ok := prev.Nodes[key]; ok {
				n.P50MS = p.P50MS
				n.P95MS = p.P95MS
				g.Nodes[key] = n
				continue
			}
		}
		ds := durations[key]
		sort.Slice(ds, func(i, j int) bool { return ds[i] < ds[j] })
		n.P50MS = percentileSorted(ds, 0.50)
		n.P95MS = percentileSorted(ds, 0.95)
		g.Nodes[key] = n
	}

	for key, n := range g.Nodes {
		if n.streamGenerationMS > 0 {
			n.OutputTokensPerSecond = float64(n.streamOutputTokens) * 1000 / float64(n.streamGenerationMS)
		}
		g.Nodes[key] = n
	}

	samples := append([]errorSample(nil), a.errors...)
	sort.SliceStable(samples, func(i, j int) bool { return samples[i].ts > samples[j].ts })
	for _, e := range samples {
		if include != nil && !include(e.route, e.node, e.provider) {
			continue
		}
		entrypoint := ""
		if resolve != nil {
			entrypoint = resolve(e.clientModel)
		}
		msg := e.message
		if msg == "" {
			msg = http.StatusText(e.status)
		}
		g.Errors = append(g.Errors, ErrorRow{
			RequestID:  e.id,
			TS:         time.UnixMilli(e.ts).UTC().Format(time.RFC3339),
			Entrypoint: entrypoint,
			Route:      e.route,
			Node:       e.node,
			Provider:   e.provider,
			Status:     e.status,
			Message:    msg,
		})
		if len(g.Errors) >= 20 {
			break
		}
	}

	return g
}

// Graph aggregates the request detail into node/edge metrics for a window.
func (s *Store) Graph(ctx context.Context, from, to time.Time) (GraphMetrics, error) {
	return s.GraphWithEntrypoints(ctx, from, to, nil)
}

// GraphWithEntrypoints also attributes requests to entrypoint nodes when
// resolve can map the client model back to an entrypoint name.
func (s *Store) GraphWithEntrypoints(ctx context.Context, from, to time.Time, resolve EntrypointResolver) (GraphMetrics, error) {
	return s.GraphWithFilter(ctx, from, to, resolve, nil)
}

// GraphWithFilter drops rows outside the current topology before aggregating.
func (s *Store) GraphWithFilter(ctx context.Context, from, to time.Time, resolve EntrypointResolver, include RowFilter) (GraphMetrics, error) {
	agg, _, err := s.GraphAggregate(ctx, from, to)
	if err != nil {
		return GraphMetrics{
			Nodes:  make(map[string]NodeMetrics),
			Edges:  make(map[string]EdgeMetrics),
			Errors: []ErrorRow{},
		}, err
	}
	return agg.Metrics(resolve, include, GraphMetrics{}, true), nil
}

// GraphAggregate loads the window snapshot and returns the highest sequence
// covered by it. The caller tails from that sequence.
func (s *Store) GraphAggregate(ctx context.Context, from, to time.Time) (*Aggregator, int64, error) {
	var high int64
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq), 0) FROM requests`).Scan(&high); err != nil {
		return nil, 0, err
	}

	agg := NewAggregator()
	rows, err := s.db.QueryContext(ctx, `SELECT request_id, ts, route, node, provider, client_model, status, error,
		duration_ms, ttft_ms, input_tokens, output_tokens, cached_tokens
		FROM requests WHERE seq <= ? AND ts >= ? AND ts < ? ORDER BY ts DESC`,
		high, from.UnixMilli(), to.UnixMilli())
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	for rows.Next() {
		var row GraphRow
		var errStr string
		if err := rows.Scan(&row.ID, &row.TS, &row.Route, &row.Node, &row.Provider, &row.ClientModel,
			&row.Status, &errStr, &row.DurationMS, &row.TTFTMS, &row.InputTokens, &row.OutputTokens, &row.CachedTokens); err != nil {
			return nil, 0, err
		}
		row.Error = errStr
		agg.Add(row)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	// The pool holds a single connection, so the request rows must be released
	// before the attempt read can run.
	if err := rows.Close(); err != nil {
		return nil, 0, err
	}

	attempts, err := s.db.QueryContext(ctx, `SELECT request_id, attempt, ts, route, node, provider, status
		FROM request_attempts WHERE seq <= ? AND seq > 0 AND ts >= ? AND ts < ?`,
		high, from.UnixMilli(), to.UnixMilli())
	if err != nil {
		return nil, 0, err
	}
	defer attempts.Close()

	for attempts.Next() {
		var row GraphAttemptRow
		if err := attempts.Scan(&row.RequestID, &row.Attempt, &row.TS, &row.Route, &row.Node,
			&row.Provider, &row.Status); err != nil {
			return nil, 0, err
		}
		agg.AddAttempt(row)
	}
	if err := attempts.Err(); err != nil {
		return nil, 0, err
	}
	return agg, high, nil
}

// ForEachRequestAfter visits every row above afterSeq in insertion order
// and returns the highest sequence scanned. It deliberately does not filter by
// ts: rows outside the window must still advance the cursor, or they would be
// rescanned forever.
func (s *Store) ForEachRequestAfter(ctx context.Context, afterSeq int64, fn func(GraphRow)) (int64, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT seq, request_id, ts, route, node, provider, client_model, status, error,
		duration_ms, ttft_ms, input_tokens, output_tokens, cached_tokens
		FROM requests WHERE seq > ? ORDER BY seq`, afterSeq)
	if err != nil {
		return afterSeq, err
	}
	defer rows.Close()

	last := afterSeq
	for rows.Next() {
		var seq int64
		var row GraphRow
		var errStr string
		if err := rows.Scan(&seq, &row.ID, &row.TS, &row.Route, &row.Node, &row.Provider, &row.ClientModel,
			&row.Status, &errStr, &row.DurationMS, &row.TTFTMS, &row.InputTokens, &row.OutputTokens, &row.CachedTokens); err != nil {
			return last, err
		}
		row.Error = errStr
		if seq > last {
			last = seq
		}
		fn(row)
	}
	if err := rows.Err(); err != nil {
		return last, err
	}
	return last, nil
}

// ForEachAttemptAfter visits every discarded attempt above afterSeq in
// insertion order and returns the highest sequence scanned. An attempt reuses
// its parent request's sequence, so the live path tails both tables with the
// same watermark; like ForEachRequestAfter it does not filter by ts, because a
// row outside the window must still advance the cursor.
func (s *Store) ForEachAttemptAfter(ctx context.Context, afterSeq int64, fn func(GraphAttemptRow)) (int64, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT seq, request_id, attempt, ts, route, node, provider, status
		FROM request_attempts WHERE seq > ? ORDER BY seq`, afterSeq)
	if err != nil {
		return afterSeq, err
	}
	defer rows.Close()

	last := afterSeq
	for rows.Next() {
		var seq int64
		var row GraphAttemptRow
		if err := rows.Scan(&seq, &row.RequestID, &row.Attempt, &row.TS, &row.Route, &row.Node,
			&row.Provider, &row.Status); err != nil {
			return last, err
		}
		if seq > last {
			last = seq
		}
		fn(row)
	}
	if err := rows.Err(); err != nil {
		return last, err
	}
	return last, nil
}

func percentileSorted(sorted []int64, p float64) int64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(p * float64(len(sorted)))
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}
