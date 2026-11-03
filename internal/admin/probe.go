package admin

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-gost/llm-gw/internal/config"
	"github.com/go-gost/llm-gw/internal/stats"
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

// ProbeOnce probes every enabled provider once and records the result.
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
		s.probes[p.Name] = res
		s.mu.Unlock()

		if s.stats != nil {
			_ = s.stats.InsertProbe(ctx, stats.ProbeResult{
				TS:        time.Now(),
				Provider:  p.Name,
				OK:        res.OK,
				Status:    res.Status,
				LatencyMS: res.LatencyMS,
				Error:     res.Error,
			})
		}
	}
}

func probeHTTP(ctx context.Context, p config.Provider) probeResult {
	path := p.Health.Path
	if path == "" {
		path = "/"
	}
	timeout := time.Duration(p.Health.Timeout)
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(p.BaseURL, "/")+path, nil)
	if err != nil {
		return probeResult{Error: err.Error()}
	}
	if len(p.Keys) > 0 {
		applyAuth(req, p, p.Keys[0])
	}

	start := time.Now()
	resp, err := (&http.Client{Timeout: timeout}).Do(req)
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
