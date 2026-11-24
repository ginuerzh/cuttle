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
	catalogLimitsCacheTTL       = 10 * time.Minute
	catalogLimitsRetryInterval  = time.Minute
	catalogLimitsRefreshTimeout = 5 * time.Second
)

type catalogLimitsEntry struct {
	limits      map[string]int
	fetchedAt   time.Time
	lastAttempt time.Time
}

// catalogLimitsCache owns the last good output limits for each provider. Reads
// are safe for concurrent use. Limits may schedule background refreshes, so
// callers must Close the cache when the gateway shuts down.
type catalogLimitsCache struct {
	mu       sync.Mutex
	entries  map[string]catalogLimitsEntry
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

func newCatalogLimitsCache() *catalogLimitsCache {
	ctx, cancel := context.WithCancel(context.Background())
	return &catalogLimitsCache{
		entries:       make(map[string]catalogLimitsEntry),
		inflight:      make(map[string]struct{}),
		ctx:           ctx,
		cancel:        cancel,
		now:           time.Now,
		ttl:           catalogLimitsCacheTTL,
		retryInterval: catalogLimitsRetryInterval,
		timeout:       catalogLimitsRefreshTimeout,
	}
}

// Prime synchronously fills the cache before the first gateway build. The
// caller's context bounds the whole prime, matching startup's previous catalog
// lookup budget.
func (c *catalogLimitsCache) Prime(ctx context.Context, cfg *gwconfig.Config, log logger.Logger) {
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
		limits, missingModels, err := fetchCatalogRequest(ctx, request)
		now := c.now()
		c.mu.Lock()
		entry := c.entries[request.id]
		if err == nil {
			entry.limits = cloneCatalogLimits(limits)
			entry.fetchedAt = now
		}
		entry.lastAttempt = now
		c.entries[request.id] = entry
		c.mu.Unlock()
		logCatalogResult(request, missingModels, err, log)
	}
	warnMissingProviders(missing, log)
}

// Limits returns a deep copy of the cached limits without waiting for network
// I/O. Missing, stale, or retryable providers are refreshed in the background;
// a failed refresh keeps the previous good limits.
func (c *catalogLimitsCache) Limits(cfg *gwconfig.Config, log logger.Logger) catalogLimits {
	out := make(catalogLimits)
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
		mergeCatalogLimits(out, request.id, entry.limits)
		if !c.shouldRefresh(entry, ok, now) {
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
func (c *catalogLimitsCache) Close() {
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

func (c *catalogLimitsCache) shouldRefresh(entry catalogLimitsEntry, ok bool, now time.Time) bool {
	if ok && now.Sub(entry.fetchedAt) < c.ttl {
		return false
	}
	return entry.lastAttempt.IsZero() || now.Sub(entry.lastAttempt) >= c.retryInterval
}

func (c *catalogLimitsCache) refresh(request catalogRequest, log logger.Logger) {
	defer c.wg.Done()
	ctx, cancel := context.WithTimeout(c.ctx, c.timeout)
	defer cancel()

	limits, missingModels, err := fetchCatalogRequest(ctx, request)
	now := c.now()
	c.mu.Lock()
	entry := c.entries[request.id]
	if err == nil {
		entry.limits = cloneCatalogLimits(limits)
		entry.fetchedAt = now
	}
	entry.lastAttempt = now
	c.entries[request.id] = entry
	delete(c.inflight, request.id)
	c.mu.Unlock()

	if err != nil && errors.Is(err, context.Canceled) {
		return
	}
	logCatalogResult(request, missingModels, err, log)
}

func cloneCatalogLimits(limits map[string]int) map[string]int {
	if len(limits) == 0 {
		return nil
	}
	out := make(map[string]int, len(limits))
	for modelID, limit := range limits {
		out[modelID] = limit
	}
	return out
}
