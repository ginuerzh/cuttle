package stats

import (
	"context"
	"time"
)

// ProbeResult is one health probe result.
type ProbeResult struct {
	TS        time.Time
	Provider  string
	OK        bool
	Status    int
	LatencyMS int64
	Error     string
}

// InsertProbe stores a probe result.
func (s *Store) InsertProbe(ctx context.Context, r ProbeResult) error {
	if r.TS.IsZero() {
		r.TS = time.Now()
	}
	ok := 0
	if r.OK {
		ok = 1
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO probe_results
		(ts, provider, ok, status, latency_ms, err) VALUES (?,?,?,?,?,?)`,
		r.TS.UnixMilli(), r.Provider, ok, r.Status, r.LatencyMS, r.Error)
	return err
}

// LatestProbes returns the most recent probe per provider.
func (s *Store) LatestProbes(ctx context.Context) (map[string]ProbeResult, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT provider, ok, status, latency_ms, err, MAX(ts)
		FROM probe_results GROUP BY provider`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string]ProbeResult)
	for rows.Next() {
		var r ProbeResult
		var ok int
		var ts int64
		if err := rows.Scan(&r.Provider, &ok, &r.Status, &r.LatencyMS, &r.Error, &ts); err != nil {
			return nil, err
		}
		r.OK = ok != 0
		r.TS = time.UnixMilli(ts).UTC()
		out[r.Provider] = r
	}
	return out, rows.Err()
}
