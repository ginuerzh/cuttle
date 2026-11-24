package gateway

import (
	"context"
	"sort"
	"strings"

	"github.com/go-gost/core/logger"
	xlogger "github.com/go-gost/x/logger"

	gwconfig "github.com/ginuerzh/cuttle/internal/config"
	"github.com/ginuerzh/cuttle/internal/provider"
)

// catalogLimits maps a provider ID to the output limit of each of its models.
// A missing entry means the model has no usable catalog output limit.
type catalogLimits map[string]map[string]int

type catalogRequest struct {
	id       string
	provider gwconfig.Provider
	models   map[string]bool
}

// fetchCatalogLimits reads each referenced provider's catalog once and collects
// positive output limits. Catalog failures are non-fatal: they are logged and
// leave the affected models without a cap, so a gateway build can still proceed.
func fetchCatalogLimits(ctx context.Context, cfg *gwconfig.Config, log logger.Logger) catalogLimits {
	limits := make(catalogLimits)
	if cfg == nil {
		return limits
	}
	if log == nil {
		log = xlogger.Nop()
	}

	requests, missing := catalogRequests(cfg)
	for _, request := range requests {
		got, missingModels, err := fetchCatalogRequest(ctx, request)
		logCatalogResult(request, missingModels, err, log)
		if err != nil {
			continue
		}
		mergeCatalogLimits(limits, request.id, got)
	}
	warnMissingProviders(missing, log)
	return limits
}

// catalogRequests selects enabled providers referenced by enabled nodes. The
// second result contains references that do not resolve to an enabled provider.
func catalogRequests(cfg *gwconfig.Config) ([]catalogRequest, map[string]map[string]bool) {
	wanted := make(map[string]map[string]bool)
	if cfg != nil {
		for _, n := range cfg.Nodes {
			if !n.EnabledValue() {
				continue
			}
			providerID := strings.TrimSpace(n.Provider)
			modelID := strings.TrimSpace(n.Model)
			if providerID == "" || modelID == "" {
				continue
			}
			if wanted[providerID] == nil {
				wanted[providerID] = make(map[string]bool)
			}
			wanted[providerID][modelID] = true
		}
	}

	if cfg == nil {
		return nil, wanted
	}
	requests := make([]catalogRequest, 0, len(cfg.Providers))
	for _, p := range cfg.Providers {
		if !p.EnabledValue() {
			continue
		}
		providerID := strings.TrimSpace(p.ID)
		models, ok := wanted[providerID]
		if !ok {
			continue
		}
		delete(wanted, providerID)
		requests = append(requests, catalogRequest{id: providerID, provider: p, models: models})
	}
	return requests, wanted
}

func fetchCatalogRequest(ctx context.Context, request catalogRequest) (map[string]int, []string, error) {
	prov, err := provider.New(request.provider)
	if err != nil {
		return nil, nil, err
	}
	catalog, err := prov.Models(ctx)
	if err != nil {
		return nil, nil, err
	}

	limits := make(map[string]int)
	available := make(map[string]bool, len(catalog))
	for _, m := range catalog {
		modelID := strings.TrimSpace(m.ID)
		if modelID == "" {
			continue
		}
		available[modelID] = true
		if m.Output > 0 {
			limits[modelID] = m.Output
		}
	}
	missing := make([]string, 0)
	for modelID := range request.models {
		if !available[modelID] {
			missing = append(missing, modelID)
		}
	}
	sort.Strings(missing)
	return limits, missing, nil
}

func logCatalogResult(request catalogRequest, missing []string, err error, log logger.Logger) {
	if err != nil {
		log.Warnf("catalog limits: provider %s: %v", request.id, err)
		return
	}
	for _, modelID := range missing {
		log.Warnf("catalog limits: provider %s: model %s not found", request.id, modelID)
	}
}

func warnMissingProviders(missing map[string]map[string]bool, log logger.Logger) {
	for providerID, models := range missing {
		for modelID := range models {
			log.Warnf("catalog limits: provider %s not configured for model %s", providerID, modelID)
		}
	}
}

func mergeCatalogLimits(dst catalogLimits, providerID string, src map[string]int) {
	if len(src) == 0 {
		return
	}
	if dst[providerID] == nil {
		dst[providerID] = make(map[string]int, len(src))
	}
	for modelID, limit := range src {
		dst[providerID][modelID] = limit
	}
}
