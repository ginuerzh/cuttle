package admin

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"time"

	"github.com/go-gost/llm-gw/internal/config"
	"github.com/go-gost/llm-gw/internal/provider"
)

// modelsCacheTTL bounds how long a provider model catalog is served from
// memory before the next request refreshes it.
const modelsCacheTTL = 10 * time.Minute

// errProviderNotFound is returned when a model catalog is requested for an
// unknown provider name.
var errProviderNotFound = errors.New("provider not found")

// providerModel is the JSON view of one provider catalog entry. The alias lets
// the admin package speak about provider models without duplicating the type.
type providerModel = provider.Model

// modelsCacheEntry is the per-provider model catalog cache. fetchedAt records
// the last successful fetch so a failed refresh can keep returning the
// last-good catalog with its original timestamp.
type modelsCacheEntry struct {
	models    []providerModel
	fetchedAt time.Time
	err       string
}

// modelsFlight coalesces concurrent catalog fetches for one provider. The
// caller that registers the flight performs the fetch; later callers wait for
// done and share its result. waiters counts the callers sharing the flight.
type modelsFlight struct {
	done      chan struct{}
	waiters   int
	models    []providerModel
	fetchedAt time.Time
	err       error
}

// providerModels returns the model catalog for the named provider. A fresh
// cache entry is served without touching the network; refresh forces a fetch.
// Concurrent fetches for the same provider share one upstream call. When a
// refresh fails after an earlier success, the last-good catalog is returned
// without a fatal error. Without a last-good catalog the fetch error is
// returned.
func (s *Server) providerModels(ctx context.Context, name string, refresh bool) ([]providerModel, time.Time, error) {
	s.mu.Lock()
	p, ok := s.providerLocked(name)
	if !ok {
		s.mu.Unlock()
		return nil, time.Time{}, errProviderNotFound
	}
	if !refresh {
		if entry, ok := s.freshModelsLocked(name); ok {
			s.mu.Unlock()
			return entry.models, entry.fetchedAt, nil
		}
	}
	if flight, ok := s.modelsInflight[name]; ok {
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
	s.modelsInflight[name] = flight
	s.mu.Unlock()

	return s.loadModels(ctx, p, name, flight)
}

// freshModelsLocked returns a non-expired cache entry. Callers must hold s.mu.
func (s *Server) freshModelsLocked(name string) (modelsCacheEntry, bool) {
	entry, ok := s.modelsCache[name]
	if !ok || entry.models == nil || time.Since(entry.fetchedAt) >= modelsCacheTTL {
		return modelsCacheEntry{}, false
	}
	return entry, true
}

// loadModels performs the upstream fetch for flight and publishes its result
// to waiters. The result is cached only while the provider configuration still
// matches the snapshot used for the fetch.
func (s *Server) loadModels(ctx context.Context, p config.Provider, name string, flight *modelsFlight) ([]providerModel, time.Time, error) {
	models, err := fetchModels(ctx, p)
	now := time.Now()

	s.mu.Lock()
	defer s.mu.Unlock()

	var (
		resultModels []providerModel
		resultAt     time.Time
		resultErr    error
	)

	if current, ok := s.providerLocked(name); !ok || !sameProviderFetch(current, p) {
		if err == nil {
			resultModels, resultAt = models, now
		} else {
			resultErr = err
		}
	} else {
		prev, hadPrev := s.modelsCache[name]
		switch {
		case err != nil && hadPrev && prev.models != nil:
			prev.err = err.Error()
			s.modelsCache[name] = prev
			resultModels, resultAt = prev.models, prev.fetchedAt
		case err != nil:
			s.modelsCache[name] = modelsCacheEntry{err: err.Error()}
			resultErr = err
		default:
			if models == nil {
				models = []providerModel{}
			}
			s.modelsCache[name] = modelsCacheEntry{models: models, fetchedAt: now}
			resultModels, resultAt = models, now
		}
	}

	flight.models = resultModels
	flight.fetchedAt = resultAt
	flight.err = resultErr
	delete(s.modelsInflight, name)
	close(flight.done)
	return resultModels, resultAt, resultErr
}

func (s *Server) providerLocked(name string) (config.Provider, bool) {
	for _, p := range s.cfg.Providers {
		if p.Name == name {
			return p, true
		}
	}
	return config.Provider{}, false
}

// invalidateModelsLocked drops cache entries whose provider was removed or
// whose upstream configuration changed. A change to Models alone keeps the
// cache: overrides are local policy and never affect the catalog. Callers must
// hold s.mu.
func (s *Server) invalidateModelsLocked(next *config.Config) {
	nextByName := make(map[string]config.Provider, len(next.Providers))
	for _, p := range next.Providers {
		nextByName[p.Name] = p
	}
	for _, p := range s.cfg.Providers {
		updated, ok := nextByName[p.Name]
		if !ok || !sameProviderFetch(p, updated) {
			delete(s.modelsCache, p.Name)
		}
	}
}

// sameProviderFetch reports whether two provider snapshots would fetch the
// same upstream catalog. Local model overrides are ignored so toggling a
// model's disabled flag reuses the cached catalog.
func sameProviderFetch(a, b config.Provider) bool {
	a.Models = nil
	b.Models = nil
	return reflect.DeepEqual(a, b)
}

// applyModelOverrides returns a copy of models with the provider's local
// overrides applied. The cached catalog is never modified.
func applyModelOverrides(models []providerModel, overrides map[string]config.ModelOverride) []providerModel {
	out := make([]providerModel, len(models))
	copy(out, models)
	if len(overrides) == 0 {
		return out
	}
	for i := range out {
		ov, ok := overrides[out[i].ID]
		if !ok {
			ov, ok = overrides[strings.TrimSpace(out[i].ID)]
		}
		if ok {
			out[i].Disabled = ov.Disabled
		}
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
