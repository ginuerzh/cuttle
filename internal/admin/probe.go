package admin

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/ginuerzh/cuttle/internal/config"
	"github.com/ginuerzh/cuttle/internal/provider"
	"github.com/ginuerzh/cuttle/internal/stats"
)

// StartProbes runs the periodic provider health probe until ctx is done.
func (s *Server) StartProbes(ctx context.Context) {
	go func() {
		s.ProbeOnce(ctx)
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				s.ProbeOnce(ctx)
			}
		}
	}()
}

// ProbeOnce probes every enabled provider once and records the result. Results
// are keyed by provider id, in memory and in the stats store.
func (s *Server) ProbeOnce(ctx context.Context) {
	s.mu.Lock()
	providers := append([]config.Provider(nil), s.cfg.Providers...)
	s.mu.Unlock()

	for _, p := range providers {
		if !p.EnabledValue() {
			continue
		}
		res := probeHTTP(ctx, p)

		s.mu.Lock()
		s.probes[p.ID] = res
		s.mu.Unlock()

		if s.stats != nil {
			_ = s.stats.InsertProbe(ctx, stats.ProbeResult{
				TS:        time.Now(),
				Provider:  p.ID,
				OK:        res.OK,
				Status:    res.Status,
				LatencyMS: res.LatencyMS,
				Error:     res.Error,
			})
		}
	}
}

func probeHTTP(ctx context.Context, p config.Provider) probeResult {
	base, api, auth, keyless, ok := probeTarget(p)
	if !ok {
		return probeResult{Error: "no provider base URL configured"}
	}
	timeout := time.Duration(p.Health.Timeout)
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, probeURL(p, api, base), nil)
	if err != nil {
		return probeResult{Error: err.Error()}
	}
	key := ""
	if !keyless {
		key, _ = p.FirstEnabledKey()
	}
	applyAuthConfig(req, auth, key)
	provider.ApplyAnthropicVersion(req, api)

	start := time.Now()
	resp, err := provider.NewHTTPClient(p.TLS, timeout).Do(req)
	latency := time.Since(start).Milliseconds()
	if err != nil {
		return probeResult{LatencyMS: latency, Error: err.Error()}
	}
	defer resp.Body.Close()

	res := probeResult{
		Status:    resp.StatusCode,
		LatencyMS: latency,
		CheckedAt: time.Now().Format(time.RFC3339),
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		res.Error = fmt.Sprintf("status %d", resp.StatusCode)
		return res
	}
	res.OK = true
	return res
}

// probeTarget returns the base URL, API, auth and catalog mode used for a
// provider health check: health.api when set, otherwise the first configured
// API in canonical order.
func probeTarget(p config.Provider) (string, provider.API, config.Auth, bool, bool) {
	prov, err := provider.New(p)
	if err != nil {
		return "", "", config.Auth{}, false, false
	}
	if raw := strings.TrimSpace(p.Health.API); raw != "" {
		api := provider.API(strings.ToLower(raw))
		if base, ok := prov.BaseURL(api); ok {
			return base, api, prov.Auth(api), prov.CatalogKeyless(), true
		}
	}
	for _, api := range []provider.API{provider.Chat, provider.Responses, provider.Anthropic, provider.Gemini} {
		if base, ok := prov.BaseURL(api); ok {
			return base, api, prov.Auth(api), prov.CatalogKeyless(), true
		}
	}
	return "", "", config.Auth{}, false, false
}

// probeURL builds the health-check URL. A configured health.path is appended
// to the selected base; otherwise the model-list URL builder is used so the
// Anthropic /v1 rule (including bases already ending in /v1) stays consistent
// with the catalog endpoint.
func probeURL(p config.Provider, api provider.API, base string) string {
	if path := p.Health.Path; path != "" {
		return strings.TrimSuffix(base, "/") + path
	}
	return provider.ModelsURL(base, api)
}
