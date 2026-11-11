// Package stats persists per-request scalars and hourly rollups.
package stats

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	_ "modernc.org/sqlite"

	"github.com/ginuerzh/cuttle/internal/capture"
	"github.com/ginuerzh/cuttle/internal/identity"
)

// Request is one recorded request. It deliberately has no header/body fields.
type Request struct {
	ID              string           `json:"requestId"`
	SID             string           `json:"sid"`
	TS              time.Time        `json:"ts"`
	Model           string           `json:"model"`
	ClientModel     string           `json:"clientModel,omitempty"`
	Provider        string           `json:"provider"`
	Route           string           `json:"route,omitempty"`
	Node            string           `json:"node,omitempty"`
	GatewayKey      string           `json:"gatewayKey,omitempty"`
	Protocol        string           `json:"protocol"`
	InboundProtocol string           `json:"inboundProtocol,omitempty"`
	URI             string           `json:"uri,omitempty"`
	Method          string           `json:"method,omitempty"`
	ClientIP        string           `json:"clientIp,omitempty"`
	UserAgent       string           `json:"userAgent,omitempty"`
	Upstream        string           `json:"upstream,omitempty"`
	ErrorStage      string           `json:"errorStage,omitempty"`
	ErrorDirection  string           `json:"errorDirection,omitempty"`
	SSEPhase        string           `json:"ssePhase,omitempty"`
	EventIndex      int              `json:"eventIndex,omitempty"`
	Effort          string           `json:"effort"`
	Status          int              `json:"status"`
	ClientStatus    int              `json:"clientStatus,omitempty"`
	Error           string           `json:"error,omitempty"`
	ErrorClass      string           `json:"errorClass,omitempty"`
	DurationMS      int64            `json:"durationMs"`
	TTFTMS          int64            `json:"ttftMs,omitempty"`
	InputTokens     int64            `json:"inputTokens"`
	OutputTokens    int64            `json:"outputTokens"`
	CachedTokens    int64            `json:"cachedTokens"`
	Stream          bool             `json:"stream"`
	SawDone         bool             `json:"sawDone,omitempty"`
	Truncated       bool             `json:"truncated,omitempty"`
	Retries         int              `json:"retries"`
	FinishReason    string           `json:"finishReason,omitempty"`
	BytesIn         uint64           `json:"bytesIn"`
	BytesOut        uint64           `json:"bytesOut"`
	HasCapture      bool             `json:"hasCapture,omitempty"`
	Capture         *capture.Capture `json:"capture,omitempty"`
}

// Filter selects requests. Provider, Route, Node and GatewayKey are entity ids:
// a row stores ids, never display names.
type Filter struct {
	From                time.Time
	To                  time.Time
	Provider            string
	Model               string
	ClientModel         string
	ClientModelPatterns []string
	// ClientModelExcludes holds globs that shadow ClientModelPatterns. Entrypoint
	// attribution follows declaration order, so a row belongs to the first
	// entrypoint whose glob matches; the globs of every earlier entrypoint are
	// excluded here so a catch-all lists only the rows no earlier one handled.
	ClientModelExcludes []string
	Route               string
	Node                string
	GatewayKey          string
	Status              int
	OnlyErrors          bool
	Captured            bool
	ErrorClass          string
	Limit               int
	Offset              int
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

// ProviderUsage aggregates one provider's hourly rollups for a time window.
type ProviderUsage struct {
	Provider     string `json:"provider,omitempty"`
	Requests     int64  `json:"requests"`
	Errors       int64  `json:"errors"`
	InputTokens  int64  `json:"inputTokens"`
	OutputTokens int64  `json:"outputTokens"`
	CachedTokens int64  `json:"cachedTokens"`
}

// KeyUsage aggregates one gateway key's hourly rollups for a time window.
type KeyUsage struct {
	// Key is the gateway key id recorded in key_rollups.gateway_key.
	Key          string `json:"key"`
	Requests     int64  `json:"requests"`
	Errors       int64  `json:"errors"`
	InputTokens  int64  `json:"inputTokens"`
	OutputTokens int64  `json:"outputTokens"`
	CachedTokens int64  `json:"cachedTokens"`
}

// Store is the SQLite-backed statistics store.
type Store struct {
	db            *sql.DB
	path          string
	captureRetain atomic.Int64

	// backupV1 copies the database before the identity rewrite. It is a field
	// so a test can force a backup failure on a host where permissions cannot.
	backupV1 func(context.Context) (string, error)

	migrationMu     sync.Mutex
	migrationBackup string
	migrationErr    error
}

// Open opens or creates the store. Use ":memory:" for tests. A database that
// already holds a requests table is left for MigrateIdentities to upgrade; a
// fresh database is created in the current schema version and never migrated.
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

	s := &Store{db: db, path: path}
	s.backupV1 = s.backupV1ToDisk
	fresh, err := s.isFresh()
	if err != nil {
		db.Close()
		return nil, err
	}
	if err := s.migrate(fresh); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// isFresh reports whether the database has no requests table yet. It runs
// before migrate creates anything, so a nil error with true means this Open is
// the one that builds the schema.
func (s *Store) isFresh() (bool, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'requests'`).Scan(&n)
	return n == 0, err
}

func (s *Store) migrate(fresh bool) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS requests (
			request_id TEXT PRIMARY KEY,
			seq INTEGER NOT NULL DEFAULT 0,
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
		`CREATE TABLE IF NOT EXISTS topology_positions (
			node_id TEXT PRIMARY KEY,
			x REAL NOT NULL,
			y REAL NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS request_captures (
			request_id TEXT PRIMARY KEY,
			request_original TEXT NOT NULL DEFAULT '',
			request_rewritten TEXT NOT NULL DEFAULT '',
			response_original TEXT NOT NULL DEFAULT '',
			response_rewritten TEXT NOT NULL DEFAULT '',
			sse_first_original TEXT NOT NULL DEFAULT '',
			sse_first_rewritten TEXT NOT NULL DEFAULT '',
			sse_last_original TEXT NOT NULL DEFAULT '',
			sse_last_rewritten TEXT NOT NULL DEFAULT '',
			request_sha256 TEXT NOT NULL DEFAULT '',
			response_sha256 TEXT NOT NULL DEFAULT '',
			request_headers TEXT NOT NULL DEFAULT '{}',
			response_headers TEXT NOT NULL DEFAULT '{}',
			truncated_fields TEXT NOT NULL DEFAULT '',
			sse_events INTEGER NOT NULL DEFAULT 0,
			reason TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS rollups (
			bucket TEXT NOT NULL,
			provider TEXT NOT NULL DEFAULT '',
			model TEXT NOT NULL DEFAULT '',
			requests INTEGER NOT NULL DEFAULT 0,
			errors INTEGER NOT NULL DEFAULT 0,
			input_tokens INTEGER NOT NULL DEFAULT 0,
			output_tokens INTEGER NOT NULL DEFAULT 0,
			cached_tokens INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (bucket, provider, model)
		)`,
		`CREATE TABLE IF NOT EXISTS key_rollups (
			bucket TEXT NOT NULL,
			gateway_key TEXT NOT NULL DEFAULT '',
			requests INTEGER NOT NULL DEFAULT 0,
			errors INTEGER NOT NULL DEFAULT 0,
			input_tokens INTEGER NOT NULL DEFAULT 0,
			output_tokens INTEGER NOT NULL DEFAULT 0,
			cached_tokens INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (bucket, gateway_key)
		)`,
		`CREATE TABLE IF NOT EXISTS request_seq (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			value INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS entity_labels (
			kind TEXT NOT NULL,
			id TEXT NOT NULL,
			name TEXT NOT NULL DEFAULT '',
			PRIMARY KEY (kind, id)
		)`,
	}
	for _, q := range stmts {
		if _, err := s.db.Exec(q); err != nil {
			return err
		}
	}
	if err := s.ensureColumns("requests", map[string]string{
		"route":            "TEXT NOT NULL DEFAULT ''",
		"client_model":     "TEXT NOT NULL DEFAULT ''",
		"node":             "TEXT NOT NULL DEFAULT ''",
		"inbound_protocol": "TEXT NOT NULL DEFAULT ''",
		"uri":              "TEXT NOT NULL DEFAULT ''",
		"method":           "TEXT NOT NULL DEFAULT ''",
		"client_ip":        "TEXT NOT NULL DEFAULT ''",
		"user_agent":       "TEXT NOT NULL DEFAULT ''",
		"upstream":         "TEXT NOT NULL DEFAULT ''",
		"client_status":    "INTEGER NOT NULL DEFAULT 0",
		"finish_reason":    "TEXT NOT NULL DEFAULT ''",
		"saw_done":         "INTEGER NOT NULL DEFAULT 0",
		"truncated":        "INTEGER NOT NULL DEFAULT 0",
		"error_stage":      "TEXT NOT NULL DEFAULT ''",
		"error_direction":  "TEXT NOT NULL DEFAULT ''",
		"error_class":      "TEXT NOT NULL DEFAULT ''",
		"sse_phase":        "TEXT NOT NULL DEFAULT ''",
		"event_index":      "INTEGER NOT NULL DEFAULT 0",
		"ttft_ms":          "INTEGER NOT NULL DEFAULT 0",
		"seq":              "INTEGER NOT NULL DEFAULT 0",
		"gateway_key":      "TEXT NOT NULL DEFAULT ''",
		"retries":          "INTEGER NOT NULL DEFAULT 0",
	}); err != nil {
		return err
	}
	if _, err := s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_requests_gateway_key_ts ON requests(gateway_key, ts)`); err != nil {
		return err
	}
	if err := s.ensureColumns("request_captures", map[string]string{
		"request_headers":  "TEXT NOT NULL DEFAULT '{}'",
		"response_headers": "TEXT NOT NULL DEFAULT '{}'",
		"truncated_fields": "TEXT NOT NULL DEFAULT ''",
	}); err != nil {
		return err
	}
	if err := s.ensureColumns("rollups", map[string]string{
		"cached_tokens": "INTEGER NOT NULL DEFAULT 0",
	}); err != nil {
		return err
	}

	// A fresh database is created in the current version: every column already
	// holds ids and entity_labels is empty, so there is nothing to rewrite.
	if fresh {
		if _, err := s.db.Exec(`INSERT OR IGNORE INTO request_seq(id, value) VALUES(1, 0)`); err != nil {
			return err
		}
		_, err := s.db.Exec(`PRAGMA user_version = ` + strconv.Itoa(statsVersion))
		return err
	}

	// v1: backfill the monotonic live cursor and initialize its counter once.
	var version int
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return err
	}
	if version < 1 {
		if _, err := s.db.Exec(`UPDATE requests SET seq = rowid WHERE seq = 0`); err != nil {
			return err
		}
		if _, err := s.db.Exec(`INSERT OR IGNORE INTO request_seq(id, value) SELECT 1, COALESCE(MAX(seq), 0) FROM requests`); err != nil {
			return err
		}
		if _, err := s.db.Exec(`PRAGMA user_version = 1`); err != nil {
			return err
		}
	}
	return nil
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

// statsVersion is the current statistics schema version. Version 2 keeps entity
// ids in every reference column and keeps display names in entity_labels.
const statsVersion = 2

// unroutedRef is the literal graph key recorded for rows without a route.
const unroutedRef = "unrouted"

// entityKinds are the kinds entity_labels keeps: the ones that appear in stats
// rows, topology positions and labels. Provider keys are identified only inside
// their provider and are never labelled.
var entityKinds = map[string]bool{
	string(identity.Provider):   true,
	string(identity.Node):       true,
	string(identity.Route):      true,
	string(identity.Entrypoint): true,
	string(identity.GatewayKey): true,
}

// positionKinds maps a topology position key prefix to its entity kind.
var positionKinds = map[string]identity.Kind{
	"provider:":   identity.Provider,
	"node:":       identity.Node,
	"route:":      identity.Route,
	"entrypoint:": identity.Entrypoint,
}

// MigrateIdentities rewrites the v1 name-keyed rows of an existing database to
// entity ids exactly once. lookup resolves a kind and a display name to the id
// the current config uses; a name the config does not know keeps a stable id
// derived from the name, so its history stays in one series. The database is
// copied to <path>.v1.bak-<unix> first, and a failed copy aborts the migration
// rather than upgrading without a backup.
//
// The outcome is recorded so the health endpoint can report a failure that the
// data plane already survived; see MigrationStatus.
func (s *Store) MigrateIdentities(ctx context.Context, lookup func(kind, name string) string) error {
	if s == nil || s.db == nil {
		return nil
	}
	backup, err := s.migrateIdentities(ctx, lookup)

	s.migrationMu.Lock()
	s.migrationBackup = backup
	s.migrationErr = err
	s.migrationMu.Unlock()
	return err
}

// MigrationStatus reports the outcome of the last identity migration: the v1
// backup path and the failure, if any. Both are empty when the migration
// succeeded or the database was already current.
func (s *Store) MigrationStatus() (backupPath string, err error) {
	if s == nil {
		return "", nil
	}
	s.migrationMu.Lock()
	defer s.migrationMu.Unlock()
	return s.migrationBackup, s.migrationErr
}

// MigrationError returns the identity migration failure as text for the health
// endpoint, or "" when the migration succeeded or was not needed.
func (s *Store) MigrationError() string {
	_, err := s.MigrationStatus()
	if err == nil {
		return ""
	}
	return err.Error()
}

func (s *Store) migrateIdentities(ctx context.Context, lookup func(kind, name string) string) (string, error) {
	version, err := s.userVersion(ctx)
	if err != nil {
		return "", err
	}
	if version >= statsVersion {
		return "", nil
	}
	backup := ""
	if s.backupV1 != nil {
		backup, err = s.backupV1(ctx)
		if err != nil {
			return "", err
		}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return backup, err
	}
	defer tx.Rollback()

	r := newIDResolver(lookup)
	for _, c := range []struct{ table, column, kind string }{
		{"requests", "provider", string(identity.Provider)},
		{"requests", "route", string(identity.Route)},
		{"requests", "node", string(identity.Node)},
		{"requests", "gateway_key", string(identity.GatewayKey)},
		{"probe_results", "provider", string(identity.Provider)},
	} {
		if err := rewriteColumn(ctx, tx, c.table, c.column, c.kind, r); err != nil {
			return backup, err
		}
	}
	if err := rewriteRollups(ctx, tx, r); err != nil {
		return backup, err
	}
	if err := rewriteKeyRollups(ctx, tx, r); err != nil {
		return backup, err
	}
	if err := rewritePositions(ctx, tx, r); err != nil {
		return backup, err
	}
	if err := r.seedLabels(ctx, tx); err != nil {
		return backup, err
	}
	if _, err := tx.ExecContext(ctx, `PRAGMA user_version = `+strconv.Itoa(statsVersion)); err != nil {
		return backup, err
	}
	return backup, tx.Commit()
}

// backupV1ToDisk copies the database before the rewrite. Statistics accumulated
// over months cannot be rebuilt, so a database that cannot be copied is left
// alone. The copy needs free space equal to the size of the database.
func (s *Store) backupV1ToDisk(ctx context.Context) (string, error) {
	if s.path == "" || s.path == ":memory:" {
		return "", nil
	}
	target := fmt.Sprintf("%s.v1.bak-%d", s.path, time.Now().Unix())
	if _, err := s.db.ExecContext(ctx, `VACUUM INTO ?`, target); err != nil {
		return "", fmt.Errorf("stats: back up %s to %s: %w", s.path, target, err)
	}
	return target, nil
}

func (s *Store) userVersion(ctx context.Context) (int, error) {
	var version int
	err := s.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version)
	return version, err
}

// idResolver maps a v1 display name to the id it now has and remembers every
// pair the migration rewrote, so entity_labels keeps the historical name.
type idResolver struct {
	lookup func(kind, name string) string
	ids    map[string]string
}

func newIDResolver(lookup func(kind, name string) string) *idResolver {
	return &idResolver{lookup: lookup, ids: make(map[string]string)}
}

// id returns the entity id for a v1 name and caches it, so one name maps to one
// id for the whole migration.
func (r *idResolver) id(kind, name string) string {
	key := kind + "\x00" + name
	if id, ok := r.ids[key]; ok {
		return id
	}
	// A v1 row can carry a padded name while the config holds the trimmed
	// label, so the lookup and the derivation both use the trimmed value. The
	// stored label keeps the text the row recorded.
	trimmed := strings.TrimSpace(name)
	id := ""
	if r.lookup != nil {
		id = r.lookup(kind, trimmed)
	}
	if id == "" {
		id = identity.Derive(identity.Kind(kind), trimmed)
	}
	r.ids[key] = id
	return id
}

// seedLabels stores the historical name of every rewritten reference. An
// existing row wins: a label already synced from a renamed config is more
// current than the name an old row happened to record.
func (r *idResolver) seedLabels(ctx context.Context, tx *sql.Tx) error {
	for key, id := range r.ids {
		kind, name, ok := strings.Cut(key, "\x00")
		if !ok || id == "" || !entityKinds[kind] {
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO entity_labels(kind, id, name) VALUES(?,?,?)
			ON CONFLICT(kind, id) DO NOTHING`, kind, id, name); err != nil {
			return err
		}
	}
	return nil
}

// migratableRef reports whether v is a v1 display name that still needs its id.
// A value that already satisfies identity.Shape is an id an id-aware gateway
// wrote before this migration; deriving from it would split its history. Empty
// values and the unrouted marker are not entity references.
func migratableRef(v string) bool {
	return v != "" && v != unroutedRef && !identity.Shape(v)
}

// distinctRefs returns the distinct v1 display names recorded in one column.
func distinctRefs(ctx context.Context, tx *sql.Tx, table, column string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT `+column+` FROM `+table+` WHERE `+column+` <> ''`)
	if err != nil {
		return nil, err
	}
	var refs []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			rows.Close()
			return nil, err
		}
		if migratableRef(v) {
			refs = append(refs, v)
		}
	}
	rows.Close()
	return refs, rows.Err()
}

// rewriteColumn rewrites every v1 name in one column to its entity id.
func rewriteColumn(ctx context.Context, tx *sql.Tx, table, column, kind string, r *idResolver) error {
	refs, err := distinctRefs(ctx, tx, table, column)
	if err != nil {
		return err
	}
	for _, ref := range refs {
		id := r.id(kind, ref)
		if id == ref {
			continue
		}
		if _, err := tx.ExecContext(ctx, `UPDATE `+table+` SET `+column+` = ? WHERE `+column+` = ?`, id, ref); err != nil {
			return err
		}
	}
	return nil
}

// rewriteRollups rewrites rollups.provider and sums a v1 series into the row an
// id-aware gateway already wrote for the same bucket and model, so neither
// history is lost to the shared primary key.
func rewriteRollups(ctx context.Context, tx *sql.Tx, r *idResolver) error {
	refs, err := distinctRefs(ctx, tx, "rollups", "provider")
	if err != nil {
		return err
	}
	for _, ref := range refs {
		id := r.id(string(identity.Provider), ref)
		if id == ref {
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO rollups
			(bucket, provider, model, requests, errors, input_tokens, output_tokens, cached_tokens)
			SELECT bucket, ?, model, requests, errors, input_tokens, output_tokens, cached_tokens
			FROM rollups WHERE provider = ?
			ON CONFLICT(bucket, provider, model) DO UPDATE SET
				requests = requests + excluded.requests,
				errors = errors + excluded.errors,
				input_tokens = input_tokens + excluded.input_tokens,
				output_tokens = output_tokens + excluded.output_tokens,
				cached_tokens = cached_tokens + excluded.cached_tokens`, id, ref); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM rollups WHERE provider = ?`, ref); err != nil {
			return err
		}
	}
	return nil
}

// rewriteKeyRollups rewrites key_rollups.gateway_key with the same merge.
func rewriteKeyRollups(ctx context.Context, tx *sql.Tx, r *idResolver) error {
	refs, err := distinctRefs(ctx, tx, "key_rollups", "gateway_key")
	if err != nil {
		return err
	}
	for _, ref := range refs {
		id := r.id(string(identity.GatewayKey), ref)
		if id == ref {
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO key_rollups
			(bucket, gateway_key, requests, errors, input_tokens, output_tokens, cached_tokens)
			SELECT bucket, ?, requests, errors, input_tokens, output_tokens, cached_tokens
			FROM key_rollups WHERE gateway_key = ?
			ON CONFLICT(bucket, gateway_key) DO UPDATE SET
				requests = requests + excluded.requests,
				errors = errors + excluded.errors,
				input_tokens = input_tokens + excluded.input_tokens,
				output_tokens = output_tokens + excluded.output_tokens,
				cached_tokens = cached_tokens + excluded.cached_tokens`, id, ref); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM key_rollups WHERE gateway_key = ?`, ref); err != nil {
			return err
		}
	}
	return nil
}

// rewritePositions rewrites the entity id carried by a topology position key.
// A position already stored under the id wins, so a saved layout is never
// replaced by a stale one.
func rewritePositions(ctx context.Context, tx *sql.Tx, r *idResolver) error {
	rows, err := tx.QueryContext(ctx, `SELECT node_id, x, y FROM topology_positions`)
	if err != nil {
		return err
	}
	type move struct {
		from, to string
		x, y     float64
	}
	var moves []move
	for rows.Next() {
		var id string
		var x, y float64
		if err := rows.Scan(&id, &x, &y); err != nil {
			rows.Close()
			return err
		}
		prefix, name, ok := strings.Cut(id, ":")
		if !ok {
			continue
		}
		kind, ok := positionKinds[prefix+":"]
		if !ok || !migratableRef(name) {
			continue
		}
		newID := r.id(string(kind), name)
		if newID == name {
			continue
		}
		moves = append(moves, move{from: id, to: prefix + ":" + newID, x: x, y: y})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, m := range moves {
		if _, err := tx.ExecContext(ctx, `DELETE FROM topology_positions WHERE node_id = ?`, m.from); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO topology_positions(node_id, x, y) VALUES(?,?,?)
			ON CONFLICT(node_id) DO NOTHING`, m.to, m.x, m.y); err != nil {
			return err
		}
	}
	return nil
}

// SyncLabels upserts the current display names of the entities the config still
// knows. Rows are never deleted: a label is the last known name of an entity
// and outlives it.
func (s *Store) SyncLabels(ctx context.Context, labels map[string]map[string]string) error {
	if s == nil || s.db == nil || len(labels) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for kind, byID := range labels {
		if !entityKinds[kind] {
			continue
		}
		for id, name := range byID {
			if id == "" || name == "" {
				continue
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO entity_labels(kind, id, name) VALUES(?,?,?)
				ON CONFLICT(kind, id) DO UPDATE SET name = excluded.name`, kind, id, name); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// Labels returns the last known display name of every labelled entity, keyed by
// kind and id.
func (s *Store) Labels(ctx context.Context) (map[string]map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT kind, id, name FROM entity_labels`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string]map[string]string)
	for rows.Next() {
		var kind, id, name string
		if err := rows.Scan(&kind, &id, &name); err != nil {
			return nil, err
		}
		if out[kind] == nil {
			out[kind] = make(map[string]string)
		}
		out[kind][id] = name
	}
	return out, rows.Err()
}

// Position is one topology node position in graph coordinates.
type Position struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// LoadTopologyPositions returns the shared topology position map.
func (s *Store) LoadTopologyPositions(ctx context.Context) (map[string]Position, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT node_id, x, y FROM topology_positions`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string]Position)
	for rows.Next() {
		var id string
		var p Position
		if err := rows.Scan(&id, &p.X, &p.Y); err != nil {
			return nil, err
		}
		out[id] = p
	}
	return out, rows.Err()
}

// SaveTopologyPositions replaces the whole shared topology position map.
func (s *Store) SaveTopologyPositions(ctx context.Context, positions map[string]Position) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM topology_positions`); err != nil {
		return err
	}
	if len(positions) > 0 {
		stmt, err := tx.PrepareContext(ctx,
			`INSERT INTO topology_positions(node_id, x, y) VALUES(?, ?, ?)`)
		if err != nil {
			return err
		}
		defer stmt.Close()
		for id, p := range positions {
			if _, err := stmt.ExecContext(ctx, id, p.X, p.Y); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
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

	var seq int64
	if _, err := tx.ExecContext(ctx, `UPDATE request_seq SET value = value + 1 WHERE id = 1`); err != nil {
		return err
	}
	if err := tx.QueryRowContext(ctx, `SELECT value FROM request_seq WHERE id = 1`).Scan(&seq); err != nil {
		return err
	}

	_, err = tx.ExecContext(ctx, `INSERT OR REPLACE INTO requests
		(request_id, seq, sid, ts, model, client_model, provider, route, node, gateway_key,
		 protocol, inbound_protocol, error_stage, error_direction, sse_phase, event_index,
		 effort, status, error, error_class, duration_ms, ttft_ms,
		 input_tokens, output_tokens, cached_tokens, stream, bytes_in, bytes_out,
		 uri, method, client_ip, user_agent, upstream, client_status,
		 finish_reason, saw_done, truncated, retries)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		r.ID, seq, r.SID, r.TS.UnixMilli(), r.Model, r.ClientModel, r.Provider, r.Route, r.Node, r.GatewayKey,
		r.Protocol, r.InboundProtocol, r.ErrorStage, r.ErrorDirection, r.SSEPhase, r.EventIndex,
		r.Effort, r.Status, r.Error, r.ErrorClass, r.DurationMS, r.TTFTMS,
		r.InputTokens, r.OutputTokens, r.CachedTokens, boolInt(r.Stream), int64(r.BytesIn), int64(r.BytesOut),
		r.URI, r.Method, r.ClientIP, r.UserAgent, r.Upstream, r.ClientStatus,
		r.FinishReason, boolInt(r.SawDone), boolInt(r.Truncated), r.Retries)
	if err != nil {
		return err
	}

	bucket := r.TS.UTC().Truncate(time.Hour).Format(time.RFC3339)
	failed := 0
	if r.Status >= 400 || r.Error != "" {
		failed = 1
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO rollups
		(bucket, provider, model, requests, errors, input_tokens, output_tokens, cached_tokens)
		VALUES (?,?,?,1,?,?,?,?)
		ON CONFLICT(bucket, provider, model) DO UPDATE SET
			requests = requests + 1,
			errors = errors + excluded.errors,
			input_tokens = input_tokens + excluded.input_tokens,
			output_tokens = output_tokens + excluded.output_tokens,
			cached_tokens = cached_tokens + excluded.cached_tokens`,
		bucket, r.Provider, r.Model, failed, r.InputTokens, r.OutputTokens, r.CachedTokens)
	if err != nil {
		return err
	}
	if r.GatewayKey != "" {
		_, err = tx.ExecContext(ctx, `INSERT INTO key_rollups
			(bucket, gateway_key, requests, errors, input_tokens, output_tokens, cached_tokens)
			VALUES (?,?,1,?,?,?,?)
			ON CONFLICT(bucket, gateway_key) DO UPDATE SET
				requests = requests + 1,
				errors = errors + excluded.errors,
				input_tokens = input_tokens + excluded.input_tokens,
				output_tokens = output_tokens + excluded.output_tokens,
				cached_tokens = cached_tokens + excluded.cached_tokens`,
			bucket, r.GatewayKey, failed, r.InputTokens, r.OutputTokens, r.CachedTokens)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ProviderUsage aggregates rollups by provider for a time window.
func (s *Store) ProviderUsage(ctx context.Context, from, to time.Time) ([]ProviderUsage, error) {
	fromBucket := from.UTC().Truncate(time.Hour).Format(time.RFC3339)
	toBucket := to.UTC().Format(time.RFC3339)
	rows, err := s.db.QueryContext(ctx, `SELECT provider,
		COALESCE(SUM(requests),0), COALESCE(SUM(errors),0),
		COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0),
		COALESCE(SUM(cached_tokens),0)
		FROM rollups WHERE bucket >= ? AND bucket < ? GROUP BY provider ORDER BY provider`,
		fromBucket, toBucket)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]ProviderUsage, 0)
	for rows.Next() {
		var r ProviderUsage
		if err := rows.Scan(&r.Provider, &r.Requests, &r.Errors,
			&r.InputTokens, &r.OutputTokens, &r.CachedTokens); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// KeyUsage aggregates key rollups by gateway key for a time window.
func (s *Store) KeyUsage(ctx context.Context, from, to time.Time) ([]KeyUsage, error) {
	fromBucket := from.UTC().Truncate(time.Hour).Format(time.RFC3339)
	toBucket := to.UTC().Format(time.RFC3339)
	rows, err := s.db.QueryContext(ctx, `SELECT gateway_key,
		COALESCE(SUM(requests),0), COALESCE(SUM(errors),0),
		COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0),
		COALESCE(SUM(cached_tokens),0)
		FROM key_rollups WHERE bucket >= ? AND bucket < ? GROUP BY gateway_key ORDER BY gateway_key`,
		fromBucket, toBucket)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]KeyUsage, 0)
	for rows.Next() {
		var u KeyUsage
		if err := rows.Scan(&u.Key, &u.Requests, &u.Errors,
			&u.InputTokens, &u.OutputTokens, &u.CachedTokens); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// Query returns requests in reverse time order.
func (s *Store) Query(ctx context.Context, f Filter) ([]Request, error) {
	where, args := f.where()
	limit := f.Limit
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	q := `SELECT request_id, sid, ts, model, client_model, provider, route, node, gateway_key,
		protocol, inbound_protocol, error_stage, error_direction, sse_phase, event_index,
		effort, status, error, error_class,
		duration_ms, ttft_ms, input_tokens, output_tokens, cached_tokens, stream, bytes_in, bytes_out,
		uri, method, client_ip, user_agent, upstream, client_status,
		finish_reason, saw_done, truncated, retries,
		EXISTS(SELECT 1 FROM request_captures c WHERE c.request_id = requests.request_id)
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
	q := `SELECT request_id, sid, ts, model, client_model, provider, route, node, gateway_key,
		protocol, inbound_protocol, error_stage, error_direction, sse_phase, event_index,
		effort, status, error, error_class,
		duration_ms, ttft_ms, input_tokens, output_tokens, cached_tokens, stream, bytes_in, bytes_out,
		uri, method, client_ip, user_agent, upstream, client_status,
		finish_reason, saw_done, truncated, retries,
		EXISTS(SELECT 1 FROM request_captures c WHERE c.request_id = requests.request_id)
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
	row := &list[0]
	c, err := s.GetCapture(ctx, id)
	if err != nil {
		return nil, err
	}
	row.Capture = c
	row.HasCapture = c != nil
	return row, nil
}

// PutCapture stores or replaces the bounded capture for one request.
func (s *Store) PutCapture(ctx context.Context, requestID string, c *capture.Capture) error {
	if requestID == "" || c == nil {
		return nil
	}
	created := c.CreatedAt
	if created.IsZero() {
		created = time.Now()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO request_captures
		(request_id, request_original, request_rewritten, response_original, response_rewritten,
		 sse_first_original, sse_first_rewritten, sse_last_original, sse_last_rewritten,
		 request_sha256, response_sha256, request_headers, response_headers, truncated_fields,
		 sse_events, reason, created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(request_id) DO UPDATE SET
			request_original=excluded.request_original,
			request_rewritten=excluded.request_rewritten,
			response_original=excluded.response_original,
			response_rewritten=excluded.response_rewritten,
			sse_first_original=excluded.sse_first_original,
			sse_first_rewritten=excluded.sse_first_rewritten,
			sse_last_original=excluded.sse_last_original,
			sse_last_rewritten=excluded.sse_last_rewritten,
			request_sha256=excluded.request_sha256,
			response_sha256=excluded.response_sha256,
			request_headers=excluded.request_headers,
			response_headers=excluded.response_headers,
			truncated_fields=excluded.truncated_fields,
			sse_events=excluded.sse_events,
			reason=excluded.reason,
			created_at=excluded.created_at`,
		requestID, c.RequestOriginal, c.RequestRewritten, c.ResponseOriginal, c.ResponseRewritten,
		c.SSEFirstOriginal, c.SSEFirstRewritten, c.SSELastOriginal, c.SSELastRewritten,
		c.RequestSHA256, c.ResponseSHA256, marshalHeaders(c.RequestHeaders), marshalHeaders(c.ResponseHeaders),
		marshalTruncated(c.TruncatedFields),
		c.SSIEvents, c.Reason, created.UnixMilli())
	return err
}

func marshalTruncated(fields []string) string {
	if len(fields) == 0 {
		return ""
	}
	b, err := json.Marshal(fields)
	if err != nil {
		return ""
	}
	return string(b)
}

func marshalHeaders(h map[string]string) string {
	if len(h) == 0 {
		return "{}"
	}
	b, err := json.Marshal(h)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// GetCapture returns the capture for one request, or nil when absent.
func (s *Store) GetCapture(ctx context.Context, requestID string) (*capture.Capture, error) {
	var c capture.Capture
	var created int64
	var reqHeaders, respHeaders, truncatedFields string
	err := s.db.QueryRowContext(ctx, `SELECT request_original, request_rewritten,
		response_original, response_rewritten, sse_first_original, sse_first_rewritten,
		sse_last_original, sse_last_rewritten, request_sha256, response_sha256,
		request_headers, response_headers, truncated_fields, sse_events, reason, created_at
		FROM request_captures WHERE request_id = ?`, requestID).Scan(
		&c.RequestOriginal, &c.RequestRewritten, &c.ResponseOriginal, &c.ResponseRewritten,
		&c.SSEFirstOriginal, &c.SSEFirstRewritten, &c.SSELastOriginal, &c.SSELastRewritten,
		&c.RequestSHA256, &c.ResponseSHA256, &reqHeaders, &respHeaders,
		&truncatedFields, &c.SSIEvents, &c.Reason, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if reqHeaders != "" {
		if err := json.Unmarshal([]byte(reqHeaders), &c.RequestHeaders); err != nil {
			return nil, fmt.Errorf("decode request headers: %w", err)
		}
	}
	if respHeaders != "" {
		if err := json.Unmarshal([]byte(respHeaders), &c.ResponseHeaders); err != nil {
			return nil, fmt.Errorf("decode response headers: %w", err)
		}
	}
	if truncatedFields != "" {
		if err := json.Unmarshal([]byte(truncatedFields), &c.TruncatedFields); err != nil {
			return nil, fmt.Errorf("decode truncated fields: %w", err)
		}
	}
	c.CreatedAt = time.UnixMilli(created).UTC()
	return &c, nil
}

func scanRequests(rows *sql.Rows) ([]Request, error) {
	var out []Request
	for rows.Next() {
		var r Request
		var ts int64
		var stream int
		var sawDone, truncated int
		var bytesIn, bytesOut int64
		var hasCapture int
		if err := rows.Scan(&r.ID, &r.SID, &ts, &r.Model, &r.ClientModel,
			&r.Provider, &r.Route, &r.Node, &r.GatewayKey, &r.Protocol,
			&r.InboundProtocol, &r.ErrorStage, &r.ErrorDirection, &r.SSEPhase, &r.EventIndex,
			&r.Effort, &r.Status, &r.Error, &r.ErrorClass, &r.DurationMS, &r.TTFTMS, &r.InputTokens, &r.OutputTokens, &r.CachedTokens,
			&stream, &bytesIn, &bytesOut,
			&r.URI, &r.Method, &r.ClientIP, &r.UserAgent, &r.Upstream, &r.ClientStatus,
			&r.FinishReason, &sawDone, &truncated, &r.Retries, &hasCapture); err != nil {
			return nil, err
		}
		r.TS = time.UnixMilli(ts).UTC()
		r.Stream = stream != 0
		r.SawDone = sawDone != 0
		r.Truncated = truncated != 0
		r.BytesIn = uint64(bytesIn)
		r.BytesOut = uint64(bytesOut)
		r.HasCapture = hasCapture != 0
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
func (s *Store) Cleanup(ctx context.Context, requestsBefore, rollupsBefore, capturesBefore time.Time) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM requests WHERE ts < ?`,
		requestsBefore.UnixMilli()); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM request_captures WHERE created_at < ?`,
		capturesBefore.UnixMilli()); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM rollups WHERE bucket < ?`,
		rollupsBefore.UTC().Format(time.RFC3339)); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM key_rollups WHERE bucket < ?`,
		rollupsBefore.UTC().Format(time.RFC3339))
	return err
}

// SetCaptureRetain updates the retention the cleanup loop applies to captures.
// A non-positive value restores the 24h default. Reloads call this so a
// capture.retain change takes effect without a restart.
func (s *Store) SetCaptureRetain(d time.Duration) {
	if d <= 0 {
		d = 24 * time.Hour
	}
	s.captureRetain.Store(int64(d))
}

// CaptureRetain reports the retention currently applied by the cleanup loop.
func (s *Store) CaptureRetain() time.Duration {
	return time.Duration(s.captureRetain.Load())
}

func (s *Store) captureCutoff(now time.Time, fallback time.Duration) time.Time {
	if d := time.Duration(s.captureRetain.Load()); d > 0 {
		return now.Add(-d)
	}
	return now.Add(-fallback)
}

// StartCleanup runs Cleanup periodically until ctx is done.
func (s *Store) StartCleanup(ctx context.Context, interval time.Duration, retainRequests, retainRollups, retainCaptures time.Duration) {
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
				_ = s.Cleanup(ctx, now.Add(-retainRequests), now.Add(-retainRollups),
					s.captureCutoff(now, retainCaptures))
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
	if len(f.ClientModelPatterns) > 0 {
		likes := make([]string, 0, len(f.ClientModelPatterns))
		for _, pattern := range f.ClientModelPatterns {
			likes = append(likes, "client_model LIKE ? ESCAPE '\\'")
			args = append(args, globToLike(pattern))
		}
		conds = append(conds, "("+strings.Join(likes, " OR ")+")")
	}
	if len(f.ClientModelExcludes) > 0 {
		likes := make([]string, 0, len(f.ClientModelExcludes))
		for _, pattern := range f.ClientModelExcludes {
			likes = append(likes, "client_model LIKE ? ESCAPE '\\'")
			args = append(args, globToLike(pattern))
		}
		conds = append(conds, "NOT ("+strings.Join(likes, " OR ")+")")
	}
	if f.Route != "" {
		conds = append(conds, "route = ?")
		args = append(args, f.Route)
	}
	if f.Node != "" {
		conds = append(conds, "node = ?")
		args = append(args, f.Node)
	}
	if f.GatewayKey != "" {
		conds = append(conds, "gateway_key = ?")
		args = append(args, f.GatewayKey)
	}
	if f.OnlyErrors {
		conds = append(conds, "(status >= 400 OR error != '')")
	}
	if f.Captured {
		conds = append(conds, "EXISTS(SELECT 1 FROM request_captures c WHERE c.request_id = requests.request_id)")
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

// globToLike converts one entrypoint model glob into a SQL LIKE pattern.
// Entrypoint globs only use '*', so escape the LIKE metacharacters.
func globToLike(pattern string) string {
	var b strings.Builder
	for _, r := range pattern {
		switch r {
		case '*':
			b.WriteByte('%')
		case '%', '_', '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
