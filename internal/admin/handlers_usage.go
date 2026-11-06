package admin

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/go-gost/llm-gw/internal/config"
	"github.com/go-gost/llm-gw/internal/provider"
	"github.com/go-gost/llm-gw/internal/stats"
)

const usageCacheTTL = 60 * time.Second

// accountUsageView is one provider key as the browser sees it. The raw key is
// never included; KeyPreview is the existing admin mask.
type accountUsageView struct {
	KeyPreview string                 `json:"keyPreview,omitempty"`
	Balance    []provider.BalancePart `json:"balance,omitempty"`
	Windows    []provider.QuotaWindow `json:"windows,omitempty"`
	Error      string                 `json:"error,omitempty"`
	Stale      bool                   `json:"stale,omitempty"`
	CheckedAt  string                 `json:"checkedAt,omitempty"`
}

// providerUsageView is one provider card.
type providerUsageView struct {
	Name      string               `json:"name"`
	Type      string               `json:"type"`
	Enabled   bool                 `json:"enabled"`
	Supported bool                 `json:"supported"`
	Note      string               `json:"note,omitempty"`
	Local     *stats.ProviderUsage `json:"local,omitempty"`
	Accounts  []accountUsageView   `json:"accounts"`
	CheckedAt string               `json:"checkedAt,omitempty"`
	Cached    bool                 `json:"cached,omitempty"`
}

// providerUsageResponse is the payload of GET /api/provider-usage.
type providerUsageResponse struct {
	From       string               `json:"from"`
	To         string               `json:"to"`
	CheckedAt  string               `json:"checkedAt"`
	Totals     *stats.ProviderUsage `json:"totals,omitempty"`
	LocalError string               `json:"localError,omitempty"`
	Providers  []providerUsageView  `json:"providers"`
}

type usageCacheEntry struct {
	view      providerUsageView
	fetchedAt time.Time
}

func (s *Server) handleProviderUsage(w http.ResponseWriter, r *http.Request) {
	from, to := parseRange(r)
	refresh := r.URL.Query().Get("refresh") == "1"
	now := time.Now().UTC()

	s.mu.Lock()
	providers := append([]config.Provider(nil), s.cfg.Providers...)
	statsStore := s.stats
	s.usageMu.Lock()
	generation := s.usageGeneration
	s.usageMu.Unlock()
	s.mu.Unlock()

	localMap := map[string]stats.ProviderUsage{}
	var totals *stats.ProviderUsage
	localErr := ""
	if statsStore != nil {
		rows, err := statsStore.ProviderUsage(r.Context(), from, to)
		if err != nil {
			localErr = err.Error()
		} else {
			sum := stats.ProviderUsage{}
			for _, row := range rows {
				localMap[row.Provider] = row
				sum.Requests += row.Requests
				sum.Errors += row.Errors
				sum.InputTokens += row.InputTokens
				sum.OutputTokens += row.OutputTokens
				sum.CachedTokens += row.CachedTokens
			}
			totals = &sum
		}
	} else {
		localErr = "stats disabled"
	}

	views := make([]providerUsageView, len(providers))
	var wg sync.WaitGroup
	for i := range providers {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			p := providers[i]
			old, cached := s.cachedUsage(p.Name)
			var view providerUsageView
			if !refresh && cached {
				view = old
				view.Cached = true
			} else {
				view = s.buildProviderUsage(r.Context(), p, old)
				if r.Context().Err() == nil {
					s.storeUsage(p.Name, view, now, generation)
				}
			}
			if statsStore != nil {
				local := localMap[p.Name]
				local.Provider = p.Name
				view.Local = &local
			}
			views[i] = view
		}()
	}
	wg.Wait()

	writeJSON(w, http.StatusOK, providerUsageResponse{
		From:       from.UTC().Format(time.RFC3339),
		To:         to.UTC().Format(time.RFC3339),
		CheckedAt:  formatCheckedAt(now),
		Totals:     totals,
		LocalError: localErr,
		Providers:  views,
	})
}

func (s *Server) buildProviderUsage(ctx context.Context, cfg config.Provider, old providerUsageView) providerUsageView {
	now := time.Now().UTC()
	view := providerUsageView{
		Name:      cfg.Name,
		Type:      cfg.Type,
		Enabled:   cfg.EnabledValue(),
		Supported: true,
		Accounts:  []accountUsageView{},
	}

	if supported, note := provider.UsageSupport(cfg.Type); !supported {
		view.Supported = false
		view.Note = note
		return view
	}
	if !view.Enabled {
		view.Note = "provider is disabled"
		return view
	}
	if len(cfg.Keys) == 0 {
		view.Note = "no API key configured"
		return view
	}

	p, err := provider.New(cfg)
	if err != nil {
		view.Supported = false
		view.Note = err.Error()
		return view
	}
	up, ok := p.(provider.UsageProvider)
	if !ok {
		view.Supported = false
		view.Note = "provider does not support usage queries"
		return view
	}
	view.CheckedAt = formatCheckedAt(now)
	accounts, err := up.Usage(ctx)
	if err != nil {
		var ue *provider.UsageUnsupportedError
		if errors.As(err, &ue) {
			view.Supported = false
			view.Note = ue.Note
		} else {
			view.Note = err.Error()
		}
		return view
	}

	for _, acc := range accounts {
		keyPreview := ""
		if acc.KeyIndex >= 0 && acc.KeyIndex < len(cfg.Keys) {
			keyPreview = preview(cfg.Keys[acc.KeyIndex])
		}
		av := accountUsageView{
			KeyPreview: keyPreview,
			Balance:    acc.Balance,
			Windows:    acc.Windows,
			Error:      acc.Error,
			CheckedAt:  formatCheckedAt(acc.CheckedAt),
		}
		if acc.Error != "" {
			if oldAcc, ok := oldAccount(old, acc.KeyIndex); ok &&
				(len(oldAcc.Balance) > 0 || len(oldAcc.Windows) > 0) {
				av.Balance = oldAcc.Balance
				av.Windows = oldAcc.Windows
				av.CheckedAt = oldAcc.CheckedAt
				av.Stale = true
			}
		}
		view.Accounts = append(view.Accounts, av)
	}
	return view
}

func oldAccount(v providerUsageView, index int) (accountUsageView, bool) {
	if index < 0 || index >= len(v.Accounts) {
		return accountUsageView{}, false
	}
	return v.Accounts[index], true
}

func (s *Server) cachedUsage(name string) (providerUsageView, bool) {
	s.usageMu.Lock()
	defer s.usageMu.Unlock()
	e, ok := s.usageCache[name]
	if !ok || time.Since(e.fetchedAt) >= usageCacheTTL {
		return e.view, false
	}
	return e.view, true
}

func (s *Server) storeUsage(name string, view providerUsageView, now time.Time, generation uint64) {
	s.usageMu.Lock()
	defer s.usageMu.Unlock()
	if generation != s.usageGeneration {
		return
	}
	s.usageCache[name] = usageCacheEntry{view: view, fetchedAt: now}
}

func (s *Server) invalidateUsageLocked() {
	s.usageMu.Lock()
	defer s.usageMu.Unlock()
	s.usageGeneration++
	s.usageCache = make(map[string]usageCacheEntry)
}

func formatCheckedAt(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
