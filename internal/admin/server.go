// Package admin exposes the llm-gw management HTTP API.
package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/go-gost/core/logger"
	xlogger "github.com/go-gost/x/logger"

	"github.com/go-gost/llm-gw/internal/config"
	"github.com/go-gost/llm-gw/internal/stats"
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

	mu       sync.Mutex
	cfg      *config.Config
	sessions map[string]time.Time
	probes   map[string]probeResult
	stats    *stats.Store

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
		modelsCache:    make(map[string]modelsCacheEntry),
		modelsInflight: make(map[string]*modelsFlight),
	}
	if sp, ok := r.(interface{ Stats() *stats.Store }); ok {
		s.stats = sp.Stats()
	}
	if s.stats != nil {
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
	api.HandleFunc("GET /api/providers/{name}", s.handleGetProvider)
	api.HandleFunc("PUT /api/providers/{name}", s.handleUpdateProvider)
	api.HandleFunc("DELETE /api/providers/{name}", s.handleDeleteProvider)
	api.HandleFunc("POST /api/providers/{name}/test", s.handleTestProvider)
	api.HandleFunc("GET /api/providers/{name}/models", s.handleProviderModels)
	api.HandleFunc("GET /api/nodes", s.handleListNodes)
	api.HandleFunc("POST /api/nodes", s.handleCreateNode)
	api.HandleFunc("GET /api/nodes/{name}", s.handleGetNode)
	api.HandleFunc("PUT /api/nodes/{name}", s.handleUpdateNode)
	api.HandleFunc("DELETE /api/nodes/{name}", s.handleDeleteNode)
	api.HandleFunc("GET /api/routes", s.handleListRoutes)
	api.HandleFunc("POST /api/routes", s.handleCreateRoute)
	api.HandleFunc("PUT /api/routes/{name}", s.handleUpdateRoute)
	api.HandleFunc("DELETE /api/routes/{name}", s.handleDeleteRoute)
	api.HandleFunc("GET /api/entrypoints", s.handleGetEntrypoints)
	api.HandleFunc("PUT /api/entrypoints", s.handlePutEntrypoints)
	api.HandleFunc("GET /api/stats/summary", s.handleSummary)
	api.HandleFunc("GET /api/requests", s.handleRequests)
	api.HandleFunc("GET /api/requests/{id}", s.handleRequest)
	api.HandleFunc("GET /api/stats/graph", s.handleGraph)
	api.HandleFunc("PUT /api/admin/password", s.handlePassword)

	mux.Handle("/api/", s.requireAuth(api))
	mux.HandleFunc("/", s.handleWeb)
	return mux
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
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
