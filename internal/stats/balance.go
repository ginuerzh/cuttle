package stats

import (
	"context"
	"time"
)

const (
	// balancePointGap is the shortest spacing the curve keeps between readings.
	// It matches config.UsageSampleMin, so a sampler at the floor still adds one
	// point per interval.
	balancePointGap = 5 * time.Minute
	// DefaultBalanceSeriesPoints bounds one returned trend.
	DefaultBalanceSeriesPoints = 180
)

// BalancePoint is one reading of a provider key's balance: Amount was left in
// Currency at At. Provider and Key are entity ids, never display names.
type BalancePoint struct {
	Provider string
	Key      string
	Currency string
	At       time.Time
	Amount   float64
}

// BalanceGroup is one (provider, key, currency) line of the chart, points
// ordered oldest first.
type BalanceGroup struct {
	Provider string
	Key      string
	Currency string
	Points   []BalancePoint
}

// mergeBalancePoint appends next to pts, or moves the last point to it when the
// new reading is too close to be worth its own point.
//
// The gap is measured against the second-to-last point, not the last: a page
// refreshed every minute would otherwise replace the last point forever and keep
// no history. A rise above the last reading is always appended, so a top-up is
// never swallowed, and a run of equal amounts keeps one sliding point.
func mergeBalancePoint(pts []BalancePoint, next BalancePoint) []BalancePoint {
	k := len(pts)
	if k > 0 && !next.At.After(pts[k-1].At) {
		return pts
	}
	if k >= 2 {
		prev, last := pts[k-2], pts[k-1]
		flat := next.Amount == last.Amount && last.Amount == prev.Amount
		if flat || (next.At.Sub(prev.At) < balancePointGap && next.Amount <= last.Amount) {
			pts[k-1] = next
			return pts
		}
	}
	return append(pts, next)
}

// RecordBalancePoints stores readings, deduplicated per (provider, key,
// currency). The store prunes nothing: retention belongs to Cleanup alone, so
// raising stats.retainRollups really extends the history.
func (s *Store) RecordBalancePoints(ctx context.Context, points []BalancePoint) error {
	if len(points) == 0 {
		return nil
	}

	type groupKey struct{ provider, key, currency string }
	var order []groupKey
	batches := make(map[groupKey][]BalancePoint)
	for _, p := range points {
		p.At = p.At.UTC().Truncate(time.Millisecond)
		gk := groupKey{p.Provider, p.Key, p.Currency}
		if _, ok := batches[gk]; !ok {
			order = append(order, gk)
		}
		batches[gk] = append(batches[gk], p)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, gk := range order {
		rows, err := tx.QueryContext(ctx, `SELECT at, amount FROM balance_points
			WHERE provider = ? AND key = ? AND currency = ?
			ORDER BY at DESC LIMIT 2`,
			gk.provider, gk.key, gk.currency)
		if err != nil {
			return err
		}
		var tail []BalancePoint
		for rows.Next() {
			var (
				ms     int64
				amount float64
			)
			if err := rows.Scan(&ms, &amount); err != nil {
				rows.Close()
				return err
			}
			tail = append(tail, BalancePoint{
				Provider: gk.provider, Key: gk.key, Currency: gk.currency,
				At: time.UnixMilli(ms).UTC(), Amount: amount,
			})
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		// The query returns newest first; the merge wants oldest first.
		for i, j := 0, len(tail)-1; i < j; i, j = i+1, j-1 {
			tail[i], tail[j] = tail[j], tail[i]
		}

		for _, p := range batches[gk] {
			before := len(tail)
			var last BalancePoint
			if before > 0 {
				last = tail[before-1]
			}
			tail = mergeBalancePoint(tail, p)
			after := len(tail)
			switch {
			case after == before+1:
				// a new reading
			case after == before && before > 0 && tail[after-1].At.Equal(p.At):
				// the last point moved to this reading
				if _, err := tx.ExecContext(ctx, `DELETE FROM balance_points
					WHERE provider = ? AND key = ? AND currency = ? AND at = ?`,
					gk.provider, gk.key, gk.currency, last.At.UnixMilli()); err != nil {
					return err
				}
			default:
				// too old to matter
				continue
			}
			if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO balance_points
				(provider, key, currency, at, amount) VALUES (?, ?, ?, ?, ?)`,
				p.Provider, p.Key, p.Currency, p.At.UnixMilli(), p.Amount); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// BalanceSeries returns the readings of every (provider, key, currency) group in
// [from, to), oldest first, downsampled to at most maxPoints entries per group
// (the last reading of each bucket).
func (s *Store) BalanceSeries(ctx context.Context, from, to time.Time, maxPoints int) ([]BalanceGroup, error) {
	out := []BalanceGroup{}
	if maxPoints <= 0 {
		maxPoints = DefaultBalanceSeriesPoints
	}
	fromMS := from.UnixMilli()
	width := int64(1)
	if d := to.UnixMilli() - fromMS; d > int64(maxPoints) {
		width = d / int64(maxPoints)
	}

	rows, err := s.db.QueryContext(ctx, `SELECT provider, key, currency, at, amount
		FROM balance_points WHERE at >= ? AND at < ?
		ORDER BY provider, key, currency, at`, fromMS, to.UnixMilli())
	if err != nil {
		return out, err
	}
	defer rows.Close()

	var (
		cur        *BalanceGroup
		lastBucket int64 = -1
	)
	for rows.Next() {
		var (
			g      BalanceGroup
			ms     int64
			amount float64
		)
		if err := rows.Scan(&g.Provider, &g.Key, &g.Currency, &ms, &amount); err != nil {
			return out, err
		}
		if cur == nil || cur.Provider != g.Provider || cur.Key != g.Key || cur.Currency != g.Currency {
			out = append(out, BalanceGroup{Provider: g.Provider, Key: g.Key, Currency: g.Currency, Points: []BalancePoint{}})
			cur = &out[len(out)-1]
			lastBucket = -1
		}
		point := BalancePoint{
			Provider: g.Provider, Key: g.Key, Currency: g.Currency,
			At: time.UnixMilli(ms).UTC(), Amount: amount,
		}
		bucket := (ms - fromMS) / width
		if bucket == lastBucket && len(cur.Points) > 0 {
			cur.Points[len(cur.Points)-1] = point
			continue
		}
		cur.Points = append(cur.Points, point)
		lastBucket = bucket
	}
	return out, rows.Err()
}
