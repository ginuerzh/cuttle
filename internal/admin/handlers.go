package admin

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-gost/llm-gw/internal/config"
)

type providerView struct {
	Name        string            `json:"name"`
	BaseURL     string            `json:"baseURL"`
	Protocol    string            `json:"protocol"`
	KeysSet     int               `json:"keysSet"`
	KeyPreviews []string          `json:"keyPreviews"`
	Auth        config.Auth       `json:"auth"`
	PathMap     map[string]string `json:"pathMap,omitempty"`
	Health      config.Health     `json:"health"`
	TLS         config.TLS        `json:"tls"`
	Enabled     *bool             `json:"enabled,omitempty"`
	Probe       *probeResult      `json:"probe,omitempty"`
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
	Effort    config.Effort  `json:"effort"`
	Aliases   []config.Alias `json:"aliases"`
	Providers []providerView `json:"providers"`
	Nodes     []config.Node  `json:"nodes"`
	Routes    []config.Route `json:"routes"`
}

func (s *Server) viewProvider(p config.Provider) providerView {
	v := providerView{
		Name:        p.Name,
		BaseURL:     p.BaseURL,
		Protocol:    p.Protocol,
		KeysSet:     len(p.Keys),
		KeyPreviews: make([]string, 0, len(p.Keys)),
		Auth:        p.Auth,
		PathMap:     p.PathMap,
		Health:      p.Health,
		TLS:         p.TLS,
		Enabled:     p.Enabled,
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
	if err := s.reloader.Reload(c); err != nil {
		return err
	}
	if err := config.Write(s.cfgPath, c); err != nil {
		return err
	}
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

func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var v configView
	v.Gateway.Listen = s.cfg.Gateway.Listen
	v.Admin.Listen = s.cfg.Admin.Listen
	v.Admin.SessionTTL = s.cfg.Admin.SessionTTL
	v.Stats = s.cfg.Stats
	v.Effort = s.cfg.Effort
	v.Aliases = s.cfg.Aliases
	v.Nodes = s.cfg.Nodes
	v.Routes = s.cfg.Routes
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

	res := probeProvider(r.Context(), p)
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleProviderModels(w http.ResponseWriter, r *http.Request) {
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

	models, err := fetchModels(r.Context(), p)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, models)
}

func (s *Server) handleListRoutes(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	writeJSON(w, http.StatusOK, s.cfg.Routes)
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
			if c.Routes[i].Name == name {
				c.Routes[i] = rt
				return nil
			}
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
		c.Routes = out
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
	writeJSON(w, http.StatusOK, s.cfg.Aliases)
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

func probeProvider(ctx context.Context, p config.Provider) probeResult {
	u, err := url.Parse(p.BaseURL)
	if err != nil || u.Host == "" {
		return probeResult{Error: "invalid baseURL"}
	}

	port := u.Port()
	if port == "" {
		if u.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	addr := net.JoinHostPort(u.Hostname(), port)

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	start := time.Now()
	d := net.Dialer{}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return probeResult{Error: err.Error()}
	}
	defer conn.Close()

	if u.Scheme == "https" {
		insecure := p.TLS.Secure != nil && !*p.TLS.Secure
		tc := tls.Client(conn, &tls.Config{ServerName: u.Hostname(), InsecureSkipVerify: insecure})
		if err := tc.HandshakeContext(ctx); err != nil {
			return probeResult{Error: err.Error()}
		}
	}
	return probeResult{
		OK:        true,
		LatencyMS: time.Since(start).Milliseconds(),
		CheckedAt: time.Now().Format(time.RFC3339),
	}
}

func fetchModels(ctx context.Context, p config.Provider) ([]string, error) {
	path := p.Health.Path
	if path == "" {
		path = "/models"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(p.BaseURL, "/")+path, nil)
	if err != nil {
		return nil, err
	}
	if len(p.Keys) > 0 {
		applyAuth(req, p, p.Keys[0])
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("models endpoint returned %d", resp.StatusCode)
	}

	var body struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(body.Data))
	for _, m := range body.Data {
		if m.ID != "" {
			out = append(out, m.ID)
		}
	}
	return out, nil
}

func applyAuth(req *http.Request, p config.Provider, key string) {
	if key == "" {
		return
	}
	if p.Auth.Header != "" {
		req.Header.Set(p.Auth.Header, p.Auth.Prefix+key)
	}
	for _, kh := range p.Auth.KeyHeaders {
		req.Header.Set(kh.Header, kh.Prefix+key)
	}
	for k, v := range p.Auth.Extra {
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
