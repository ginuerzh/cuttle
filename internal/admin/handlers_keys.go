package admin

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/ginuerzh/cuttle/internal/config"
	"github.com/ginuerzh/cuttle/internal/identity"
	"github.com/ginuerzh/cuttle/internal/stats"
)

const (
	gatewayKeyPrefix   = "sk-gw-"
	gatewayKeyAttempts = 8
)

// errGatewayKeyGeneration reports that key generation could not produce a
// value unique across the configured keys after bounded retries.
var errGatewayKeyGeneration = errors.New("could not generate a unique gateway key")

// gatewayKeyView is one configured key as the browser sees it. The raw value is
// never included; KeyPreview is the existing admin mask. The id is the handle,
// the name is a display label.
type gatewayKeyView struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	KeyPreview string `json:"keyPreview"`
	Enabled    bool   `json:"enabled"`
}

// gatewayKeySecret is the one-time create/rotate response. It is the only
// admin response that carries a raw gateway key.
type gatewayKeySecret struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Key  string `json:"key"`
}

func gwKeyEnabled(k config.GatewayKey) bool {
	return k.Enabled == nil || *k.Enabled
}

// generateGatewayKey returns a new 256-bit secret with the gateway prefix.
func generateGatewayKey() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return gatewayKeyPrefix + base64.RawURLEncoding.EncodeToString(b[:]), nil
}

// newGatewayKey generates a key that does not collide with any configured key.
// The entry with the skipID is ignored so rotation can exclude its current
// value.
func newGatewayKey(existing []config.GatewayKey, skipID string) (string, error) {
	for range gatewayKeyAttempts {
		key, err := generateGatewayKey()
		if err != nil {
			return "", fmt.Errorf("%w: %v", errGatewayKeyGeneration, err)
		}
		collision := false
		for _, k := range existing {
			if k.ID == skipID {
				continue
			}
			if k.Key == key {
				collision = true
				break
			}
		}
		if !collision {
			return key, nil
		}
	}
	return "", errGatewayKeyGeneration
}

func (s *Server) handleListGatewayKeys(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]gatewayKeyView, 0, len(s.cfg.Gateway.Keys))
	for _, k := range s.cfg.Gateway.Keys {
		out = append(out, gatewayKeyView{
			ID:         k.ID,
			Name:       k.Name,
			KeyPreview: preview(k.Key),
			Enabled:    gwKeyEnabled(k),
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleCreateGatewayKey(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name    string `json:"name"`
		Enabled *bool  `json:"enabled"`
	}
	if err := decode(w, r, &in); err != nil {
		return
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}

	// The server owns the id: a client-supplied one is ignored.
	id, err := identity.New(identity.GatewayKey)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	var secret string
	err = s.mutate(func(c *config.Config) error {
		for _, existing := range c.Gateway.Keys {
			if existing.Name == name {
				return fmt.Errorf("gateway key %q already exists", name)
			}
		}
		key, err := newGatewayKey(c.Gateway.Keys, "")
		if err != nil {
			return err
		}
		secret = key
		c.Gateway.Keys = append(c.Gateway.Keys, config.GatewayKey{
			ID:      id,
			Name:    name,
			Key:     key,
			Enabled: in.Enabled,
		})
		return nil
	})
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, errGatewayKeyGeneration) {
			status = http.StatusInternalServerError
		}
		writeError(w, status, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, gatewayKeySecret{ID: id, Name: name, Key: secret})
}

func (s *Server) handleUpdateGatewayKey(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var in struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Enabled *bool  `json:"enabled"`
	}
	if err := decode(w, r, &in); err != nil {
		return
	}
	if in.ID != "" && in.ID != id {
		writeError(w, http.StatusBadRequest, "id in body does not match the path")
		return
	}
	name := strings.TrimSpace(in.Name)
	if name == "" && in.Enabled == nil {
		writeError(w, http.StatusBadRequest, "name or enabled is required")
		return
	}

	err := s.mutate(func(c *config.Config) error {
		for i := range c.Gateway.Keys {
			if c.Gateway.Keys[i].ID != id {
				continue
			}
			// The name is a display label: an omitted one keeps the stored
			// label instead of blanking it.
			if name != "" {
				c.Gateway.Keys[i].Name = name
			}
			if in.Enabled != nil {
				c.Gateway.Keys[i].Enabled = in.Enabled
			}
			return nil
		}
		return fmt.Errorf("gateway key %q not found", id)
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleRotateGatewayKey(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	var secret, name string
	err := s.mutate(func(c *config.Config) error {
		for i := range c.Gateway.Keys {
			if c.Gateway.Keys[i].ID != id {
				continue
			}
			key, err := newGatewayKey(c.Gateway.Keys, id)
			if err != nil {
				return err
			}
			secret = key
			name = c.Gateway.Keys[i].Name
			c.Gateway.Keys[i].Key = key
			return nil
		}
		return fmt.Errorf("gateway key %q not found", id)
	})
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, errGatewayKeyGeneration) {
			status = http.StatusInternalServerError
		}
		writeError(w, status, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, gatewayKeySecret{ID: id, Name: name, Key: secret})
}

func (s *Server) handleDeleteGatewayKey(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	err := s.mutate(func(c *config.Config) error {
		out := c.Gateway.Keys[:0]
		found := false
		for _, k := range c.Gateway.Keys {
			if k.ID == id {
				found = true
				continue
			}
			out = append(out, k)
		}
		if !found {
			return fmt.Errorf("gateway key %q not found", id)
		}
		c.Gateway.Keys = out
		return nil
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type gatewayKeyUsageCounts struct {
	Requests     int64 `json:"requests"`
	Errors       int64 `json:"errors"`
	InputTokens  int64 `json:"inputTokens"`
	OutputTokens int64 `json:"outputTokens"`
	CachedTokens int64 `json:"cachedTokens"`
}

type gatewayKeyUsageRow struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Configured   bool   `json:"configured"`
	Enabled      bool   `json:"enabled"`
	KeyPreview   string `json:"keyPreview,omitempty"`
	Requests     int64  `json:"requests"`
	Errors       int64  `json:"errors"`
	InputTokens  int64  `json:"inputTokens"`
	OutputTokens int64  `json:"outputTokens"`
	CachedTokens int64  `json:"cachedTokens"`
}

// gatewayKeyUsageResponse is the payload of GET /api/gateway-key-usage.
type gatewayKeyUsageResponse struct {
	From   string                `json:"from"`
	To     string                `json:"to"`
	Totals gatewayKeyUsageCounts `json:"totals"`
	Keys   []gatewayKeyUsageRow  `json:"keys"`
}

// handleGatewayKeyUsage merges per-key rollups with the current config: keys
// deleted from the config still report their history as unconfigured rows. The
// rollups and the rows are joined by gateway key id; a deleted key is labelled
// with the last name the store kept for it.
func (s *Server) handleGatewayKeyUsage(w http.ResponseWriter, r *http.Request) {
	if s.stats == nil {
		writeError(w, http.StatusServiceUnavailable, "stats disabled")
		return
	}
	from, to := parseRange(r)

	s.mu.Lock()
	keys := append([]config.GatewayKey(nil), s.cfg.Gateway.Keys...)
	s.mu.Unlock()

	usage, err := s.stats.KeyUsage(r.Context(), from, to)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	byKey := make(map[string]stats.KeyUsage, len(usage))
	for _, u := range usage {
		byKey[u.Key] = u
	}
	labels := s.newLabelSet(r.Context())

	rows := make([]gatewayKeyUsageRow, 0, len(keys)+len(byKey))
	totals := gatewayKeyUsageCounts{}
	add := func(id string, configured, enabled bool, key string) {
		u := byKey[id]
		row := gatewayKeyUsageRow{
			ID:           id,
			Name:         labels.gatewayKey(id),
			Configured:   configured,
			Enabled:      enabled,
			Requests:     u.Requests,
			Errors:       u.Errors,
			InputTokens:  u.InputTokens,
			OutputTokens: u.OutputTokens,
			CachedTokens: u.CachedTokens,
		}
		if configured {
			row.KeyPreview = preview(key)
		}
		rows = append(rows, row)
		totals.Requests += u.Requests
		totals.Errors += u.Errors
		totals.InputTokens += u.InputTokens
		totals.OutputTokens += u.OutputTokens
		totals.CachedTokens += u.CachedTokens
	}

	seen := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		seen[k.ID] = struct{}{}
		add(k.ID, true, gwKeyEnabled(k), k.Key)
	}
	historical := make([]string, 0, len(byKey))
	for id := range byKey {
		if _, ok := seen[id]; !ok {
			historical = append(historical, id)
		}
	}
	// Deleted keys follow the configured ones, alphabetically by the label
	// they last carried.
	sort.Slice(historical, func(i, j int) bool {
		a, b := labels.gatewayKey(historical[i]), labels.gatewayKey(historical[j])
		if a != b {
			return a < b
		}
		return historical[i] < historical[j]
	})
	for _, id := range historical {
		add(id, false, false, "")
	}

	writeJSON(w, http.StatusOK, gatewayKeyUsageResponse{
		From:   from.UTC().Format(time.RFC3339),
		To:     to.UTC().Format(time.RFC3339),
		Totals: totals,
		Keys:   rows,
	})
}
