package gateway

import (
	"context"
	"strings"

	"github.com/go-gost/core/logger"
	xlogger "github.com/go-gost/x/logger"

	gwconfig "github.com/ginuerzh/cuttle/internal/config"
	"github.com/ginuerzh/cuttle/internal/provider"
)

// catalogLimits maps a provider ID to the output limit of each of its models.
// A missing entry means the model has no usable catalog output limit.
type catalogLimits map[string]map[string]int

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

	// Deduplicate both providers and models so one warning covers every node
	// that references the same missing model.
	wanted := make(map[string]map[string]bool)
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

		prov, err := provider.New(p)
		if err != nil {
			log.Warnf("catalog limits: provider %s: %v", providerID, err)
			continue
		}
		catalog, err := prov.Models(ctx)
		if err != nil {
			log.Warnf("catalog limits: provider %s: %v", providerID, err)
			continue
		}

		available := make(map[string]bool, len(catalog))
		for _, m := range catalog {
			modelID := strings.TrimSpace(m.ID)
			if modelID == "" {
				continue
			}
			available[modelID] = true
			if m.Output <= 0 {
				continue
			}
			if limits[providerID] == nil {
				limits[providerID] = make(map[string]int)
			}
			limits[providerID][modelID] = m.Output
		}
		for modelID := range models {
			if !available[modelID] {
				log.Warnf("catalog limits: provider %s: model %s not found", providerID, modelID)
			}
		}
	}

	for providerID, models := range wanted {
		for modelID := range models {
			log.Warnf("catalog limits: provider %s not configured for model %s", providerID, modelID)
		}
	}
	return limits
}
