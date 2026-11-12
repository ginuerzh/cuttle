package admin

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/ginuerzh/cuttle/internal/config"
	"github.com/ginuerzh/cuttle/internal/provider"
	"github.com/ginuerzh/cuttle/internal/stats"
)

const usageCacheTTL = 60 * time.Second

// accountUsageView is one provider key as the browser sees it. The raw key is
// never included; KeyPreview is the existing admin mask. KeyID is the configured
// provider-key id, empty when an upstream account matches no configured key.
type accountUsageView struct {
	KeyID      string                 `json:"keyId,omitempty"`
	Name       string                 `json:"name,omitempty"`
	KeyPreview string                 `json:"keyPreview,omitempty"`
	Enabled    bool                   `json:"enabled"`
	Balance    []provider.BalancePart `json:"balance,omitempty"`
	Windows    []provider.QuotaWindow `json:"windows,omitempty"`
	Note       string                 `json:"note,omitempty"`
	Error      string                 `json:"error,omitempty"`
	Stale      bool                   `json:"stale,omitempty"`
	CheckedAt  string                 `json:"checkedAt,omitempty"`
}

// providerUsageView is one provider card.
type providerUsageView struct {
	ID        string               `json:"id"`
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
			old, cached := s.cachedUsage(p.ID)
			var view providerUsageView
			if !refresh && cached {
				view = old
				view.Cached = true
			} else {
				view = s.buildProviderUsage(r.Context(), p, old, now)
				if r.Context().Err() == nil {
					s.storeUsage(p.ID, view, now, generation)
				}
			}
			if statsStore != nil {
				local := localMap[p.ID]
				local.Provider = p.ID
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

// queryUsage reads one provider's accounts from upstream. A non-empty note means
// the provider could not be queried at all; unsupported reports that the reason
// is the provider itself rather than a transient failure. Accounts carry the
// configured key id so callers can store readings and join history.
func queryUsage(ctx context.Context, cfg config.Provider) (accounts []accountUsageView, note string, unsupported bool) {
	p, err := provider.New(cfg)
	if err != nil {
		return nil, err.Error(), true
	}
	up, ok := p.(provider.UsageProvider)
	if !ok {
		return nil, "provider does not support usage queries", true
	}
	list, err := up.Usage(ctx)
	if err != nil {
		var ue *provider.UsageUnsupportedError
		if errors.As(err, &ue) {
			return nil, ue.Note, true
		}
		return nil, err.Error(), false
	}

	out := make([]accountUsageView, 0, len(list))
	for _, acc := range list {
		av := accountUsageView{
			Name:      acc.Name,
			Enabled:   true,
			Balance:   acc.Balance,
			Windows:   acc.Windows,
			Note:      acc.Note,
			Error:     acc.Error,
			CheckedAt: formatCheckedAt(acc.CheckedAt),
		}
		if acc.KeyIndex >= 0 && acc.KeyIndex < len(cfg.Keys) {
			key := cfg.Keys[acc.KeyIndex]
			av.KeyID = key.ID
			av.Name = key.Name
			av.KeyPreview = preview(key.Key)
			av.Enabled = key.EnabledValue()
		}
		out = append(out, av)
	}
	return out, "", false
}

// recordBalancePoints stores the readings of one provider's accounts. Only
// accounts with a configured key and no error contribute; a store failure is
// logged and never fails the page.
func (s *Server) recordBalancePoints(ctx context.Context, providerID string, accounts []accountUsageView, now time.Time) {
	if s.stats == nil || providerID == "" {
		return
	}
	var points []stats.BalancePoint
	for _, acc := range accounts {
		if acc.Error != "" || acc.KeyID == "" {
			continue
		}
		for _, b := range acc.Balance {
			points = append(points, stats.BalancePoint{
				Provider: providerID,
				Key:      acc.KeyID,
				Currency: b.Currency,
				At:       now,
				Amount:   b.Total,
			})
		}
	}
	if len(points) == 0 {
		return
	}
	if err := s.stats.RecordBalancePoints(ctx, points); err != nil {
		s.log.Warnf("stats: record balance readings for provider %s: %v", providerID, err)
	}
}

// buildProviderUsage assembles one provider card from a fresh upstream read. now
// is the request's instant, so every reading of one request shares a timestamp.
func (s *Server) buildProviderUsage(ctx context.Context, cfg config.Provider, old providerUsageView, now time.Time) providerUsageView {
	view := providerUsageView{
		ID:        cfg.ID,
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

	accounts, note, unsupported := queryUsage(ctx, cfg)
	// CheckedAt means "an upstream read ran": a provider-level unsupported reason
	// sets no timestamp, a transient read failure does.
	if note == "" || !unsupported {
		view.CheckedAt = formatCheckedAt(now)
	}
	if unsupported {
		view.Supported = false
	}
	view.Note = note
	if note != "" {
		view.Accounts = accounts
		return view
	}

	// Fresh readings only: the cache hit path and the stale fallback below never
	// reach here.
	s.recordBalancePoints(ctx, cfg.ID, accounts, now)

	for i := range accounts {
		acc := &accounts[i]
		if acc.Error == "" {
			continue
		}
		if oldAcc, ok := oldAccount(old, i); ok &&
			(len(oldAcc.Balance) > 0 || len(oldAcc.Windows) > 0 || oldAcc.Note != "") {
			acc.Balance = oldAcc.Balance
			acc.Windows = oldAcc.Windows
			acc.Note = oldAcc.Note
			acc.CheckedAt = oldAcc.CheckedAt
			acc.Stale = true
		}
	}
	view.Accounts = accounts
	return view
}

func oldAccount(v providerUsageView, index int) (accountUsageView, bool) {
	if index < 0 || index >= len(v.Accounts) {
		return accountUsageView{}, false
	}
	return v.Accounts[index], true
}

// cachedUsage returns the cached card for one provider id.
func (s *Server) cachedUsage(id string) (providerUsageView, bool) {
	s.usageMu.Lock()
	defer s.usageMu.Unlock()
	e, ok := s.usageCache[id]
	if !ok || time.Since(e.fetchedAt) >= usageCacheTTL {
		return e.view, false
	}
	return e.view, true
}

// storeUsage caches one provider card under its provider id.
func (s *Server) storeUsage(id string, view providerUsageView, now time.Time, generation uint64) {
	s.usageMu.Lock()
	defer s.usageMu.Unlock()
	if generation != s.usageGeneration {
		return
	}
	s.usageCache[id] = usageCacheEntry{view: view, fetchedAt: now}
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
