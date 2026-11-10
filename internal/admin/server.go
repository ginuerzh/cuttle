// Package admin exposes the cuttle management HTTP API.
package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/go-gost/core/logger"
	xlogger "github.com/go-gost/x/logger"

	"github.com/ginuerzh/cuttle/internal/authfail"
	"github.com/ginuerzh/cuttle/internal/capture"
	"github.com/ginuerzh/cuttle/internal/config"
	"github.com/ginuerzh/cuttle/internal/health"
	"github.com/ginuerzh/cuttle/internal/stats"
	"github.com/ginuerzh/cuttle/internal/version"
)

// Reloader applies a new configuration to the data plane.
type Reloader interface {
	Reload(*config.Config) error
}

type probeResult struct {
	OK        bool   `json:"ok"`
	Status    int    `json:"status,omitempty"`
	LatencyMS int64  `json:"latencyMs"`
	Error     string `json:"error,omitempty"`
	CheckedAt string `json:"checkedAt,omitempty"`
}

// Server is the management API handler.
type Server struct {
	cfgPath      string
	reloader     Reloader
	log          logger.Logger
	passwordHash string
	ttl          time.Duration
	rememberTTL  time.Duration

	mu           sync.Mutex
	cfg          *config.Config
	sessions     map[string]time.Time
	probes       map[string]probeResult
	stats        *stats.Store
	capture      *capture.Manager
	authFailures *authfail.Tracker
	routeHealth  func() []health.Route
	// migrationError reports the stats identity migration failure, if any.
	migrationError func() string

	wsMu          sync.Mutex
	wsConns       map[*wsClient]struct{}
	wsInterval    time.Duration
	wsPctInterval time.Duration
	wsMaxAge      time.Duration

	// usageCache is keyed by provider id.
	usageMu         sync.Mutex
	usageCache      map[string]usageCacheEntry
	usageGeneration uint64

	// modelsCache and modelsInflight are keyed by provider id.
	modelsCache    map[string]modelsCacheEntry
	modelsInflight map[string]*modelsFlight
}

// New creates a management API server. cfg.Admin.PasswordHash must be set by
// the caller; an empty hash disables login.
func New(cfgPath string, cfg *config.Config, r Reloader, log logger.Logger) *Server {
	if log == nil {
		log = xlogger.Nop()
	}
	ttl := time.Duration(cfg.Admin.SessionTTL)
	if ttl <= 0 {
		ttl = 12 * time.Hour
	}
	rememberTTL := time.Duration(cfg.Admin.RememberTTL)
	if rememberTTL <= 0 {
		rememberTTL = 30 * 24 * time.Hour
	}
	s := &Server{
		cfgPath:        cfgPath,
		reloader:       r,
		log:            log,
		passwordHash:   cfg.Admin.PasswordHash,
		ttl:            ttl,
		rememberTTL:    rememberTTL,
		cfg:            cfg,
		sessions:       make(map[string]time.Time),
		probes:         make(map[string]probeResult),
		usageCache:     make(map[string]usageCacheEntry),
		modelsCache:    make(map[string]modelsCacheEntry),
		modelsInflight: make(map[string]*modelsFlight),
		wsConns:        make(map[*wsClient]struct{}),
		wsInterval:     time.Second,
		wsPctInterval:  10 * time.Second,
		wsMaxAge:       7 * 24 * time.Hour,
	}
	if sp, ok := r.(interface{ Stats() *stats.Store }); ok {
		s.stats = sp.Stats()
	}
	if cp, ok := r.(interface{ Capture() *capture.Manager }); ok {
		s.capture = cp.Capture()
	}
	if hp, ok := r.(interface{ RouteHealth() []health.Route }); ok {
		s.routeHealth = hp.RouteHealth
	}
	if af, ok := r.(interface{ AuthFailures() *authfail.Tracker }); ok {
		s.authFailures = af.AuthFailures()
	}
	if s.stats != nil {
		s.migrationError = s.stats.MigrationError
		if saved, err := s.stats.LoadSessions(context.Background(), time.Now()); err != nil {
			log.Warnf("load sessions: %v", err)
		} else {
			for token, exp := range saved {
				s.sessions[token] = exp
			}
		}
	}
	return s
}

// SetAuthFailures attaches the data-plane auth failure tracker. New also picks
// it up from the Reloader when that value implements AuthFailures.
func (s *Server) SetAuthFailures(t *authfail.Tracker) {
	s.authFailures = t
}

// SetConfig replaces the management snapshot after an external config reload
// (SIGHUP), so later admin mutations do not write stale values back to disk.
func (s *Server) SetConfig(cfg *config.Config) {
	if cfg == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.invalidateModelsLocked(cfg)
	s.invalidateUsageLocked()
	s.cfg = cfg
	s.passwordHash = cfg.Admin.PasswordHash
}

// Handler returns the API router.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("POST /api/login", s.handleLogin)
	mux.HandleFunc("POST /api/logout", s.handleLogout)

	api := http.NewServeMux()
	api.HandleFunc("GET /api/config", s.handleGetConfig)
	api.HandleFunc("PUT /api/config", s.handlePutConfig)
	api.HandleFunc("GET /api/presets", s.handlePresets)
	api.HandleFunc("GET /api/providers", s.handleListProviders)
	api.HandleFunc("POST /api/providers", s.handleCreateProvider)
	api.HandleFunc("GET /api/providers/{id}", s.handleGetProvider)
	api.HandleFunc("PUT /api/providers/{id}", s.handleUpdateProvider)
	api.HandleFunc("DELETE /api/providers/{id}", s.handleDeleteProvider)
	api.HandleFunc("POST /api/providers/{id}/test", s.handleTestProvider)
	api.HandleFunc("GET /api/providers/{id}/models", s.handleProviderModels)
	api.HandleFunc("GET /api/nodes", s.handleListNodes)
	api.HandleFunc("POST /api/nodes", s.handleCreateNode)
	api.HandleFunc("GET /api/nodes/{id}", s.handleGetNode)
	api.HandleFunc("PUT /api/nodes/{id}", s.handleUpdateNode)
	api.HandleFunc("DELETE /api/nodes/{id}", s.handleDeleteNode)
	api.HandleFunc("GET /api/routes", s.handleListRoutes)
	api.HandleFunc("GET /api/routes/health", s.handleRouteHealth)
	api.HandleFunc("POST /api/routes", s.handleCreateRoute)
	api.HandleFunc("PUT /api/routes/{id}", s.handleUpdateRoute)
	api.HandleFunc("DELETE /api/routes/{id}", s.handleDeleteRoute)
	api.HandleFunc("GET /api/entrypoints", s.handleGetEntrypoints)
	api.HandleFunc("PUT /api/entrypoints", s.handlePutEntrypoints)
	api.HandleFunc("GET /api/gateway-keys", s.handleListGatewayKeys)
	api.HandleFunc("POST /api/gateway-keys", s.handleCreateGatewayKey)
	api.HandleFunc("PUT /api/gateway-keys/{id}", s.handleUpdateGatewayKey)
	api.HandleFunc("POST /api/gateway-keys/{id}/rotate", s.handleRotateGatewayKey)
	api.HandleFunc("DELETE /api/gateway-keys/{id}", s.handleDeleteGatewayKey)
	api.HandleFunc("GET /api/gateway-key-usage", s.handleGatewayKeyUsage)
	api.HandleFunc("GET /api/stats/summary", s.handleSummary)
	api.HandleFunc("PUT /api/stats/capture", s.handlePutStatsCapture)
	api.HandleFunc("GET /api/stats/errors", s.handleErrorSummary)
	api.HandleFunc("GET /api/alerts", s.handleAlerts)
	api.HandleFunc("GET /api/provider-usage", s.handleProviderUsage)
	api.HandleFunc("GET /api/requests", s.handleRequests)
	api.HandleFunc("GET /api/requests/{id}", s.handleRequest)
	api.HandleFunc("GET /api/captures", s.handleListCaptures)
	api.HandleFunc("POST /api/captures", s.handleCreateCapture)
	api.HandleFunc("DELETE /api/captures/{id}", s.handleDeleteCapture)
	api.HandleFunc("GET /api/stats/graph", s.handleGraph)
	api.HandleFunc("GET /api/events", s.handleEvents)
	api.HandleFunc("DELETE /api/auth-failures", s.handleClearAuthFailures)
	api.HandleFunc("GET /api/topology/positions", s.handleGetTopologyPositions)
	api.HandleFunc("PUT /api/topology/positions", s.handlePutTopologyPositions)
	api.HandleFunc("PUT /api/admin/password", s.handlePassword)

	mux.Handle("/api/", s.requireAuth(api))
	mux.HandleFunc("/", s.handleWeb)
	return mux
}

type healthResponse struct {
	OK      bool   `json:"ok"`
	Version string `json:"version"`
	Commit  string `json:"commit,omitempty"`
	BuiltAt string `json:"builtAt,omitempty"`
	// StatsMigrationError is set when the one-time v1 stats rewrite could not
	// run (for example a failed backup). Absent when the database is current.
	StatsMigrationError string `json:"statsMigrationError,omitempty"`
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	info := version.Current()
	resp := healthResponse{
		OK:      true,
		Version: info.Version,
		Commit:  info.Commit,
		BuiltAt: info.BuiltAt,
	}
	if s.migrationError != nil {
		resp.StatsMigrationError = s.migrationError()
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handlePresets(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, presets)
}

// nonNilSlice keeps list endpoints returning JSON arrays instead of null.
func nonNilSlice[T any](v []T) []T {
	if v == nil {
		return []T{}
	}
	return v
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
