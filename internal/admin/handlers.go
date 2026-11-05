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
	Stats     config.Stats   `json:"stats"`
	Aliases   []config.Alias `json:"aliases"`
	Providers []providerView `json:"providers"`
	Nodes     []config.Node  `json:"nodes"`
	Routes    []config.Route `json:"routes"`
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
	v.Aliases = nonNilSlice(s.cfg.Aliases)
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

func (s *Server) handleUpdateProvider(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var p config.Provider
	if err := decode(w, r, &p); err != nil {
		return
	}
	if p.Name != "" && p.Name != name {
		writeError(w, http.StatusBadRequest, "name in body does not match the path")
		return
	}
	p.Name = name

	err := s.mutate(func(c *config.Config) error {
		for i := range c.Providers {
			if c.Providers[i].Name != name {
				continue
			}
			if len(p.Keys) == 0 {
				p.Keys = c.Providers[i].Keys
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
	rt.Match.Model = strings.TrimSpace(rt.Match.Model)

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
			oldRaw := c.Routes[i].Match.Model
			old := strings.TrimSpace(oldRaw)
			next := strings.TrimSpace(rt.Match.Model)
			if old != "" && strings.EqualFold(old, next) {
				// Case/space-only edits keep the stored key so Node.Models
				// lookups do not silently fall through to "*".
				rt.Match.Model = oldRaw
				c.Routes[i] = rt
				return nil
			}
			rt.Match.Model = next
			c.Routes[i] = rt
			if old == "" {
				return nil
			}
			remaining := false
			for _, r := range c.Routes {
				if r.Name != name && r.Match.Model != "" && strings.EqualFold(strings.TrimSpace(r.Match.Model), old) {
					remaining = true
					break
				}
			}
			if remaining {
				return nil
			}
			matches := c.AliasMatchesForModel(old)
			if next == "" {
				if len(matches) > 0 {
					return fmt.Errorf("route %q: aliases still reference logical model %q (%s); update or delete them first",
						name, old, strings.Join(matches, ", "))
				}
				return nil
			}
			if err := migrateRouteNodeModels(c, rt.Nodes, old, next); err != nil {
				return fmt.Errorf("route %q: %w", name, err)
			}
			for j := range c.Aliases {
				if strings.EqualFold(strings.TrimSpace(c.Aliases[j].Model), old) {
					c.Aliases[j].Model = next
				}
			}
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

// migrateRouteNodeModels moves a renamed logical-model mapping on the nodes
// used by the route. A conflicting target already mapped under the new name is
// rejected: merging the two names would silently change that route's upstream
// model.
func migrateRouteNodeModels(c *config.Config, refs []string, old, next string) error {
	used := make(map[string]bool, len(refs))
	for _, ref := range refs {
		used[strings.TrimSpace(ref)] = true
	}
	for i := range c.Nodes {
		n := &c.Nodes[i]
		if !used[n.Name] {
			continue
		}
		from, ok := n.Models[old]
		if !ok {
			continue
		}
		if to, exists := n.Models[next]; exists && to != from {
			return fmt.Errorf("node %q maps %q to %q but %q is already mapped to %q",
				n.Name, old, from, next, to)
		}
	}
	for i := range c.Nodes {
		n := &c.Nodes[i]
		if !used[n.Name] {
			continue
		}
		from, ok := n.Models[old]
		if !ok {
			continue
		}
		n.Models[next] = from
		delete(n.Models, old)
	}
	return nil
}

func (s *Server) handleDeleteRoute(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	err := s.mutate(func(c *config.Config) error {
		out := c.Routes[:0]
		removed := config.Route{}
		found := false
		for _, rt := range c.Routes {
			if rt.Name == name {
				removed = rt
				found = true
				continue
			}
			out = append(out, rt)
		}
		if !found {
			return fmt.Errorf("route %q not found", name)
		}
		c.Routes = out
		old := strings.TrimSpace(removed.Match.Model)
		if old != "" && !c.HasLogicalModel(old) {
			if matches := c.AliasMatchesForModel(old); len(matches) > 0 {
				return fmt.Errorf("route %q: still referenced by aliases %s; update or delete them first",
					name, strings.Join(matches, ", "))
			}
		}
		return nil
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleGetAliases(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	writeJSON(w, http.StatusOK, nonNilSlice(s.cfg.Aliases))
}

func (s *Server) handlePutAliases(w http.ResponseWriter, r *http.Request) {
	var aliases []config.Alias
	if err := decode(w, r, &aliases); err != nil {
		return
	}

	err := s.mutate(func(c *config.Config) error {
		c.Aliases = aliases
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

func (s *Server) handleRouteOrder(w http.ResponseWriter, r *http.Request) {
	var names []string
	if err := decode(w, r, &names); err != nil {
		return
	}

	err := s.mutate(func(c *config.Config) error {
		if len(names) != len(c.Routes) {
			return fmt.Errorf("order must list every route (got %d, want %d)", len(names), len(c.Routes))
		}
		byName := make(map[string]config.Route, len(c.Routes))
		for _, rt := range c.Routes {
			byName[rt.Name] = rt
		}
		out := make([]config.Route, 0, len(names))
		for _, name := range names {
			rt, ok := byName[name]
			if !ok {
				return fmt.Errorf("unknown route %q", name)
			}
			out = append(out, rt)
			delete(byName, name)
		}
		if len(byName) != 0 {
			return fmt.Errorf("order is missing routes")
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
