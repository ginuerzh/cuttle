package gateway

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/go-gost/core/logger"
	xlogger "github.com/go-gost/x/logger"

	gwconfig "github.com/ginuerzh/cuttle/internal/config"
)

const (
	catalogCacheTTL       = 10 * time.Minute
	catalogRetryInterval  = time.Minute
	catalogRefreshTimeout = 5 * time.Second
)

type catalogEntry struct {
	fingerprint string
	models      map[string]catalogModel
	known       map[string]bool
	attempted   map[string]bool
	fetchedAt   time.Time
	lastAttempt time.Time
}

// catalogCache owns the last good catalog metadata for each provider. Reads
// are safe for concurrent use. Catalog may schedule background refreshes, so
// callers must Close the cache when the gateway shuts down.
type catalogCache struct {
	mu       sync.Mutex
	entries  map[string]catalogEntry
	inflight map[string]struct{}
	closed   bool

	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	closeOnce sync.Once

	now           func() time.Time
	ttl           time.Duration
	retryInterval time.Duration
	timeout       time.Duration
}

func newCatalogCache() *catalogCache {
	ctx, cancel := context.WithCancel(context.Background())
	return &catalogCache{
		entries:       make(map[string]catalogEntry),
		inflight:      make(map[string]struct{}),
		ctx:           ctx,
		cancel:        cancel,
		now:           time.Now,
		ttl:           catalogCacheTTL,
		retryInterval: catalogRetryInterval,
		timeout:       catalogRefreshTimeout,
	}
}

// Prime synchronously fills the cache before the first gateway build. The
// caller's context bounds the whole prime, matching startup's previous catalog
// lookup budget.
func (c *catalogCache) Prime(ctx context.Context, cfg *gwconfig.Config, log logger.Logger) {
	if c == nil || cfg == nil {
		return
	}
	if log == nil {
		log = xlogger.Nop()
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	requests, missing := catalogRequests(cfg)
	for _, request := range requests {
		// A request never sent because an earlier provider consumed the shared
		// startup budget must not arm the retry gate for this provider.
		if ctx.Err() != nil {
			continue
		}
		info, known, missingModels, err := fetchCatalogRequest(ctx, request)
		now := c.now()
		c.mu.Lock()
		entry := c.entries[request.id]
		if err == nil {
			entry.models = cloneCatalogModels(info)
			entry.known = cloneCatalogKnown(known)
			entry.fetchedAt = now
		}
		entry.fingerprint = request.fingerprint
		entry.attempted = cloneCatalogKnown(request.models)
		entry.lastAttempt = now
		c.entries[request.id] = entry
		c.mu.Unlock()
		logCatalogResult(request, missingModels, err, log)
	}
	warnMissingProviders(missing, log)
}

// Catalog returns a deep copy of the cached metadata without waiting for
// network I/O. Missing, stale, or retryable providers are refreshed in the
// background; a failed refresh keeps the previous good metadata.
func (c *catalogCache) Catalog(cfg *gwconfig.Config, log logger.Logger) catalogInfo {
	out := make(catalogInfo)
	if c == nil || cfg == nil {
		return out
	}
	if log == nil {
		log = xlogger.Nop()
	}

	requests, missing := catalogRequests(cfg)
	now := c.now()
	launches := make([]catalogRequest, 0, len(requests))
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return out
	}
	for _, request := range requests {
		entry, ok := c.entries[request.id]
		if ok && entry.fingerprint != request.fingerprint {
			delete(c.entries, request.id)
			entry = catalogEntry{}
			ok = false
		}
		mergeCatalogInfo(out, request.id, entry.models)
		if !c.shouldRefresh(entry, ok, request, now) {
			continue
		}
		if _, busy := c.inflight[request.id]; busy {
			continue
		}
		c.inflight[request.id] = struct{}{}
		c.wg.Add(1)
		launches = append(launches, request)
	}
	c.mu.Unlock()

	warnMissingProviders(missing, log)
	for _, request := range launches {
		go c.refresh(request, log)
	}
	return out
}

// Close cancels outstanding refreshes and waits for their goroutines. It is
// idempotent.
func (c *catalogCache) Close() {
	if c == nil {
		return
	}
	c.closeOnce.Do(func() {
		c.mu.Lock()
		c.closed = true
		c.cancel()
		c.mu.Unlock()
		c.wg.Wait()
	})
}

func (c *catalogCache) shouldRefresh(entry catalogEntry, ok bool, request catalogRequest, now time.Time) bool {
	if ok && now.Sub(entry.fetchedAt) < c.ttl && catalogModelsKnown(entry.known, request.models) {
		return false
	}
	if catalogModelsAttempted(entry.attempted, request.models) {
		return entry.lastAttempt.IsZero() || now.Sub(entry.lastAttempt) >= c.retryInterval
	}
	return true
}

func catalogModelsAttempted(attempted map[string]bool, wanted map[string]bool) bool {
	for modelID := range wanted {
		if !attempted[modelID] {
			return false
		}
	}
	return true
}

// catalogModelsKnown reports whether every requested model appeared in the
// last successful catalog. A model absent from both this set and the cached
// metadata is new upstream and needs another fetch before it can be resolved.
func catalogModelsKnown(known map[string]bool, wanted map[string]bool) bool {
	for modelID := range wanted {
		if !known[modelID] {
			return false
		}
	}
	return true
}

func (c *catalogCache) refresh(request catalogRequest, log logger.Logger) {
	defer c.wg.Done()
	ctx, cancel := context.WithTimeout(c.ctx, c.timeout)
	defer cancel()

	info, known, missingModels, err := fetchCatalogRequest(ctx, request)
	now := c.now()
	c.mu.Lock()
	entry := c.entries[request.id]
	// A later configuration already owns this entry; its refresh will replace
	// the result of this older request.
	if entry.fingerprint != "" && entry.fingerprint != request.fingerprint {
		delete(c.inflight, request.id)
		c.mu.Unlock()
		return
	}
	if err == nil {
		entry.models = cloneCatalogModels(info)
		entry.known = cloneCatalogKnown(known)
		entry.fetchedAt = now
	}
	entry.fingerprint = request.fingerprint
	entry.lastAttempt = now
	c.entries[request.id] = entry
	delete(c.inflight, request.id)
	c.mu.Unlock()

	if err != nil && errors.Is(err, context.Canceled) {
		return
	}
	logCatalogResult(request, missingModels, err, log)
}

func cloneCatalogKnown(known map[string]bool) map[string]bool {
	if len(known) == 0 {
		return nil
	}
	out := make(map[string]bool, len(known))
	for modelID := range known {
		out[modelID] = true
	}
	return out
}
