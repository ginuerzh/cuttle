package admin

import (
	"context"
	"time"

	"github.com/ginuerzh/cuttle/internal/config"
	"github.com/ginuerzh/cuttle/internal/provider"
)

// usageSampleIdle is how long the loop waits before re-reading the config while
// the sampler is off, so enabling it through SIGHUP needs no restart.
const usageSampleIdle = 30 * time.Second

// StartUsageSampler runs the balance reader in the background until ctx is done,
// like StartProbes: it returns immediately. The interval is re-read from the
// config every round, so a reload applies without a restart; 0 idles without
// calling any vendor.
func (s *Server) StartUsageSampler(ctx context.Context) {
	go func() {
		logged := time.Duration(-1)
		for {
			interval := s.usageSampleInterval()
			if interval != logged {
				if interval > 0 {
					s.log.Infof("usage: sampling provider balances every %s", interval)
				} else {
					s.log.Infof("usage: balance sampling is off; page visits still record")
				}
				logged = interval
			}
			wait := interval
			if wait <= 0 {
				wait = usageSampleIdle
			}
			timer := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
			if interval > 0 {
				s.SampleUsageOnce(ctx, time.Now())
			}
		}
	}()
}

// usageSampleInterval is the configured sampling interval; 0 means off.
func (s *Server) usageSampleInterval() time.Duration {
	s.mu.Lock()
	cfg := s.cfg
	s.mu.Unlock()
	if cfg == nil || cfg.Usage.SampleInterval == nil {
		return 0
	}
	if d := time.Duration(*cfg.Usage.SampleInterval); d > 0 {
		return d
	}
	return 0
}

// SampleUsageOnce reads every enabled, supported provider once and stores the
// readings. It is sequential: only the loop calls it, so passes never overlap.
func (s *Server) SampleUsageOnce(ctx context.Context, now time.Time) {
	if s.stats == nil {
		return
	}
	s.mu.Lock()
	var providers []config.Provider
	if s.cfg != nil {
		providers = append(providers, s.cfg.Providers...)
	}
	s.mu.Unlock()

	for _, p := range providers {
		if !p.EnabledValue() {
			continue
		}
		if supported, _ := provider.UsageSupport(p.Type); !supported {
			continue
		}
		// A disabled key never funds a request, so the sampler must not keep
		// sending it upstream. Filtering also keeps KeyIndex mapping inside the
		// filtered slice. The page keeps listing disabled keys on purpose.
		keys := make([]config.ProviderKey, 0, len(p.Keys))
		for _, k := range p.Keys {
			if k.EnabledValue() {
				keys = append(keys, k)
			}
		}
		if len(keys) == 0 {
			continue
		}
		p.Keys = keys
		accounts, _, _ := queryUsage(ctx, p)
		s.recordBalancePoints(ctx, p.ID, accounts, now)
	}
}
