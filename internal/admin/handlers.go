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

	"github.com/ginuerzh/cuttle/internal/config"
	"github.com/ginuerzh/cuttle/internal/health"
	"github.com/ginuerzh/cuttle/internal/identity"
	"github.com/ginuerzh/cuttle/internal/logging"
	"github.com/ginuerzh/cuttle/internal/provider"
)

// providerKeyView is one configured key as the browser sees it. The raw value
// is never included; KeyPreview is the existing admin mask.
type providerKeyView struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	KeyPreview string `json:"keyPreview"`
	Enabled    bool   `json:"enabled"`
}

type providerView struct {
	ID        string                          `json:"id"`
	Name      string                          `json:"name"`
	Type      string                          `json:"type"`
	Chat      string                          `json:"chat,omitempty"`
	Responses string                          `json:"responses,omitempty"`
	Anthropic string                          `json:"anthropic,omitempty"`
	Gemini    string                          `json:"gemini,omitempty"`
	Keys      []providerKeyView               `json:"keys"`
	Auth      config.Auth                     `json:"auth"`
	Health    config.Health                   `json:"health"`
	TLS       config.TLS                      `json:"tls"`
	Enabled   *bool                           `json:"enabled,omitempty"`
	Models    map[string]config.ModelOverride `json:"models,omitempty"`
	Probe     *probeResult                    `json:"probe,omitempty"`
}

type configView struct {
	Gateway struct {
		Listen       string          `json:"listen"`
		ReadTimeout  config.Duration `json:"readTimeout"`
		UserAgent    string          `json:"userAgent,omitempty"`
		KeysSet      int             `json:"keysSet"`
		AuthRequired bool            `json:"authRequired"`
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
		ID:        p.ID,
		Name:      p.Name,
		Type:      p.Type,
		Chat:      p.Chat,
		Responses: p.Responses,
		Anthropic: p.Anthropic,
		Gemini:    p.Gemini,
		Keys:      make([]providerKeyView, 0, len(p.Keys)),
		Auth:      p.Auth,
		Health:    p.Health,
		TLS:       p.TLS,
		Enabled:   p.Enabled,
		Models:    maps.Clone(p.Models),
	}
	for _, k := range p.Keys {
		v.Keys = append(v.Keys, providerKeyView{
			ID:         k.ID,
			Name:       k.Name,
			KeyPreview: preview(k.Key),
			Enabled:    k.EnabledValue(),
		})
	}
	if pr, ok := s.probes[p.ID]; ok {
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

// providerIndexByID returns the index of the provider with the id, or -1. It is
// the write-side lookup: mutations only address entities by id.
func providerIndexByID(c *config.Config, id string) int {
	if id == "" {
		return -1
	}
	for i := range c.Providers {
		if c.Providers[i].ID == id {
			return i
		}
	}
	return -1
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
	s.invalidateUsageLocked()
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
// the provider instead. Providers are matched by id, so a rename keeps every
// rule intact. New and deleted providers are always allowed.
func validateProviderImmutability(old, next *config.Config) error {
	if old == nil || next == nil {
		return nil
	}

	nextByID := make(map[string]config.Provider, len(next.Providers))
	for _, p := range next.Providers {
		nextByID[p.ID] = p
	}

	for _, prev := range old.Providers {
		updated, ok := nextByID[prev.ID]
		if !ok {
			continue
		}
		name := strings.TrimSpace(updated.Name)
		if name == "" {
			name = prev.Name
		}
		if !strings.EqualFold(strings.TrimSpace(prev.Type), strings.TrimSpace(updated.Type)) {
			return fmt.Errorf("provider %q: type cannot change after creation; delete and recreate it", name)
		}
		if strings.EqualFold(strings.TrimSpace(prev.Type), "generic") {
			continue
		}
		if !sameBaseURL(prev.Chat, updated.Chat) ||
			!sameBaseURL(prev.Responses, updated.Responses) ||
			!sameBaseURL(prev.Anthropic, updated.Anthropic) ||
			!sameBaseURL(prev.Gemini, updated.Gemini) {
			return fmt.Errorf("provider %q: base URL cannot change after creation for type %q", name, prev.Type)
		}
	}
	return nil
}

// sameBaseURL compares base URLs ignoring surrounding whitespace.
func sameBaseURL(a, b string) bool {
	return strings.TrimSpace(a) == strings.TrimSpace(b)
}

// entityLabel renders an entity for an error message: its display label and,
// when it has one, the id the API addresses it by.
func entityLabel(name, id string) string {
	name = strings.TrimSpace(name)
	id = strings.TrimSpace(id)
	switch {
	case id == "":
		return fmt.Sprintf("%q", name)
	case name == "":
		return id
	}
	return fmt.Sprintf("%q (%s)", name, id)
}

// adoptEntityID returns the id to keep for one entity of a full replacement:
// the sent id, the stored id for the same display name, or a freshly minted
// id. It is the shared write rule for an id-less payload.
func adoptEntityID(kind identity.Kind, sent, name string, storedByName map[string]string) (string, error) {
	if id := strings.TrimSpace(sent); id != "" {
		return id, nil
	}
	if id, ok := storedByName[strings.TrimSpace(name)]; ok {
		return id, nil
	}
	return identity.New(kind)
}

func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var v configView
	v.Gateway.Listen = s.cfg.Gateway.Listen
	v.Gateway.ReadTimeout = s.cfg.Gateway.ReadTimeout
	v.Gateway.UserAgent = s.cfg.Gateway.UserAgent
	v.Gateway.KeysSet = len(s.cfg.Gateway.Keys)
	v.Gateway.AuthRequired = len(s.cfg.Gateway.Keys) > 0
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
		// GET /api/config never returns key values, so an absent list must
		// preserve them; only an explicit [] clears the set.
		if c.Gateway.Keys == nil {
			c.Gateway.Keys = dst.Gateway.Keys
		}
		// Gateway keys follow the same identity rule as every other entity:
		// an id-less entry keeps the stored id of the same name, and a
		// genuinely new key is minted.
		keyIDs := make(map[string]string, len(dst.Gateway.Keys))
		for _, k := range dst.Gateway.Keys {
			if k.ID != "" {
				keyIDs[strings.TrimSpace(k.Name)] = k.ID
			}
		}
		for i := range c.Gateway.Keys {
			id, err := adoptEntityID(identity.GatewayKey, c.Gateway.Keys[i].ID, c.Gateway.Keys[i].Name, keyIDs)
			if err != nil {
				return err
			}
			c.Gateway.Keys[i].ID = id
		}
		// Provider keys are write-only too. Preserve them per existing
		// provider id when the decoded field is absent; an explicit []
		// still clears and a new provider stays keyless.
		oldByID := make(map[string]config.Provider, len(dst.Providers))
		oldByName := make(map[string]config.Provider, len(dst.Providers))
		for _, p := range dst.Providers {
			if p.ID != "" {
				oldByID[p.ID] = p
			}
			if name := strings.TrimSpace(p.Name); name != "" {
				oldByName[name] = p
			}
		}
		for i := range c.Providers {
			in := &c.Providers[i]
			old, known := oldByID[in.ID]
			if in.ID == "" {
				// An id-less entity is not a new identity: adopt the
				// stored one when the name matches, so its keys and
				// its history stay attached. Only a genuinely new
				// entity gets a freshly minted id.
				if stored, found := oldByName[strings.TrimSpace(in.Name)]; found {
					in.ID = stored.ID
					old, known = stored, true
				} else {
					id, err := identity.New(identity.Provider)
					if err != nil {
						return err
					}
					in.ID = id
				}
			}
			if !known {
				keys, err := mergeProviderKeys(nil, in.Keys)
				if err != nil {
					return fmt.Errorf("provider %q: %w", in.Name, err)
				}
				in.Keys = keys
				continue
			}
			adoptKeyIDs(in.Keys, old.Keys)
			keys, err := mergeProviderKeys(old.Keys, in.Keys)
			if err != nil {
				return fmt.Errorf("provider %q: %w", in.Name, err)
			}
			if err := ensureKeysNotPinned(&c, keys, old, *in); err != nil {
				return fmt.Errorf("provider %q: %w", in.Name, err)
			}
			in.Keys = keys
		}
		// Nodes, routes and entrypoints follow the same identity rule: an
		// id-less entity adopts the stored id of the same name, and only a
		// genuinely new entity is minted.
		nodeIDs := make(map[string]string, len(dst.Nodes))
		for _, n := range dst.Nodes {
			if n.ID != "" {
				nodeIDs[strings.TrimSpace(n.Name)] = n.ID
			}
		}
		for i := range c.Nodes {
			id, err := adoptEntityID(identity.Node, c.Nodes[i].ID, c.Nodes[i].Name, nodeIDs)
			if err != nil {
				return err
			}
			c.Nodes[i].ID = id
		}
		routeIDs := make(map[string]string, len(dst.Routes))
		for _, rt := range dst.Routes {
			if rt.ID != "" {
				routeIDs[strings.TrimSpace(rt.Name)] = rt.ID
			}
		}
		for i := range c.Routes {
			id, err := adoptEntityID(identity.Route, c.Routes[i].ID, c.Routes[i].Name, routeIDs)
			if err != nil {
				return err
			}
			c.Routes[i].ID = id
		}
		entrypointIDs := make(map[string]string, len(dst.Entrypoints))
		for _, e := range dst.Entrypoints {
			if e.ID != "" {
				entrypointIDs[strings.TrimSpace(e.Name)] = e.ID
			}
		}
		for i := range c.Entrypoints {
			id, err := adoptEntityID(identity.Entrypoint, c.Entrypoints[i].ID, c.Entrypoints[i].Name, entrypointIDs)
			if err != nil {
				return err
			}
			c.Entrypoints[i].ID = id
		}
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
	ref := r.PathValue("id")

	s.mu.Lock()
	defer s.mu.Unlock()

	if p, ok := s.providerLocked(ref); ok {
		writeJSON(w, http.StatusOK, s.viewProvider(p))
		return
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

	// The server owns the id; a client-supplied one is ignored.
	id, err := identity.New(identity.Provider)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	p.ID = id
	keys, err := mergeProviderKeys(nil, p.Keys)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("provider %q: %v", p.Name, err))
		return
	}
	p.Keys = keys

	err = s.mutate(func(c *config.Config) error {
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

// providerKeyUpdate is one write-only key change addressed by key id. An entry
// with an empty id and a value appends a new key and receives a generated id.
type providerKeyUpdate struct {
	ID      string `json:"id,omitempty"`
	Name    string `json:"name,omitempty"`
	Key     string `json:"key,omitempty"`
	Enabled *bool  `json:"enabled,omitempty"`
	Delete  bool   `json:"delete,omitempty"`
}

type providerUpdate struct {
	config.Provider
	KeyUpdates []providerKeyUpdate `json:"keyUpdates,omitempty"`
}

// applyProviderKeyUpdates applies key changes by key id without exposing the
// existing secrets. A rename, a value replacement and an enabled flag may be
// combined for one id; a delete runs on its own. Updates apply in request
// order, so one request may delete a key and append a replacement with the
// same name.
func applyProviderKeyUpdates(keys []config.ProviderKey, updates []providerKeyUpdate) ([]config.ProviderKey, error) {
	out := make([]config.ProviderKey, len(keys))
	copy(out, keys)

	indexOf := func(id string) int {
		for i := range out {
			if out[i].ID == id {
				return i
			}
		}
		return -1
	}
	nameTaken := func(name string, except int) bool {
		for i := range out {
			if i != except && strings.TrimSpace(out[i].Name) == name {
				return true
			}
		}
		return false
	}

	for _, u := range updates {
		id := strings.TrimSpace(u.ID)
		name := strings.TrimSpace(u.Name)
		value := strings.TrimSpace(u.Key)

		if id == "" {
			// A new key is identified by its value.
			switch {
			case value == "":
				return nil, fmt.Errorf("key id is required, or a name and key to append a new key")
			case name == "":
				return nil, fmt.Errorf("key name is required")
			case u.Delete:
				return nil, fmt.Errorf("key %q: a new key cannot be deleted", name)
			case nameTaken(name, -1):
				return nil, fmt.Errorf("key %q already exists", name)
			}
			newID, err := identity.New(identity.ProviderKey)
			if err != nil {
				return nil, err
			}
			out = append(out, config.ProviderKey{ID: newID, Name: name, Key: value, Enabled: u.Enabled})
			continue
		}

		i := indexOf(id)
		if i < 0 {
			return nil, fmt.Errorf("key %q is not configured", id)
		}
		if u.Delete {
			if name != "" || value != "" || u.Enabled != nil {
				return nil, fmt.Errorf("key %q: delete cannot be combined with name, key or enabled", id)
			}
			out = append(out[:i], out[i+1:]...)
			continue
		}
		if name == "" && value == "" && u.Enabled == nil {
			return nil, fmt.Errorf("key %q: name, key, enabled or delete is required", id)
		}
		if name != "" {
			if nameTaken(name, i) {
				return nil, fmt.Errorf("key %q is already used by another key", name)
			}
			out[i].Name = name
		}
		if value != "" {
			out[i].Key = value
		}
		if u.Enabled != nil {
			out[i].Enabled = u.Enabled
		}
	}
	return out, nil
}

// adoptKeyIDs gives every incoming key without an id the id of the stored key
// with the same name, so an id-less full replacement updates the stored keys
// instead of minting new identities for them. A name that is not stored keeps
// its empty id and is minted by mergeProviderKeys.
func adoptKeyIDs(incoming, stored []config.ProviderKey) {
	byName := make(map[string]string, len(stored))
	for _, k := range stored {
		if k.ID != "" {
			byName[strings.TrimSpace(k.Name)] = k.ID
		}
	}
	for i := range incoming {
		if strings.TrimSpace(incoming[i].ID) != "" {
			continue
		}
		if id, ok := byName[strings.TrimSpace(incoming[i].Name)]; ok {
			incoming[i].ID = id
		}
	}
}

// mergeProviderKeys applies a full key replacement: nil preserves the stored
// keys, an empty slice clears them, and a non-empty list merges by key id. An
// entry with an empty id is a new key and receives a generated id, so a
// GET -> PUT round-trip of the masked view is safe: the view carries the ids
// and an entry with an empty value keeps the stored value.
func mergeProviderKeys(old, incoming []config.ProviderKey) ([]config.ProviderKey, error) {
	if incoming == nil {
		return old, nil
	}
	if len(incoming) == 0 {
		return []config.ProviderKey{}, nil
	}

	byID := make(map[string]config.ProviderKey, len(old))
	for _, k := range old {
		if k.ID != "" {
			byID[k.ID] = k
		}
	}

	out := make([]config.ProviderKey, 0, len(incoming))
	names := make(map[string]bool, len(incoming))
	for _, k := range incoming {
		k.ID = strings.TrimSpace(k.ID)
		k.Name = strings.TrimSpace(k.Name)
		value := strings.TrimSpace(k.Key)

		if k.ID == "" {
			if value == "" {
				return nil, fmt.Errorf("key %q: key is required", k.Name)
			}
			id, err := identity.New(identity.ProviderKey)
			if err != nil {
				return nil, err
			}
			k.ID = id
			k.Key = value
		} else {
			prev, ok := byID[k.ID]
			if !ok {
				return nil, fmt.Errorf("key id %q is not configured", k.ID)
			}
			if k.Name == "" {
				k.Name = strings.TrimSpace(prev.Name)
			}
			if value == "" {
				k.Key = prev.Key
			} else {
				k.Key = value
			}
			if k.Enabled == nil {
				k.Enabled = prev.Enabled
			}
		}
		if k.Name == "" {
			return nil, fmt.Errorf("key name is required")
		}
		if names[k.Name] {
			return nil, fmt.Errorf("key %q is already used by another key", k.Name)
		}
		names[k.Name] = true
		out = append(out, k)
	}
	return out, nil
}

// sameProviderRef reports whether a node's provider reference points at one of
// the given snapshots of the same provider: the stored one and, after a rename,
// the incoming one. The reference is the provider id.
func sameProviderRef(ref string, providers ...config.Provider) bool {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return false
	}
	for _, p := range providers {
		if ref == p.ID {
			return true
		}
	}
	return false
}

// sameKeyRef reports whether a node's key pin points at k. The pin is the key
// id.
func sameKeyRef(ref string, k config.ProviderKey) bool {
	return strings.TrimSpace(ref) == k.ID
}

// ensureKeysNotPinned rejects a key change that would leave a node of the same
// provider pointing at a missing key. Every snapshot of the provider (stored
// and incoming) is matched, so a rename cannot hide a pin.
func ensureKeysNotPinned(c *config.Config, keys []config.ProviderKey, providers ...config.Provider) error {
	for _, n := range c.Nodes {
		if !sameProviderRef(n.Provider, providers...) || strings.TrimSpace(n.Key) == "" {
			continue
		}
		found := false
		for _, k := range keys {
			if sameKeyRef(n.Key, k) {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("key %q is used by node %q", n.Key, n.Name)
		}
	}
	return nil
}

func (s *Server) handleUpdateProvider(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var in providerUpdate
	if err := decode(w, r, &in); err != nil {
		return
	}
	p := in.Provider
	if p.ID != "" && p.ID != id {
		writeError(w, http.StatusBadRequest, "id in body does not match the path")
		return
	}
	if len(in.KeyUpdates) > 0 && len(p.Keys) > 0 {
		writeError(w, http.StatusBadRequest, "keys and keyUpdates cannot both be set")
		return
	}
	p.ID = id

	err := s.mutate(func(c *config.Config) error {
		i := providerIndexByID(c, id)
		if i < 0 {
			return fmt.Errorf("provider %q not found", id)
		}
		current := c.Providers[i]
		if strings.TrimSpace(p.Name) == "" {
			// The name is a display label: an omitted one keeps the
			// stored label instead of blanking it.
			p.Name = current.Name
		}
		var (
			keys []config.ProviderKey
			err  error
		)
		if len(in.KeyUpdates) > 0 {
			keys, err = applyProviderKeyUpdates(current.Keys, in.KeyUpdates)
		} else {
			keys, err = mergeProviderKeys(current.Keys, p.Keys)
		}
		if err != nil {
			return fmt.Errorf("provider %q: %w", id, err)
		}
		if err := ensureKeysNotPinned(c, keys, current, p); err != nil {
			return fmt.Errorf("provider %q: %w", id, err)
		}
		p.Keys = keys
		c.Providers[i] = p
		return nil
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeleteProvider(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	err := s.mutate(func(c *config.Config) error {
		i := providerIndexByID(c, id)
		if i < 0 {
			return fmt.Errorf("provider %q not found", id)
		}
		target := c.Providers[i]
		for _, n := range c.Nodes {
			if sameProviderRef(n.Provider, target) {
				return fmt.Errorf("provider %q is used by node %q", target.Name, n.Name)
			}
		}
		c.Providers = append(c.Providers[:i], c.Providers[i+1:]...)
		return nil
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleTestProvider(w http.ResponseWriter, r *http.Request) {
	ref := r.PathValue("id")

	s.mu.Lock()
	p, found := s.providerLocked(ref)
	s.mu.Unlock()

	if !found {
		writeError(w, http.StatusNotFound, "provider not found")
		return
	}

	res := probeHTTP(r.Context(), p)
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleProviderModels(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	refresh := r.URL.Query().Get("refresh") == "1"

	models, fetchedAt, err := s.providerModels(r.Context(), id, refresh)
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
	p, ok := s.providerLocked(id)
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

// routeIndexByID returns the index of the route with the id, or -1. It is the
// write-side lookup: mutations only address routes by id.
func routeIndexByID(c *config.Config, id string) int {
	if id == "" {
		return -1
	}
	for i := range c.Routes {
		if c.Routes[i].ID == id {
			return i
		}
	}
	return -1
}

// sameRouteRef reports whether an entrypoint's route reference points at rt.
// The reference is the route id.
func sameRouteRef(ref string, rt config.Route) bool {
	return strings.TrimSpace(ref) == rt.ID
}

func (s *Server) handleRouteHealth(w http.ResponseWriter, r *http.Request) {
	if s.routeHealth == nil {
		writeJSON(w, http.StatusOK, []health.Route{})
		return
	}
	// Already id-bearing: the payload carries route, node and provider ids
	// beside their display labels.
	writeJSON(w, http.StatusOK, nonNilSlice(s.routeHealth()))
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

	// The server owns the id; a client-supplied one is ignored.
	id, err := identity.New(identity.Route)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	rt.ID = id

	err = s.mutate(func(c *config.Config) error {
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
	id := r.PathValue("id")
	var rt config.Route
	if err := decode(w, r, &rt); err != nil {
		return
	}
	if rt.ID != "" && rt.ID != id {
		writeError(w, http.StatusBadRequest, "id in body does not match the path")
		return
	}
	rt.ID = id

	err := s.mutate(func(c *config.Config) error {
		i := routeIndexByID(c, id)
		if i < 0 {
			return fmt.Errorf("route %q not found", id)
		}
		if strings.TrimSpace(rt.Name) == "" {
			// The name is a display label: an omitted one keeps the
			// stored label instead of blanking it.
			rt.Name = c.Routes[i].Name
		}
		c.Routes[i] = rt
		return nil
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeleteRoute(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	err := s.mutate(func(c *config.Config) error {
		i := routeIndexByID(c, id)
		if i < 0 {
			return fmt.Errorf("route %q not found", id)
		}
		target := c.Routes[i]
		var refs []string
		for _, e := range c.Entrypoints {
			if sameRouteRef(e.Route, target) {
				refs = append(refs, entityLabel(e.Name, e.ID))
			}
		}
		if len(refs) > 0 {
			return fmt.Errorf("route %s: still referenced by entrypoints %s; update or delete them first",
				entityLabel(target.Name, target.ID), strings.Join(refs, ", "))
		}
		c.Routes = append(c.Routes[:i], c.Routes[i+1:]...)
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
		storedByName := make(map[string]string, len(c.Entrypoints))
		for _, e := range c.Entrypoints {
			if e.ID != "" {
				storedByName[strings.TrimSpace(e.Name)] = e.ID
			}
		}
		// A full-list replacement keeps the id of every entry that carries
		// one, keeps the stored identity of an id-less entry matched by
		// name, and mints one for a genuinely new entry. Order is
		// precedence and is never reordered here.
		for i := range entrypoints {
			id, err := adoptEntityID(identity.Entrypoint, entrypoints[i].ID, entrypoints[i].Name, storedByName)
			if err != nil {
				return err
			}
			entrypoints[i].ID = id
		}
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
	s.closeAllWS()
	if s.stats != nil {
		if err := s.stats.DeleteAllSessions(r.Context()); err != nil {
			s.log.Warnf("clear sessions: %v", err)
		}
	}
	w.WriteHeader(http.StatusNoContent)
}
