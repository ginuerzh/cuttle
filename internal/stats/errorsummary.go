package stats

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// ErrorGroup is one low-cardinality error bucket.
type ErrorGroup struct {
	Key      string `json:"key"`
	Requests int64  `json:"requests"`
	Errors   int64  `json:"errors"`
}

// ErrorFingerprint is one repeated error message.
type ErrorFingerprint struct {
	Class    string `json:"class"`
	Message  string `json:"message"`
	Count    int64  `json:"count"`
	LastSeen string `json:"lastSeen"`
}

// ErrorBucket is one hourly point in the error trend.
type ErrorBucket struct {
	Bucket   string `json:"bucket"`
	Requests int64  `json:"requests"`
	Errors   int64  `json:"errors"`
}

// ErrorSummary is the payload of GET /api/stats/errors.
type ErrorSummary struct {
	Total         int64              `json:"total"`
	Errors        int64              `json:"errors"`
	ErrorRate     float64            `json:"errorRate"`
	ByClass       []ErrorGroup       `json:"byClass"`
	ByProvider    []ErrorGroup       `json:"byProvider"`
	ByRoute       []ErrorGroup       `json:"byRoute"`
	ByClientModel []ErrorGroup       `json:"byClientModel"`
	TopMessages   []ErrorFingerprint `json:"topMessages"`
	Hourly        []ErrorBucket      `json:"hourly"`
}

const failedExpr = "(status >= 400 OR error != '')"

// ErrorSummary aggregates failures in [from, to). It reads the retained raw
// request rows; callers should keep the window inside retainRequests.
func (s *Store) ErrorSummary(ctx context.Context, from, to time.Time) (ErrorSummary, error) {
	var out ErrorSummary
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*),
		COALESCE(SUM(CASE WHEN `+failedExpr+` THEN 1 ELSE 0 END),0)
		FROM requests WHERE ts >= ? AND ts < ?`,
		from.UnixMilli(), to.UnixMilli()).Scan(&out.Total, &out.Errors); err != nil {
		return out, err
	}
	if out.Total > 0 {
		out.ErrorRate = float64(out.Errors) / float64(out.Total)
	}

	groupQueries := []struct {
		column string
		dst    *[]ErrorGroup
		where  string
	}{
		{"COALESCE(NULLIF(error_class,''),'unknown')", &out.ByClass, failedExpr},
		{"provider", &out.ByProvider, ""},
		{"route", &out.ByRoute, ""},
		{"client_model", &out.ByClientModel, "client_model != ''"},
	}
	for _, g := range groupQueries {
		where := "ts >= ? AND ts < ?"
		if g.where != "" {
			where += " AND " + g.where
		}
		q := fmt.Sprintf(`SELECT %s, COUNT(*),
			COALESCE(SUM(CASE WHEN %s THEN 1 ELSE 0 END),0)
			FROM requests WHERE %s GROUP BY 1 ORDER BY 3 DESC, 2 DESC`,
			g.column, failedExpr, where)
		if g.column == "provider" || g.column == "route" {
			q += " LIMIT 50"
		}
		if strings.Contains(g.column, "client_model") {
			q += " LIMIT 20"
		}
		rows, err := s.db.QueryContext(ctx, q, from.UnixMilli(), to.UnixMilli())
		if err != nil {
			return out, err
		}
		groups, err := scanErrorGroups(rows)
		rows.Close()
		if err != nil {
			return out, err
		}
		*g.dst = groups
	}

	rows, err := s.db.QueryContext(ctx, `SELECT
		COALESCE(NULLIF(error_class,''),'unknown'), error, COUNT(*), MAX(ts)
		FROM requests WHERE ts >= ? AND ts < ? AND error != ''
		GROUP BY error_class, error ORDER BY COUNT(*) DESC LIMIT 50`,
		from.UnixMilli(), to.UnixMilli())
	if err != nil {
		return out, err
	}
	fingerprints, err := scanFingerprints(rows)
	rows.Close()
	if err != nil {
		return out, err
	}
	out.TopMessages = mergeFingerprints(fingerprints, 20)

	rows, err = s.db.QueryContext(ctx, `SELECT
		strftime('%Y-%m-%dT%H:00:00Z', ts/1000, 'unixepoch'), COUNT(*),
		COALESCE(SUM(CASE WHEN `+failedExpr+` THEN 1 ELSE 0 END),0)
		FROM requests WHERE ts >= ? AND ts < ? GROUP BY 1 ORDER BY 1`,
		from.UnixMilli(), to.UnixMilli())
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var b ErrorBucket
		if err := rows.Scan(&b.Bucket, &b.Requests, &b.Errors); err != nil {
			return out, err
		}
		out.Hourly = append(out.Hourly, b)
	}
	return out, rows.Err()
}

func scanErrorGroups(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
}) ([]ErrorGroup, error) {
	var out []ErrorGroup
	for rows.Next() {
		var g ErrorGroup
		if err := rows.Scan(&g.Key, &g.Requests, &g.Errors); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func scanFingerprints(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
}) ([]ErrorFingerprint, error) {
	var out []ErrorFingerprint
	for rows.Next() {
		var class, message string
		var count, last int64
		if err := rows.Scan(&class, &message, &count, &last); err != nil {
			return nil, err
		}
		out = append(out, ErrorFingerprint{
			Class:    class,
			Message:  message,
			Count:    count,
			LastSeen: time.UnixMilli(last).UTC().Format(time.RFC3339),
		})
	}
	return out, rows.Err()
}

var (
	fingerprintUUID = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)
	fingerprintIP   = regexp.MustCompile(`\b\d{1,3}(?:\.\d{1,3}){3}(?::\d+)?\b`)
	fingerprintID   = regexp.MustCompile(`(?i)\b(request[_ -]?id|trace[_ -]?id|sid)\s*[:=]\s*\S+`)
	fingerprintHex  = regexp.MustCompile(`\b[0-9a-f]{16,}\b`)
)

// FingerprintError collapses volatile parts of an error message so repeated
// failures group together.
func FingerprintError(message string) string {
	s := strings.ToLower(strings.TrimSpace(message))
	s = fingerprintUUID.ReplaceAllString(s, "<uuid>")
	s = fingerprintIP.ReplaceAllString(s, "<ip>")
	s = fingerprintID.ReplaceAllString(s, "$1=<id>")
	s = fingerprintHex.ReplaceAllString(s, "<hex>")
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 200 {
		s = strings.TrimSpace(s[:200])
	}
	return s
}

func mergeFingerprints(in []ErrorFingerprint, limit int) []ErrorFingerprint {
	type key struct{ class, message string }
	merged := make(map[key]ErrorFingerprint, len(in))
	for _, f := range in {
		k := key{f.Class, FingerprintError(f.Message)}
		if prev, ok := merged[k]; ok {
			prev.Count += f.Count
			if f.LastSeen > prev.LastSeen {
				prev.LastSeen = f.LastSeen
			}
			merged[k] = prev
			continue
		}
		f.Message = k.message
		merged[k] = f
	}
	out := make([]ErrorFingerprint, 0, len(merged))
	for _, f := range merged {
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count == out[j].Count {
			return out[i].LastSeen > out[j].LastSeen
		}
		return out[i].Count > out[j].Count
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}
