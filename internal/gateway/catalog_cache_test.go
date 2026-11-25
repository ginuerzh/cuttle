package gateway

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	xlogger "github.com/go-gost/x/logger"

	gwconfig "github.com/ginuerzh/cuttle/internal/config"
)

func catalogCacheConfig(providers ...gwconfig.Provider) *gwconfig.Config {
	cfg := &gwconfig.Config{Providers: providers}
	for i, p := range providers {
		cfg.Nodes = append(cfg.Nodes, gwconfig.Node{
			ID:       fmt.Sprintf("n_%d", i),
			Name:     p.Name,
			Provider: p.ID,
			Model:    "glm-5.3",
		})
	}
	return cfg
}

func waitForCatalogSignal(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

func TestCatalogCacheKeepsEffortsAfterRefreshFailure(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			fmt.Fprint(w, `{"data":[{"id":"glm-5.3","limit":{"output":131072},"effort":{"supported_levels":["low","high","xhigh"]}}]}`)
			return
		}
		http.Error(w, "down", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	cfg := catalogCacheConfig(gwconfig.Provider{ID: "p1", Name: "p1", Type: "generic", Chat: srv.URL})
	cache := newCatalogCache()
	t.Cleanup(cache.Close)
	cache.Prime(context.Background(), cfg, xlogger.Nop())
	cache.now = func() time.Time { return time.Now().Add(11 * time.Minute) }

	cache.Catalog(cfg, xlogger.Nop())
	cache.wg.Wait()
	got := cache.Catalog(cfg, xlogger.Nop())["p1"]["glm-5.3"]
	if got.Output != 131072 || !slices.Equal(got.Efforts, []string{"low", "high", "xhigh"}) {
		t.Fatalf("catalog after failed refresh = %+v, want output 131072 and efforts [low high xhigh]", got)
	}
	if gotCalls := calls.Load(); gotCalls != 2 {
		t.Fatalf("catalog calls after retry backoff = %d, want 2", gotCalls)
	}
}

func TestCatalogCacheCopiesEfforts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"data":[{"id":"glm-5.3","effort":{"supported_levels":["low","high"]}}]}`)
	}))
	t.Cleanup(srv.Close)

	cfg := catalogCacheConfig(gwconfig.Provider{ID: "p1", Name: "p1", Type: "generic", Chat: srv.URL})
	cache := newCatalogCache()
	t.Cleanup(cache.Close)
	cache.Prime(context.Background(), cfg, xlogger.Nop())

	first := cache.Catalog(cfg, xlogger.Nop())
	first["p1"]["glm-5.3"].Efforts[0] = "max"
	second := cache.Catalog(cfg, xlogger.Nop())
	if got := second["p1"]["glm-5.3"].Efforts; !slices.Equal(got, []string{"low", "high"}) {
		t.Fatalf("catalog efforts after caller mutation = %v, want [low high]", got)
	}
}

func TestCatalogCacheServesLastGoodWithoutWaiting(t *testing.T) {
	var calls atomic.Int32
	refreshStarted := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			fmt.Fprint(w, `{"data":[{"id":"glm-5.3","limit":{"output":131072}}]}`)
			return
		}
		close(refreshStarted)
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)

	cfg := catalogCacheConfig(gwconfig.Provider{ID: "p1", Name: "p1", Type: "generic", Chat: srv.URL})
	cache := newCatalogCache()
	t.Cleanup(cache.Close)
	cache.Prime(context.Background(), cfg, xlogger.Nop())
	cache.now = func() time.Time { return time.Now().Add(11 * time.Minute) }

	started := time.Now()
	got := cache.Catalog(cfg, xlogger.Nop())
	if elapsed := time.Since(started); elapsed > 100*time.Millisecond {
		t.Fatalf("Catalog() with blocked refresh took %s, want <= 100ms", elapsed)
	}
	if cap := got["p1"]["glm-5.3"].Output; cap != 131072 {
		t.Fatalf("Catalog() cap = %d, want 131072; catalog = %v", cap, got)
	}
	waitForCatalogSignal(t, refreshStarted, "background refresh")
}

func TestCatalogCacheKeepsLastGoodAfterRefreshFailure(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			fmt.Fprint(w, `{"data":[{"id":"glm-5.3","limit":{"output":131072}}]}`)
			return
		}
		http.Error(w, "down", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	cfg := catalogCacheConfig(gwconfig.Provider{ID: "p1", Name: "p1", Type: "generic", Chat: srv.URL})
	cache := newCatalogCache()
	t.Cleanup(cache.Close)
	cache.Prime(context.Background(), cfg, xlogger.Nop())
	cache.now = func() time.Time { return time.Now().Add(11 * time.Minute) }

	cache.Catalog(cfg, xlogger.Nop())
	cache.wg.Wait()
	got := cache.Catalog(cfg, xlogger.Nop())
	if cap := got["p1"]["glm-5.3"].Output; cap != 131072 {
		t.Fatalf("Catalog() cap after failed refresh = %d, want 131072; catalog = %v", cap, got)
	}
	if gotCalls := calls.Load(); gotCalls != 2 {
		t.Fatalf("catalog calls after retry backoff = %d, want 2", gotCalls)
	}
}

func TestCatalogCacheCoalescesRefreshes(t *testing.T) {
	var calls atomic.Int32
	started := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			started <- struct{}{}
		}
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)

	cfg := catalogCacheConfig(gwconfig.Provider{ID: "p1", Name: "p1", Type: "generic", Chat: srv.URL})
	cache := newCatalogCache()
	t.Cleanup(cache.Close)

	cache.Catalog(cfg, xlogger.Nop())
	cache.Catalog(cfg, xlogger.Nop())
	waitForCatalogSignal(t, started, "coalesced catalog refresh")
	if got := calls.Load(); got != 1 {
		t.Fatalf("catalog calls for two Catalog() calls = %d, want 1", got)
	}
}

func TestCatalogCacheRefreshesProvidersIndependently(t *testing.T) {
	blocked := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	t.Cleanup(blocked.Close)
	live := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"data":[{"id":"glm-5.3","limit":{"output":8192}}]}`)
	}))
	t.Cleanup(live.Close)

	cfg := catalogCacheConfig(
		gwconfig.Provider{ID: "p1", Name: "blocked", Type: "generic", Chat: blocked.URL},
		gwconfig.Provider{ID: "p2", Name: "live", Type: "generic", Chat: live.URL},
	)
	cache := newCatalogCache()
	t.Cleanup(cache.Close)
	cache.timeout = 50 * time.Millisecond

	cache.Catalog(cfg, xlogger.Nop())
	cache.wg.Wait()
	cache.mu.Lock()
	got := cache.entries["p2"].models["glm-5.3"].Output
	cache.mu.Unlock()
	if got != 8192 {
		t.Fatalf("independent provider cap = %d, want 8192", got)
	}
}

func TestCatalogCacheRefreshesForNewModel(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			fmt.Fprint(w, `{"data":[{"id":"glm-5.3","limit":{"output":131072}}]}`)
			return
		}
		fmt.Fprint(w, `{"data":[{"id":"glm-5.3","limit":{"output":131072}},{"id":"glm-7","limit":{"output":65536}}]}`)
	}))
	t.Cleanup(srv.Close)

	provider := gwconfig.Provider{ID: "p1", Name: "p1", Type: "generic", Chat: srv.URL}
	cache := newCatalogCache()
	t.Cleanup(cache.Close)
	cache.Prime(context.Background(), catalogCacheConfig(provider), xlogger.Nop())

	next := catalogCacheConfig(provider)
	next.Nodes[0].Model = "glm-7"
	if got := cache.Catalog(next, xlogger.Nop()); got["p1"]["glm-7"].Output != 0 {
		t.Fatalf("Catalog() before refresh cap = %d, want 0; catalog = %v", got["p1"]["glm-7"].Output, got)
	}
	cache.wg.Wait()
	got := cache.Catalog(next, xlogger.Nop())
	if cap := got["p1"]["glm-7"].Output; cap != 65536 {
		t.Fatalf("Catalog() cap for newly published model = %d, want 65536; catalog = %v", cap, got)
	}
	if gotCalls := calls.Load(); gotCalls != 2 {
		t.Fatalf("catalog calls for new model = %d, want 2", gotCalls)
	}
}

func TestCatalogCacheInvalidatesOnProviderChange(t *testing.T) {
	var callsA, callsB atomic.Int32
	oldProvider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callsA.Add(1)
		fmt.Fprint(w, `{"data":[{"id":"glm-5.3","limit":{"output":200000}}]}`)
	}))
	t.Cleanup(oldProvider.Close)
	newProvider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callsB.Add(1)
		fmt.Fprint(w, `{"data":[{"id":"glm-5.3","limit":{"output":8192}}]}`)
	}))
	t.Cleanup(newProvider.Close)

	cache := newCatalogCache()
	t.Cleanup(cache.Close)
	cache.Prime(context.Background(), catalogCacheConfig(
		gwconfig.Provider{ID: "p1", Name: "p1", Type: "generic", Chat: oldProvider.URL},
	), xlogger.Nop())

	next := catalogCacheConfig(
		gwconfig.Provider{ID: "p1", Name: "p1", Type: "generic", Chat: newProvider.URL},
	)
	if got := cache.Catalog(next, xlogger.Nop()); got["p1"]["glm-5.3"].Output != 0 {
		t.Fatalf("Catalog() after provider change cap = %d, want 0; catalog = %v", got["p1"]["glm-5.3"].Output, got)
	}
	cache.wg.Wait()
	got := cache.Catalog(next, xlogger.Nop())
	if cap := got["p1"]["glm-5.3"].Output; cap != 8192 {
		t.Fatalf("Catalog() cap after provider change = %d, want 8192; catalog = %v", cap, got)
	}
	if gotA, gotB := callsA.Load(), callsB.Load(); gotA != 1 || gotB != 1 {
		t.Fatalf("catalog calls old/new = %d/%d, want 1/1", gotA, gotB)
	}
}

func TestCatalogCachePrimeDoesNotGateUnqueriedProvider(t *testing.T) {
	blocked := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	t.Cleanup(blocked.Close)
	var liveCalls atomic.Int32
	live := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		liveCalls.Add(1)
		fmt.Fprint(w, `{"data":[{"id":"glm-5.3","limit":{"output":8192}}]}`)
	}))
	t.Cleanup(live.Close)

	cfg := catalogCacheConfig(
		gwconfig.Provider{ID: "p1", Name: "blocked", Type: "generic", Chat: blocked.URL},
		gwconfig.Provider{ID: "p2", Name: "live", Type: "generic", Chat: live.URL},
	)
	cache := newCatalogCache()
	t.Cleanup(cache.Close)
	cache.timeout = 50 * time.Millisecond
	cache.Prime(context.Background(), cfg, xlogger.Nop())

	if got := cache.Catalog(cfg, xlogger.Nop()); got["p2"]["glm-5.3"].Output != 0 {
		t.Fatalf("Catalog() immediately after prime cap = %d, want 0; catalog = %v", got["p2"]["glm-5.3"].Output, got)
	}
	cache.wg.Wait()
	got := cache.Catalog(cfg, xlogger.Nop())
	if cap := got["p2"]["glm-5.3"].Output; cap != 8192 {
		t.Fatalf("Catalog() cap for provider skipped by prime = %d, want 8192; catalog = %v", cap, got)
	}
	if gotCalls := liveCalls.Load(); gotCalls != 1 {
		t.Fatalf("live catalog calls after prime = %d, want 1", gotCalls)
	}
}

func TestCatalogCacheCloseCancelsRefresh(t *testing.T) {
	started := make(chan struct{})
	canceled := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
		close(canceled)
	}))
	t.Cleanup(srv.Close)

	cfg := catalogCacheConfig(gwconfig.Provider{ID: "p1", Name: "p1", Type: "generic", Chat: srv.URL})
	cache := newCatalogCache()
	t.Cleanup(cache.Close)
	cache.Catalog(cfg, xlogger.Nop())
	waitForCatalogSignal(t, started, "blocked catalog refresh")

	closed := make(chan struct{})
	go func() {
		cache.Close()
		close(closed)
	}()
	waitForCatalogSignal(t, closed, "cache close")
	waitForCatalogSignal(t, canceled, "catalog request cancellation")
}
