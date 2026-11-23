package stats

import (
	"context"
	"time"

	"github.com/ginuerzh/cuttle/internal/agent"
)

// SubagentHealthRow is one client's marker coverage inside a window. Client is
// the label mined from user_agent; Requests counts that client's traffic and
// Marked counts the rows that carried its subagent marker.
type SubagentHealthRow struct {
	Client   string
	Requests int64
	Marked   int64
}

// SubagentHealth cross-tabulates request volume against marker coverage per
// client. It exists to separate "this client sent no subagents" from "this
// client stopped sending its marker", which the requests table alone cannot:
// subagent_source is empty in both cases.
//
// The query groups by (user_agent, subagent_source) and folds the labels in Go,
// so internal/agent stays the only place that knows how a UA maps to a client.
func (s *Store) SubagentHealth(ctx context.Context, from, to time.Time) ([]SubagentHealthRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT user_agent, subagent_source, COUNT(*)
		FROM requests WHERE ts >= ? AND ts < ?
		GROUP BY user_agent, subagent_source`, from.UnixMilli(), to.UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	byClient := make(map[string]*SubagentHealthRow)
	for rows.Next() {
		var ua, source string
		var n int64
		if err := rows.Scan(&ua, &source, &n); err != nil {
			return nil, err
		}
		client := agent.ClientOf(ua)
		if client == "" {
			continue
		}
		row, ok := byClient[client]
		if !ok {
			row = &SubagentHealthRow{Client: client}
			byClient[client] = row
		}
		row.Requests += n
		// Only this client's own marker counts. A fork or a proxy that injects
		// another client's marker must not make this client look healthy: the
		// whole point is to catch this client's header going missing.
		if source != "" && agent.SourceOfClient(client) == source {
			row.Marked += n
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Emit in the marker table's order so the console is stable across ticks.
	out := make([]SubagentHealthRow, 0, len(byClient))
	for _, client := range agent.KnownClients() {
		if row, ok := byClient[client]; ok {
			out = append(out, *row)
		}
	}
	return out, nil
}
