// Package stats persists per-request scalars and hourly rollups.
package stats

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Request is one recorded request. It deliberately has no header/body fields.
type Request struct {
	ID           string    `json:"requestId"`
	SID          string    `json:"sid"`
	TS           time.Time `json:"ts"`
	Model        string    `json:"model"`
	ClientModel  string    `json:"clientModel,omitempty"`
	Provider     string    `json:"provider"`
	Route        string    `json:"route,omitempty"`
	Node         string    `json:"node,omitempty"`
	Protocol     string    `json:"protocol"`
	Effort       string    `json:"effort"`
	Status       int       `json:"status"`
	Error        string    `json:"error,omitempty"`
	DurationMS   int64     `json:"durationMs"`
	InputTokens  int64     `json:"inputTokens"`
	OutputTokens int64     `json:"outputTokens"`
	CachedTokens int64     `json:"cachedTokens"`
	Stream       bool      `json:"stream"`
	BytesIn      uint64    `json:"bytesIn"`
	BytesOut     uint64    `json:"bytesOut"`
}

// Filter selects requests.
type Filter struct {
	From        time.Time
	To          time.Time
	Provider    string
	Model       string
	ClientModel string
	Route       string
	Status      int
	OnlyErrors  bool
	Limit       int
	Offset      int
}

// Group is an aggregated bucket.
type Group struct {
	Key          string `json:"key"`
	Requests     int64  `json:"requests"`
	Errors       int64  `json:"errors"`
	InputTokens  int64  `json:"inputTokens"`
	OutputTokens int64  `json:"outputTokens"`
}

// Summary aggregates a time window.
type Summary struct {
	Requests     int64            `json:"requests"`
	Errors       int64            `json:"errors"`
	InputTokens  int64            `json:"inputTokens"`
	OutputTokens int64            `json:"outputTokens"`
	P50MS        int64            `json:"p50Ms"`
	P95MS        int64            `json:"p95Ms"`
	ByProvider   []Group          `json:"byProvider"`
	ByModel      []Group          `json:"byModel"`
	ByEffort     map[string]int64 `json:"byEffort"`
}

// Store is the SQLite-backed statistics store.
type Store struct {
	db *sql.DB
}

// Open opens or creates the store. Use ":memory:" for tests.
func Open(path string) (*Store, error) {
	dsn := path
	if path != ":memory:" && path != "" {
		dsn = "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}

	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS requests (
			request_id TEXT PRIMARY KEY,
			sid TEXT NOT NULL DEFAULT '',
			ts INTEGER NOT NULL,
			model TEXT NOT NULL DEFAULT '',
			provider TEXT NOT NULL DEFAULT '',
			node TEXT NOT NULL DEFAULT '',
			protocol TEXT NOT NULL DEFAULT '',
			effort TEXT NOT NULL DEFAULT '',
			status INTEGER NOT NULL DEFAULT 0,
			error TEXT NOT NULL DEFAULT '',
			duration_ms INTEGER NOT NULL DEFAULT 0,
			input_tokens INTEGER NOT NULL DEFAULT 0,
			output_tokens INTEGER NOT NULL DEFAULT 0,
			cached_tokens INTEGER NOT NULL DEFAULT 0,
			stream INTEGER NOT NULL DEFAULT 0,
			bytes_in INTEGER NOT NULL DEFAULT 0,
			bytes_out INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE INDEX IF NOT EXISTS idx_requests_ts ON requests(ts)`,
		`CREATE INDEX IF NOT EXISTS idx_requests_provider_ts ON requests(provider, ts)`,
		`CREATE TABLE IF NOT EXISTS probe_results (
			ts INTEGER NOT NULL,
			provider TEXT NOT NULL DEFAULT '',
			ok INTEGER NOT NULL DEFAULT 0,
			status INTEGER NOT NULL DEFAULT 0,
			latency_ms INTEGER NOT NULL DEFAULT 0,
			err TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS sessions (
			token_hash TEXT PRIMARY KEY,
			expires INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS rollups (
			bucket TEXT NOT NULL,
			provider TEXT NOT NULL DEFAULT '',
			model TEXT NOT NULL DEFAULT '',
			requests INTEGER NOT NULL DEFAULT 0,
			errors INTEGER NOT NULL DEFAULT 0,
			input_tokens INTEGER NOT NULL DEFAULT 0,
			output_tokens INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (bucket, provider, model)
		)`,
	}
	for _, q := range stmts {
		if _, err := s.db.Exec(q); err != nil {
			return err
		}
	}
	return s.ensureColumns("requests", map[string]string{
		"route":        "TEXT NOT NULL DEFAULT ''",
		"client_model": "TEXT NOT NULL DEFAULT ''",
		"node":         "TEXT NOT NULL DEFAULT ''",
	})
}

// ensureColumns adds missing columns to an existing table (SQLite has no
// IF NOT EXISTS for ALTER TABLE).
func (s *Store) ensureColumns(table string, cols map[string]string) error {
	rows, err := s.db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return err
	}
	existing := make(map[string]bool)
	for rows.Next() {
		var cid, notNull, pk int
		var name, typ string
		var dflt any
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk); err != nil {
			rows.Close()
			return err
		}
		existing[name] = true
	}
	rows.Close()

	for name, decl := range cols {
		if existing[name] {
			continue
		}
		if _, err := s.db.Exec(`ALTER TABLE ` + table + ` ADD COLUMN ` + name + ` ` + decl); err != nil {
			return err
		}
	}
	return nil
}

// PutSession stores (or refreshes) a persistent session.
func (s *Store) PutSession(ctx context.Context, tokenHash string, expires time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO sessions(token_hash, expires) VALUES(?, ?)
		 ON CONFLICT(token_hash) DO UPDATE SET expires=excluded.expires`,
		tokenHash, expires.Unix())
	return err
}

// DeleteSession removes one persistent session.
func (s *Store) DeleteSession(ctx context.Context, tokenHash string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, tokenHash)
	return err
}

// DeleteAllSessions removes every persistent session (used on password change).
func (s *Store) DeleteAllSessions(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions`)
	return err
}

// LoadSessions returns non-expired persistent sessions and prunes the rest.
func (s *Store) LoadSessions(ctx context.Context, now time.Time) (map[string]time.Time, error) {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires <= ?`, now.Unix()); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT token_hash, expires FROM sessions`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string]time.Time)
	for rows.Next() {
		var hash string
		var exp int64
		if err := rows.Scan(&hash, &exp); err != nil {
			return nil, err
		}
		out[hash] = time.Unix(exp, 0)
	}
	return out, rows.Err()
}

// Close closes the database.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// Insert writes one request and updates its hourly rollup.
func (s *Store) Insert(ctx context.Context, r Request) error {
	if r.TS.IsZero() {
		r.TS = time.Now()
	}
	if r.ID == "" {
		r.ID = r.SID
	}
	if r.ID == "" {
		r.ID = fmt.Sprintf("%d", time.Now().UnixNano())
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx, `INSERT OR REPLACE INTO requests
		(request_id, sid, ts, model, client_model, provider, route, node,
		 protocol, effort, status, error, duration_ms,
		 input_tokens, output_tokens, cached_tokens, stream, bytes_in, bytes_out)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		r.ID, r.SID, r.TS.UnixMilli(), r.Model, r.ClientModel, r.Provider, r.Route, r.Node,
		r.Protocol, r.Effort, r.Status, r.Error, r.DurationMS,
		r.InputTokens, r.OutputTokens, r.CachedTokens, boolInt(r.Stream), int64(r.BytesIn), int64(r.BytesOut))
	if err != nil {
		return err
	}

	bucket := r.TS.UTC().Truncate(time.Hour).Format(time.RFC3339)
	failed := 0
	if r.Status >= 400 || r.Error != "" {
		failed = 1
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO rollups
		(bucket, provider, model, requests, errors, input_tokens, output_tokens)
		VALUES (?,?,?,1,?,?,?)
		ON CONFLICT(bucket, provider, model) DO UPDATE SET
			requests = requests + 1,
			errors = errors + excluded.errors,
			input_tokens = input_tokens + excluded.input_tokens,
			output_tokens = output_tokens + excluded.output_tokens`,
		bucket, r.Provider, r.Model, failed, r.InputTokens, r.OutputTokens)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// Query returns requests in reverse time order.
func (s *Store) Query(ctx context.Context, f Filter) ([]Request, error) {
	where, args := f.where()
	limit := f.Limit
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	q := `SELECT request_id, sid, ts, model, client_model, provider, route, node,
		protocol, effort, status, error,
		duration_ms, input_tokens, output_tokens, cached_tokens, stream, bytes_in, bytes_out
		FROM requests` + where + ` ORDER BY ts DESC LIMIT ? OFFSET ?`
	args = append(args, limit, max(f.Offset, 0))

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRequests(rows)
}

// Get returns one request by id.
func (s *Store) Get(ctx context.Context, id string) (*Request, error) {
	q := `SELECT request_id, sid, ts, model, client_model, provider, route, node,
		protocol, effort, status, error,
		duration_ms, input_tokens, output_tokens, cached_tokens, stream, bytes_in, bytes_out
		FROM requests WHERE request_id = ?`
	rows, err := s.db.QueryContext(ctx, q, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list, err := scanRequests(rows)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, nil
	}
	return &list[0], nil
}

func scanRequests(rows *sql.Rows) ([]Request, error) {
	var out []Request
	for rows.Next() {
		var r Request
		var ts int64
		var stream int
		var bytesIn, bytesOut int64
		if err := rows.Scan(&r.ID, &r.SID, &ts, &r.Model, &r.ClientModel,
			&r.Provider, &r.Route, &r.Node, &r.Protocol, &r.Effort,
			&r.Status, &r.Error, &r.DurationMS, &r.InputTokens, &r.OutputTokens, &r.CachedTokens,
			&stream, &bytesIn, &bytesOut); err != nil {
			return nil, err
		}
		r.TS = time.UnixMilli(ts).UTC()
		r.Stream = stream != 0
		r.BytesIn = uint64(bytesIn)
		r.BytesOut = uint64(bytesOut)
		out = append(out, r)
	}
	return out, rows.Err()
}

// Summary aggregates a window. Totals and groups come from rollups; percentiles
// come from the request detail.
func (s *Store) Summary(ctx context.Context, from, to time.Time) (Summary, error) {
	sum := Summary{ByEffort: map[string]int64{}}
	fromBucket := from.UTC().Truncate(time.Hour).Format(time.RFC3339)
	toBucket := to.UTC().Format(time.RFC3339)

	if err := s.db.QueryRowContext(ctx, `SELECT
		COALESCE(SUM(requests),0), COALESCE(SUM(errors),0),
		COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0)
		FROM rollups WHERE bucket >= ? AND bucket < ?`,
		fromBucket, toBucket).Scan(&sum.Requests, &sum.Errors, &sum.InputTokens, &sum.OutputTokens); err != nil {
		return sum, err
	}

	for _, g := range []struct {
		col string
		dst *[]Group
	}{{"provider", &sum.ByProvider}, {"model", &sum.ByModel}} {
		rows, err := s.db.QueryContext(ctx, `SELECT `+g.col+`,
			COALESCE(SUM(requests),0), COALESCE(SUM(errors),0),
			COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0)
			FROM rollups WHERE bucket >= ? AND bucket < ? GROUP BY `+g.col+` ORDER BY 2 DESC`,
			fromBucket, toBucket)
		if err != nil {
			return sum, err
		}
		for rows.Next() {
			var v Group
			if err := rows.Scan(&v.Key, &v.Requests, &v.Errors, &v.InputTokens, &v.OutputTokens); err != nil {
				rows.Close()
				return sum, err
			}
			*g.dst = append(*g.dst, v)
		}
		rows.Close()
	}

	rows, err := s.db.QueryContext(ctx, `SELECT effort, COUNT(*) FROM requests
		WHERE ts >= ? AND ts < ? GROUP BY effort`, from.UnixMilli(), to.UnixMilli())
	if err != nil {
		return sum, err
	}
	for rows.Next() {
		var k string
		var n int64
		if err := rows.Scan(&k, &n); err != nil {
			rows.Close()
			return sum, err
		}
		sum.ByEffort[k] = n
	}
	rows.Close()

	var n int64
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM requests WHERE ts >= ? AND ts < ?`,
		from.UnixMilli(), to.UnixMilli()).Scan(&n); err != nil {
		return sum, err
	}
	sum.P50MS = s.percentile(ctx, from, to, n, 0.50)
	sum.P95MS = s.percentile(ctx, from, to, n, 0.95)
	return sum, nil
}

func (s *Store) percentile(ctx context.Context, from, to time.Time, n int64, p float64) int64 {
	if n == 0 {
		return 0
	}
	offset := int64(p * float64(n))
	if offset >= n {
		offset = n - 1
	}
	var v int64
	if err := s.db.QueryRowContext(ctx, `SELECT duration_ms FROM requests
		WHERE ts >= ? AND ts < ? ORDER BY duration_ms LIMIT 1 OFFSET ?`,
		from.UnixMilli(), to.UnixMilli(), offset).Scan(&v); err != nil {
		return 0
	}
	return v
}

// Cleanup deletes detail rows older than requestsBefore and rollups older than
// rollupsBefore.
func (s *Store) Cleanup(ctx context.Context, requestsBefore, rollupsBefore time.Time) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM requests WHERE ts < ?`,
		requestsBefore.UnixMilli()); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM rollups WHERE bucket < ?`,
		rollupsBefore.UTC().Format(time.RFC3339))
	return err
}

// StartCleanup runs Cleanup periodically until ctx is done.
func (s *Store) StartCleanup(ctx context.Context, interval time.Duration, retainRequests, retainRollups time.Duration) {
	if interval <= 0 {
		interval = time.Hour
	}
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-t.C:
				_ = s.Cleanup(ctx, now.Add(-retainRequests), now.Add(-retainRollups))
			}
		}
	}()
}

func (f Filter) where() (string, []any) {
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
	if f.Provider != "" {
		conds = append(conds, "provider = ?")
		args = append(args, f.Provider)
	}
	if f.Model != "" {
		conds = append(conds, "model = ?")
		args = append(args, f.Model)
	}
	if f.ClientModel != "" {
		conds = append(conds, "client_model LIKE ?")
		args = append(args, "%"+f.ClientModel+"%")
	}
	if f.Route != "" {
		conds = append(conds, "route = ?")
		args = append(args, f.Route)
	}
	if f.OnlyErrors {
		conds = append(conds, "(status >= 400 OR error != '')")
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

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
