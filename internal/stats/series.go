package stats

import (
	"context"
	"time"
)

// SeriesPoint is one bucket of the session time series. Bucket is the start of
// the bucket in UTC.
type SeriesPoint struct {
	Bucket   time.Time `json:"bucket"`
	Requests int64     `json:"requests"`
	Errors   int64     `json:"errors"`
}

// Series is the payload of GET /api/stats/series. Only non-empty buckets are
// present, in ascending order: the caller pads the gaps so a quiet window is
// visibly zero instead of absent.
type Series struct {
	Points []SeriesPoint `json:"points"`
}

// Series aggregates the filtered request rows into fixed-width buckets. It
// reuses the list endpoint's filter (window, provider, route, node, key, client
// model, error class, only-errors, captured) so a chart and its list can never
// disagree.
func (s *Store) Series(ctx context.Context, f Filter, bucket time.Duration) (Series, error) {
	out := Series{Points: []SeriesPoint{}}

	width := bucket.Milliseconds()
	if width <= 0 {
		width = time.Hour.Milliseconds()
	}
	where, args := f.where()
	q := `SELECT (ts / ?) * ? AS b, COUNT(*),
		COALESCE(SUM(CASE WHEN ` + failedExpr + ` THEN 1 ELSE 0 END),0)
		FROM requests` + where + ` GROUP BY b ORDER BY b`
	queryArgs := append([]any{width, width}, args...)

	rows, err := s.db.QueryContext(ctx, q, queryArgs...)
	if err != nil {
		return out, err
	}
	defer rows.Close()

	for rows.Next() {
		var (
			ms    int64
			point SeriesPoint
		)
		if err := rows.Scan(&ms, &point.Requests, &point.Errors); err != nil {
			return out, err
		}
		point.Bucket = time.UnixMilli(ms).UTC()
		out.Points = append(out.Points, point)
	}
	return out, rows.Err()
}
