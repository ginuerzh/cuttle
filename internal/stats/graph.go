package stats

import (
	"context"
	"net/http"
	"sort"
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
}

// EdgeMetrics aggregates one route→node or node→provider edge.
type EdgeMetrics struct {
	Requests int64 `json:"requests"`
	Errors   int64 `json:"errors"`
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

type comboKey struct {
	route       string
	node        string
	provider    string
	clientModel string
}

type comboStats struct {
	requests     int64
	errors       int64
	inputTokens  int64
	outputTokens int64
	cachedTokens int64
	durations    []int64
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
	maxErrorSamples   = 200
)

// Aggregator accumulates request rows by their raw dimensions. Entrypoint
// attribution and topology filtering are applied in Metrics, so a config
// change is reflected without rebuilding the aggregate.
type Aggregator struct {
	combos    map[comboKey]*comboStats
	errors    []errorSample
	seen      map[string]struct{}
	seenOrder []string
	seenNext  int
}

// NewAggregator returns an empty aggregator.
func NewAggregator() *Aggregator {
	return &Aggregator{
		combos: make(map[comboKey]*comboStats),
		errors: make([]errorSample, 0),
		seen:   make(map[string]struct{}),
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
		a.seen = make(map[string]struct{})
	}

	if row.ID != "" {
		if _, ok := a.seen[row.ID]; ok {
			return
		}
		a.seen[row.ID] = struct{}{}
		if len(a.seenOrder) < maxSeenRequestIDs {
			a.seenOrder = append(a.seenOrder, row.ID)
		} else {
			evicted := a.seenOrder[a.seenNext]
			delete(a.seen, evicted)
			a.seenOrder[a.seenNext] = row.ID
			a.seenNext = (a.seenNext + 1) % maxSeenRequestIDs
		}
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
	}
	c.inputTokens += row.InputTokens
	c.outputTokens += row.OutputTokens
	c.cachedTokens += row.CachedTokens
	c.durations = append(c.durations, row.DurationMS)

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
		n.InputTokens += c.inputTokens
		n.OutputTokens += c.outputTokens
		n.CachedTokens += c.cachedTokens
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

		if resolve != nil && key.clientModel != "" {
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

	samples := append([]errorSample(nil), a.errors...)
	sort.SliceStable(samples, func(i, j int) bool { return samples[i].ts > samples[j].ts })
	for _, e := range samples {
		if include != nil && !include(e.route, e.node, e.provider) {
			continue
		}
		entrypoint := ""
		if resolve != nil && e.clientModel != "" {
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
