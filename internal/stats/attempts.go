package stats

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"
)

// Attempt is one discarded upstream attempt: the response opted into a retry,
// so it never reached the client and is stored beside its parent request.
//
// TS is the parent request's start time. Insert always stores the parent's ts
// and ignores any value set here, because retention and the graph time windows
// filter on it; the read side scans it back.
type Attempt struct {
	RequestID       string            `json:"requestId"`
	Attempt         int               `json:"attempt"`
	TS              time.Time         `json:"ts"`
	Route           string            `json:"route,omitempty"`
	Node            string            `json:"node,omitempty"`
	Provider        string            `json:"provider,omitempty"`
	GatewayKey      string            `json:"gatewayKey,omitempty"`
	Status          int               `json:"status"`
	Error           string            `json:"error,omitempty"`
	ErrorClass      string            `json:"errorClass,omitempty"`
	DurationMS      int64             `json:"durationMs"`
	BytesIn         int64             `json:"bytesIn"`
	BytesOut        int64             `json:"bytesOut"`
	Upstream        string            `json:"upstream,omitempty"`
	ResponseHeaders map[string]string `json:"responseHeaders,omitempty"`
	BodyPreview     string            `json:"bodyPreview,omitempty"`
}

// attemptMessage is the short human-readable reason stored with an attempt. It
// prefers the upstream error object (error.message, then error.type), then a
// top-level message, and otherwise falls back to the raw body: a non-JSON body
// (an HTML error page, a plain-text refusal) is kept as its first line, trimmed
// to 200 characters. The full body stays in the preview.
func attemptMessage(body []byte) string {
	var payload struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
		} `json:"error"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &payload); err == nil {
		if msg := firstNonEmpty(payload.Error.Message, payload.Error.Type, payload.Message); msg != "" {
			return msg
		}
	}
	line := string(body)
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	line = strings.TrimSpace(line)
	if runes := []rune(line); len(runes) > 200 {
		line = string(runes[:200])
	}
	return line
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// AttemptFilter selects attempts for the admin API and the node panel. Route,
// Node and Provider are entity ids, like Filter.
type AttemptFilter struct {
	From, To              time.Time
	Route, Node, Provider string
	Status                int
	ErrorClass            string
	Limit, Offset         int
}

// attemptColumns is the column list every attempt query scans, in Attempt
// field order.
const attemptColumns = `request_id, attempt, ts, route, node, provider, gateway_key,
	status, error, error_class, duration_ms, bytes_in, bytes_out, upstream,
	response_headers, body_preview`

// insertAttempts writes the discarded attempts of one client request inside the
// parent's transaction, with the parent's seq so the live event path can tail
// them with the same watermark. Every attempt carries the parent's ts, which
// retention and the graph windows rely on. Attempts never touch rollups.
func insertAttempts(ctx context.Context, tx *sql.Tx, r Request, seq int64) error {
	if len(r.Attempts) == 0 {
		return nil
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT OR REPLACE INTO request_attempts
		(request_id, attempt, seq, ts, route, node, provider, gateway_key, status,
		 error, error_class, duration_ms, bytes_in, bytes_out, upstream,
		 response_headers, body_preview)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, a := range r.Attempts {
		requestID := a.RequestID
		if requestID == "" {
			requestID = r.ID
		}
		route := a.Route
		if route == "" {
			route = r.Route
		}
		node := a.Node
		if node == "" {
			node = r.Node
		}
		provider := a.Provider
		if provider == "" {
			provider = r.Provider
		}
		gatewayKey := a.GatewayKey
		if gatewayKey == "" {
			gatewayKey = r.GatewayKey
		}
		if _, err := stmt.ExecContext(ctx, requestID, a.Attempt, seq, r.TS.UnixMilli(),
			route, node, provider, gatewayKey, a.Status, a.Error, a.ErrorClass,
			a.DurationMS, a.BytesIn, a.BytesOut, a.Upstream,
			marshalHeaders(a.ResponseHeaders), a.BodyPreview); err != nil {
			return err
		}
	}
	return nil
}

// AttemptsForRequest returns one request's attempts ordered by attempt
// ascending, so they read in the order they were discarded.
func (s *Store) AttemptsForRequest(ctx context.Context, requestID string) ([]Attempt, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+attemptColumns+`
		FROM request_attempts WHERE request_id = ? ORDER BY attempt`, requestID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAttempts(rows)
}

// Attempts returns attempts newest first, filtered by f.
func (s *Store) Attempts(ctx context.Context, f AttemptFilter) ([]Attempt, error) {
	where, args := f.where()
	limit := f.Limit
	if limit <= 0 {
		limit = 100
	} else if limit > 1000 {
		limit = 1000
	}
	q := `SELECT ` + attemptColumns + ` FROM request_attempts` + where +
		` ORDER BY ts DESC, request_id, attempt DESC LIMIT ? OFFSET ?`
	args = append(args, limit, max(f.Offset, 0))

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAttempts(rows)
}

func scanAttempts(rows *sql.Rows) ([]Attempt, error) {
	out := make([]Attempt, 0)
	for rows.Next() {
		var a Attempt
		var ts int64
		var headers string
		if err := rows.Scan(&a.RequestID, &a.Attempt, &ts, &a.Route, &a.Node,
			&a.Provider, &a.GatewayKey, &a.Status, &a.Error, &a.ErrorClass,
			&a.DurationMS, &a.BytesIn, &a.BytesOut, &a.Upstream, &headers,
			&a.BodyPreview); err != nil {
			return nil, err
		}
		a.TS = time.UnixMilli(ts).UTC()
		if headers != "" && headers != "{}" {
			if err := json.Unmarshal([]byte(headers), &a.ResponseHeaders); err != nil {
				return nil, err
			}
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (f AttemptFilter) where() (string, []any) {
	var conds []string
	var args []any
	if !f.From.IsZero() {
		conds = append(conds, "ts >= ?")
		args = append(args, f.From.UnixMilli())
	}
	if !f.To.IsZero() {
		conds = append(conds, "ts < ?")
		args = append(args, f.To.UnixMilli())
	}
	if f.Route != "" {
		conds = append(conds, "route = ?")
		args = append(args, f.Route)
	}
	if f.Node != "" {
		conds = append(conds, "node = ?")
		args = append(args, f.Node)
	}
	if f.Provider != "" {
		conds = append(conds, "provider = ?")
		args = append(args, f.Provider)
	}
	if f.ErrorClass != "" {
		conds = append(conds, "error_class = ?")
		args = append(args, f.ErrorClass)
	}
	if f.Status > 0 {
		conds = append(conds, "status = ?")
		args = append(args, f.Status)
	}
	if len(conds) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(conds, " AND "), args
}
