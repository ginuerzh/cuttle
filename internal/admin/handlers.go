package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"strings"
	"time"

	"github.com/go-gost/llm-gw/internal/config"
	"github.com/go-gost/llm-gw/internal/logging"
	"github.com/go-gost/llm-gw/internal/provider"
)

type providerView struct {
	Name        string                          `json:"name"`
	Type        string                          `json:"type"`
	Chat        string                          `json:"chat,omitempty"`
	Responses   string                          `json:"responses,omitempty"`
	Anthropic   string                          `json:"anthropic,omitempty"`
	Gemini      string                          `json:"gemini,omitempty"`
	KeysSet     int                             `json:"keysSet"`
	KeyPreviews []string                        `json:"keyPreviews"`
	Auth        config.Auth                     `json:"auth"`
	Health      config.Health                   `json:"health"`
	TLS         config.TLS                      `json:"tls"`
	Enabled     *bool                           `json:"enabled,omitempty"`
	Models      map[string]config.ModelOverride `json:"models,omitempty"`
	Probe       *probeResult                    `json:"probe,omitempty"`
}

type configView struct {
	Gateway struct {
		Listen string `json:"listen"`
	} `json:"gateway"`
	Admin struct {
		Listen     string          `json:"listen"`
		SessionTTL config.Duration `json:"sessionTTL"`
	} `json:"admin"`
	Stats       config.Stats        `json:"stats"`
	Log         config.Log          `json:"log"`
	Entrypoints []config.Entrypoint `json:"entrypoints"`
	Providers   []providerView      `json:"providers"`
	Nodes       []config.Node       `json:"nodes"`
	Routes      []config.Route      `json:"routes"`
}

func (s *Server) viewProvider(p config.Provider) providerView {
	v := providerView{
		Name:        p.Name,
		Type:        p.Type,
		Chat:        p.Chat,
		Responses:   p.Responses,
		Anthropic:   p.Anthropic,
		Gemini:      p.Gemini,
		KeysSet:     len(p.Keys),
		KeyPreviews: make([]string, 0, len(p.Keys)),
		Auth:        p.Auth,
		Health:      p.Health,
		TLS:         p.TLS,
		Enabled:     p.Enabled,
		Models:      maps.Clone(p.Models),
	}
	for _, k := range p.Keys {
		v.KeyPreviews = append(v.KeyPreviews, preview(k))
	}
	if pr, ok := s.probes[p.Name]; ok {
		v.Probe = &pr
	}
	return v
}

func preview(key string) string {
	if len(key) <= 8 {
		return "****"
	}
	return key[:3] + "..." + key[len(key)-4:]
}

func (s *Server) clone() *config.Config {
	b, err := json.Marshal(s.cfg)
	if err != nil {
		panic(err)
	}
	var c config.Config
	if err := json.Unmarshal(b, &c); err != nil {
		panic(err)
	}
	c.Admin.PasswordHash = s.cfg.Admin.PasswordHash
	return &c
}

func (s *Server) replaceLocked(c *config.Config) error {
	c.Admin.PasswordHash = s.passwordHash
	config.ApplyDefaults(c)
	if err := config.Validate(c); err != nil {
		return err
	}
	if err := validateProviderImmutability(s.cfg, c); err != nil {
		return err
	}
	if err := s.reloader.Reload(c); err != nil {
		return err
	}
	if err := config.Write(s.cfgPath, c); err != nil {
		return err
	}
	s.invalidateModelsLocked(c)
	s.cfg = c
	logging.Apply(s.log, c.Log.Level, c.Log.Format)
	return nil
}

func (s *Server) mutate(fn func(*config.Config) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	c := s.clone()
	if err := fn(c); err != nil {
		return err
	}
	return s.replaceLocked(c)
}

// validateProviderImmutability rejects provider identity changes through the
// management API. A provider's type is fixed after creation, and so are the
// API base URLs of non-generic providers; operators must delete and recreate
// the provider instead. New and deleted providers are always allowed.
func validateProviderImmutability(old, next *config.Config) error {
	if old == nil || next == nil {
		return nil
	}

	nextByName := make(map[string]config.Provider, len(next.Providers))
	for _, p := range next.Providers {
		nextByName[p.Name] = p
	}

	for _, prev := range old.Providers {
		updated, ok := nextByName[prev.Name]
		if !ok {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(prev.Type), strings.TrimSpace(updated.Type)) {
			return fmt.Errorf("provider %q: type cannot change after creation; delete and recreate it", prev.Name)
		}
		if strings.EqualFold(strings.TrimSpace(prev.Type), "generic") {
			continue
		}
		if !sameBaseURL(prev.Chat, updated.Chat) ||
			!sameBaseURL(prev.Responses, updated.Responses) ||
			!sameBaseURL(prev.Anthropic, updated.Anthropic) ||
			!sameBaseURL(prev.Gemini, updated.Gemini) {
			return fmt.Errorf("provider %q: base URL cannot change after creation for type %q", prev.Name, prev.Type)
		}
	}
	return nil
}

// sameBaseURL compares base URLs ignoring surrounding whitespace.
func sameBaseURL(a, b string) bool {
	return strings.TrimSpace(a) == strings.TrimSpace(b)
}

func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var v configView
	v.Gateway.Listen = s.cfg.Gateway.Listen
	v.Admin.Listen = s.cfg.Admin.Listen
	v.Admin.SessionTTL = s.cfg.Admin.SessionTTL
	v.Stats = s.cfg.Stats
	v.Log = s.cfg.Log
	v.Entrypoints = nonNilSlice(s.cfg.Entrypoints)
	v.Nodes = nonNilSlice(s.cfg.Nodes)
	v.Routes = nonNilSlice(s.cfg.Routes)
	v.Providers = make([]providerView, 0, len(s.cfg.Providers))
	for _, p := range s.cfg.Providers {
		v.Providers = append(v.Providers, s.viewProvider(p))
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) handlePutConfig(w http.ResponseWriter, r *http.Request) {
	var c config.Config
	if err := decode(w, r, &c); err != nil {
		return
	}
	if err := s.mutate(func(dst *config.Config) error {
		c.Admin.PasswordHash = dst.Admin.PasswordHash
		*dst = c
		return nil
	}); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleListProviders(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]providerView, 0, len(s.cfg.Providers))
	for _, p := range s.cfg.Providers {
		out = append(out, s.viewProvider(p))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleGetProvider(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	s.mu.Lock()
	defer s.mu.Unlock()

	for _, p := range s.cfg.Providers {
		if p.Name == name {
			writeJSON(w, http.StatusOK, s.viewProvider(p))
			return
		}
	}
	writeError(w, http.StatusNotFound, "provider not found")
}

func (s *Server) handleCreateProvider(w http.ResponseWriter, r *http.Request) {
	var p config.Provider
	if err := decode(w, r, &p); err != nil {
		return
	}
	if strings.TrimSpace(p.Name) == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}

	err := s.mutate(func(c *config.Config) error {
		for _, existing := range c.Providers {
			if existing.Name == p.Name {
				return fmt.Errorf("provider %q already exists", p.Name)
			}
		}
		c.Providers = append(c.Providers, p)
		return nil
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusCreated)
}

type providerKeyUpdate struct {
	// Index is nil for an appended key.
	Index  *int   `json:"index,omitempty"`
	Key    string `json:"key,omitempty"`
	Delete bool   `json:"delete,omitempty"`
}

type providerUpdate struct {
	config.Provider
	KeyUpdates []providerKeyUpdate `json:"keyUpdates,omitempty"`
}

// applyProviderKeyUpdates deletes existing keys and appends new ones without
// exposing the existing secrets to the client. Existing keys are read-only:
// changing one requires deleting it and adding a replacement.
func applyProviderKeyUpdates(keys []string, updates []providerKeyUpdate) ([]string, error) {
	deleted := make(map[int]bool, len(updates))
	var appended []string
	for _, u := range updates {
		key := strings.TrimSpace(u.Key)
		if u.Index == nil {
			if u.Delete || key == "" {
				return nil, fmt.Errorf("new key requires a non-empty key")
			}
			appended = append(appended, key)
			continue
		}
		i := *u.Index
		if i < 0 || i >= len(keys) {
			return nil, fmt.Errorf("key index %d out of range", i)
		}
		if deleted[i] {
			return nil, fmt.Errorf("duplicate delete for key index %d", i)
		}
		if !u.Delete {
			return nil, fmt.Errorf("key index %d is read-only; delete it and add a new key", i)
		}
		if key != "" {
			return nil, fmt.Errorf("key index %d: delete and key are mutually exclusive", i)
		}
		deleted[i] = true
	}

	out := make([]string, 0, len(keys)-len(deleted)+len(appended))
	for i, key := range keys {
		if deleted[i] {
			continue
		}
		out = append(out, key)
	}
	return append(out, appended...), nil
}

func (s *Server) handleUpdateProvider(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var in providerUpdate
	if err := decode(w, r, &in); err != nil {
		return
	}
	p := in.Provider
	if p.Name != "" && p.Name != name {
		writeError(w, http.StatusBadRequest, "name in body does not match the path")
		return
	}
	if len(in.KeyUpdates) > 0 && len(p.Keys) > 0 {
		writeError(w, http.StatusBadRequest, "keys and keyUpdates cannot both be set")
		return
	}
	p.Name = name

	err := s.mutate(func(c *config.Config) error {
		for i := range c.Providers {
			if c.Providers[i].Name != name {
				continue
			}
			current := c.Providers[i]
			switch {
			case len(in.KeyUpdates) > 0:
				keys, err := applyProviderKeyUpdates(current.Keys, in.KeyUpdates)
				if err != nil {
					return fmt.Errorf("provider %q: %w", name, err)
				}
				p.Keys = keys
			case len(p.Keys) == 0:
				p.Keys = current.Keys
			}
			c.Providers[i] = p
			return nil
		}
		return fmt.Errorf("provider %q not found", name)
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeleteProvider(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	err := s.mutate(func(c *config.Config) error {
		for _, n := range c.Nodes {
			if n.Provider == name {
				return fmt.Errorf("provider %q is used by node %q", name, n.Name)
			}
		}
		out := c.Providers[:0]
		found := false
		for _, p := range c.Providers {
			if p.Name == name {
				found = true
				continue
			}
			out = append(out, p)
		}
		if !found {
			return fmt.Errorf("provider %q not found", name)
		}
		c.Providers = out
		return nil
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleTestProvider(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	s.mu.Lock()
	var p config.Provider
	found := false
	for _, candidate := range s.cfg.Providers {
		if candidate.Name == name {
			p, found = candidate, true
			break
		}
	}
	s.mu.Unlock()

	if !found {
		writeError(w, http.StatusNotFound, "provider not found")
		return
	}

	res := probeHTTP(r.Context(), p)
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleProviderModels(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	refresh := r.URL.Query().Get("refresh") == "1"

	models, fetchedAt, err := s.providerModels(r.Context(), name, refresh)
	if err != nil {
		if errors.Is(err, errProviderNotFound) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	// Read the current overrides after the fetch: the provider may have been
	// edited while the upstream call was in flight.
	s.mu.Lock()
	p, ok := s.providerLocked(name)
	s.mu.Unlock()
	if !ok {
		writeError(w, http.StatusNotFound, errProviderNotFound.Error())
		return
	}
	models = applyModelOverrides(models, p.Models)

	if at := formatFetchedAt(fetchedAt); at != "" {
		w.Header().Set("X-Models-Fetched-At", at)
	}
	writeJSON(w, http.StatusOK, models)
}

func (s *Server) handleListRoutes(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	writeJSON(w, http.StatusOK, nonNilSlice(s.cfg.Routes))
}

func (s *Server) handleCreateRoute(w http.ResponseWriter, r *http.Request) {
	var rt config.Route
	if err := decode(w, r, &rt); err != nil {
		return
	}
	if strings.TrimSpace(rt.Name) == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}

	err := s.mutate(func(c *config.Config) error {
		for _, existing := range c.Routes {
			if existing.Name == rt.Name {
				return fmt.Errorf("route %q already exists", rt.Name)
			}
		}
		c.Routes = append(c.Routes, rt)
		return nil
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusCreated)
}

func (s *Server) handleUpdateRoute(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var rt config.Route
	if err := decode(w, r, &rt); err != nil {
		return
	}
	if rt.Name != "" && rt.Name != name {
		writeError(w, http.StatusBadRequest, "name in body does not match the path")
		return
	}
	rt.Name = name

	err := s.mutate(func(c *config.Config) error {
		for i := range c.Routes {
			if c.Routes[i].Name != name {
				continue
			}
			c.Routes[i] = rt
			return nil
		}
		return fmt.Errorf("route %q not found", name)
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeleteRoute(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	err := s.mutate(func(c *config.Config) error {
		out := c.Routes[:0]
		found := false
		for _, rt := range c.Routes {
			if rt.Name == name {
				found = true
				continue
			}
			out = append(out, rt)
		}
		if !found {
			return fmt.Errorf("route %q not found", name)
		}
		var refs []string
		for _, e := range c.Entrypoints {
			if strings.EqualFold(strings.TrimSpace(e.Route), name) {
				refs = append(refs, e.Name)
			}
		}
		if len(refs) > 0 {
			return fmt.Errorf("route %q: still referenced by entrypoints %s; update or delete them first",
				name, strings.Join(refs, ", "))
		}
		c.Routes = out
		return nil
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleGetEntrypoints(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	writeJSON(w, http.StatusOK, nonNilSlice(s.cfg.Entrypoints))
}

func (s *Server) handlePutEntrypoints(w http.ResponseWriter, r *http.Request) {
	var entrypoints []config.Entrypoint
	if err := decode(w, r, &entrypoints); err != nil {
		return
	}

	err := s.mutate(func(c *config.Config) error {
		c.Entrypoints = entrypoints
		return nil
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func decode(w http.ResponseWriter, r *http.Request, v any) error {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return err
	}
	return nil
}

// fetchModels returns the provider catalog through its adapter.
func fetchModels(ctx context.Context, p config.Provider) ([]provider.Model, error) {
	prov, err := provider.New(p)
	if err != nil {
		return nil, err
	}
	return prov.Models(ctx)
}

// applyAuthConfig injects key into req according to a. Extra headers are
// applied even for keyless providers; key-derived headers are skipped when
// key is empty.
func applyAuthConfig(req *http.Request, a config.Auth, key string) {
	if key != "" {
		if a.Header != "" {
			req.Header.Set(a.Header, a.Prefix+key)
		}
		for _, kh := range a.KeyHeaders {
			if kh.Header == "" {
				continue
			}
			req.Header.Set(kh.Header, kh.Prefix+key)
		}
	}
	for k, v := range a.Extra {
		req.Header.Set(k, v)
	}
}

func (s *Server) handlePassword(w http.ResponseWriter, r *http.Request) {
	var in struct {
		OldPassword string `json:"oldPassword"`
		NewPassword string `json:"newPassword"`
	}
	if err := decode(w, r, &in); err != nil {
		return
	}
	if !config.VerifyPassword(s.passwordHash, in.OldPassword) {
		writeError(w, http.StatusUnauthorized, "old password is incorrect")
		return
	}
	if len(in.NewPassword) < 8 {
		writeError(w, http.StatusBadRequest, "new password must be at least 8 characters")
		return
	}

	hash, err := config.HashPassword(in.NewPassword)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	c := s.clone()
	c.Admin.PasswordHash = hash
	config.ApplyDefaults(c)
	if err := config.Validate(c); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.reloader.Reload(c); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := config.Write(s.cfgPath, c); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.cfg = c
	s.passwordHash = hash
	s.sessions = make(map[string]time.Time)
	if s.stats != nil {
		if err := s.stats.DeleteAllSessions(r.Context()); err != nil {
			s.log.Warnf("clear sessions: %v", err)
		}
	}
	w.WriteHeader(http.StatusNoContent)
}
