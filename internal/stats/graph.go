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
	RequestID string `json:"requestId"`
	TS        string `json:"ts"`
	Route     string `json:"route"`
	Node      string `json:"node"`
	Provider  string `json:"provider"`
	Status    int    `json:"status"`
	Message   string `json:"message"`
}

// GraphMetrics is the payload of GET /api/stats/graph.
type GraphMetrics struct {
	Nodes  map[string]NodeMetrics `json:"nodes"`
	Edges  map[string]EdgeMetrics `json:"edges"`
	Errors []ErrorRow             `json:"errors"`
}

// Graph aggregates the request detail into node/edge metrics for a window.
func (s *Store) Graph(ctx context.Context, from, to time.Time) (GraphMetrics, error) {
	g := GraphMetrics{
		Nodes:  make(map[string]NodeMetrics),
		Edges:  make(map[string]EdgeMetrics),
		Errors: []ErrorRow{},
	}

	rows, err := s.db.QueryContext(ctx, `SELECT request_id, ts, route, node, provider, status, error,
		duration_ms, input_tokens, output_tokens, cached_tokens
		FROM requests WHERE ts >= ? AND ts < ? ORDER BY ts DESC`,
		from.UnixMilli(), to.UnixMilli())
	if err != nil {
		return g, err
	}
	defer rows.Close()

	durations := make(map[string][]int64)
	addNode := func(key string, failed bool, in, out, cached, dur int64) {
		n := g.Nodes[key]
		n.Requests++
		if failed {
			n.Errors++
		}
		n.InputTokens += in
		n.OutputTokens += out
		n.CachedTokens += cached
		g.Nodes[key] = n
		durations[key] = append(durations[key], dur)
	}

	for rows.Next() {
		var id string
		var ts int64
		var route, node, provider, errStr string
		var status int
		var dur, in, out, cached int64
		if err := rows.Scan(&id, &ts, &route, &node, &provider, &status, &errStr,
			&dur, &in, &out, &cached); err != nil {
			return g, err
		}

		failed := status >= 400 || errStr != ""
		addNode("provider:"+provider, failed, in, out, cached, dur)
		if node != "" {
			addNode("node:"+node, failed, in, out, cached, dur)
		}
		routeKey := "route:" + route
		if route == "" {
			routeKey = "unrouted"
		}
		addNode(routeKey, failed, in, out, cached, dur)

		if route == "" {
			e := g.Edges["unrouted|provider:"+provider]
			e.Requests++
			if failed {
				e.Errors++
			}
			g.Edges["unrouted|provider:"+provider] = e
		} else if node != "" {
			key := "route:" + route + "|node:" + node
			e := g.Edges[key]
			e.Requests++
			if failed {
				e.Errors++
			}
			g.Edges[key] = e

			key = "node:" + node + "|provider:" + provider
			e = g.Edges[key]
			e.Requests++
			if failed {
				e.Errors++
			}
			g.Edges[key] = e
		}

		if failed && len(g.Errors) < 20 {
			msg := errStr
			if msg == "" {
				msg = http.StatusText(status)
			}
			g.Errors = append(g.Errors, ErrorRow{
				RequestID: id,
				TS:        time.UnixMilli(ts).UTC().Format(time.RFC3339),
				Route:     route,
				Node:      node,
				Provider:  provider,
				Status:    status,
				Message:   msg,
			})
		}
	}
	if err := rows.Err(); err != nil {
		return g, err
	}

	for key, ds := range durations {
		n := g.Nodes[key]
		sort.Slice(ds, func(i, j int) bool { return ds[i] < ds[j] })
		n.P50MS = percentileSorted(ds, 0.50)
		n.P95MS = percentileSorted(ds, 0.95)
		g.Nodes[key] = n
	}
	return g, nil
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
