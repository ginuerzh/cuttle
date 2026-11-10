package admin

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/ginuerzh/cuttle/internal/config"
	"github.com/ginuerzh/cuttle/internal/provider"
)

// modelsCacheTTL bounds how long a provider model catalog is served from
// memory before the next request refreshes it.
const modelsCacheTTL = 10 * time.Minute

// errProviderNotFound is returned when a model catalog is requested for an
// unknown provider.
var errProviderNotFound = errors.New("provider not found")

// providerModel is the JSON view of one provider catalog entry. The alias lets
// the admin package speak about provider models without duplicating the type.
type providerModel = provider.Model

// modelsCacheEntry is the per-provider model catalog cache. fetchedAt records
// the last successful fetch so a failed refresh can keep returning the
// last-good catalog with its original timestamp. The cache is keyed by
// provider id.
type modelsCacheEntry struct {
	models    []providerModel
	fetchedAt time.Time
	err       string
}

// modelsFlight coalesces concurrent catalog fetches for one provider. The
// caller that registers the flight performs the fetch; later callers wait for
// done and share its result. waiters counts the callers sharing the flight.
// Flights are keyed by provider id.
type modelsFlight struct {
	done      chan struct{}
	waiters   int
	models    []providerModel
	fetchedAt time.Time
	err       error
}

// providerModels returns the model catalog for the provider a path value refers
// to: its id, or its display name until Task 7 removes the transition fallback.
// A fresh cache entry is served without touching the network; refresh forces a
// fetch. Concurrent fetches for the same provider share one upstream call. When
// a refresh fails after an earlier success, the last-good catalog is returned
// without a fatal error. Without a last-good catalog the fetch error is
// returned.
//
// The cache is keyed by the provider id, so a rename keeps the catalog.
func (s *Server) providerModels(ctx context.Context, ref string, refresh bool) ([]providerModel, time.Time, error) {
	s.mu.Lock()
	p, ok := s.providerLocked(ref)
	if !ok {
		s.mu.Unlock()
		return nil, time.Time{}, errProviderNotFound
	}
	id := p.ID
	if !refresh {
		if entry, ok := s.freshModelsLocked(id); ok {
			s.mu.Unlock()
			return entry.models, entry.fetchedAt, nil
		}
	}
	if flight, ok := s.modelsInflight[id]; ok {
		flight.waiters++
		s.mu.Unlock()

		select {
		case <-flight.done:
			return flight.models, flight.fetchedAt, flight.err
		case <-ctx.Done():
			return nil, time.Time{}, ctx.Err()
		}
	}
	flight := &modelsFlight{done: make(chan struct{})}
	s.modelsInflight[id] = flight
	s.mu.Unlock()

	return s.loadModels(ctx, p, id, flight)
}

// freshModelsLocked returns a non-expired cache entry. Callers must hold s.mu.
func (s *Server) freshModelsLocked(id string) (modelsCacheEntry, bool) {
	entry, ok := s.modelsCache[id]
	if !ok || entry.models == nil || time.Since(entry.fetchedAt) >= modelsCacheTTL {
		return modelsCacheEntry{}, false
	}
	return entry, true
}

// loadModels performs the upstream fetch for flight and publishes its result
// to waiters. The result is cached only while the provider configuration still
// matches the snapshot used for the fetch.
func (s *Server) loadModels(ctx context.Context, p config.Provider, id string, flight *modelsFlight) ([]providerModel, time.Time, error) {
	models, err := fetchModels(ctx, p)
	now := time.Now()

	s.mu.Lock()
	defer s.mu.Unlock()

	var (
		resultModels []providerModel
		resultAt     time.Time
		resultErr    error
	)

	if current, ok := s.providerLocked(id); !ok || !sameProviderFetch(current, p) {
		if err == nil {
			resultModels, resultAt = models, now
		} else {
			resultErr = err
		}
	} else {
		prev, hadPrev := s.modelsCache[id]
		switch {
		case err != nil && hadPrev && prev.models != nil:
			prev.err = err.Error()
			s.modelsCache[id] = prev
			resultModels, resultAt = prev.models, prev.fetchedAt
		case err != nil:
			s.modelsCache[id] = modelsCacheEntry{err: err.Error()}
			resultErr = err
		default:
			if models == nil {
				models = []providerModel{}
			}
			s.modelsCache[id] = modelsCacheEntry{models: models, fetchedAt: now}
			resultModels, resultAt = models, now
		}
	}

	flight.models = resultModels
	flight.fetchedAt = resultAt
	flight.err = resultErr
	delete(s.modelsInflight, id)
	close(flight.done)
	return resultModels, resultAt, resultErr
}

// providerLocked returns the provider a read path value refers to. Reads still
// accept the display name until Task 7 removes the transition fallback. Callers
// must hold s.mu.
func (s *Server) providerLocked(ref string) (config.Provider, bool) {
	if p, ok := s.cfg.Provider(ref); ok {
		return *p, true
	}
	return config.Provider{}, false
}

// invalidateModelsLocked drops cache entries whose provider was removed or
// whose upstream configuration changed. A change to Models alone keeps the
// cache: overrides are local policy and never affect the catalog. Entries are
// keyed by provider id. Callers must hold s.mu.
func (s *Server) invalidateModelsLocked(next *config.Config) {
	nextByID := make(map[string]config.Provider, len(next.Providers))
	for _, p := range next.Providers {
		if p.ID != "" {
			nextByID[p.ID] = p
		}
	}
	for _, p := range s.cfg.Providers {
		updated, ok := nextByID[p.ID]
		if !ok || !sameProviderFetch(p, updated) {
			delete(s.modelsCache, p.ID)
		}
	}
}

// sameProviderFetch reports whether two snapshots of the same provider would
// fetch the same upstream catalog. Local model overrides are ignored so
// toggling a model's disabled flag reuses the cached catalog, and the name is
// ignored because it is a display label: a rename keeps the catalog.
func sameProviderFetch(a, b config.Provider) bool {
	a.Models = nil
	b.Models = nil
	a.Name = ""
	b.Name = ""
	return reflect.DeepEqual(a, b)
}

// applyModelOverrides returns a copy of models with the provider's local
// overrides applied. Disabled overrides for models the upstream catalog no
// longer lists are appended as synthetic entries, so the UI can still show and
// re-enable them and route warnings can detect the skip. The cached catalog is
// never modified.
func applyModelOverrides(models []providerModel, overrides map[string]config.ModelOverride) []providerModel {
	out := make([]providerModel, len(models))
	copy(out, models)
	if len(overrides) == 0 {
		return out
	}
	normalized := make(map[string]config.ModelOverride, len(overrides))
	for id, ov := range overrides {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		// Merge duplicate keys that normalize to the same model ID.
		if prev, ok := normalized[id]; ok {
			ov.Disabled = prev.Disabled || ov.Disabled
		}
		normalized[id] = ov
	}
	seen := make(map[string]struct{}, len(out)+len(normalized))
	for i := range out {
		id := strings.TrimSpace(out[i].ID)
		seen[id] = struct{}{}
		if ov, ok := normalized[id]; ok {
			out[i].Disabled = ov.Disabled
		}
	}
	var missing []string
	for id, ov := range normalized {
		if !ov.Disabled {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		missing = append(missing, id)
	}
	slices.Sort(missing)
	for _, id := range missing {
		out = append(out, providerModel{ID: id, Disabled: true})
	}
	return out
}

// formatFetchedAt renders a catalog timestamp for the response header.
func formatFetchedAt(at time.Time) string {
	if at.IsZero() {
		return ""
	}
	return at.UTC().Format(time.RFC3339)
}
