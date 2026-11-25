package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"sort"
	"strings"

	"github.com/go-gost/core/logger"
	xlogger "github.com/go-gost/x/logger"

	gwconfig "github.com/ginuerzh/cuttle/internal/config"
	"github.com/ginuerzh/cuttle/internal/provider"
)

// catalogModel is the catalog metadata the gateway carries for one model.
// A nil Efforts slice means the listing reported no effort metadata.
type catalogModel struct {
	Output  int
	Efforts []string
}

// catalogInfo maps a provider ID to the catalog metadata of each of its models.
// A missing entry means the listing reported neither an output limit nor effort
// metadata.
type catalogInfo map[string]map[string]catalogModel

type catalogRequest struct {
	id          string
	fingerprint string
	provider    gwconfig.Provider
	models      map[string]bool
}

// fetchCatalogInfo reads each referenced provider's catalog once and collects
// per-model output limits and effort tiers. Catalog failures are non-fatal:
// they are logged and leave the affected models without metadata, so a gateway
// build can still proceed.
func fetchCatalogInfo(ctx context.Context, cfg *gwconfig.Config, log logger.Logger) catalogInfo {
	info := make(catalogInfo)
	if cfg == nil {
		return info
	}
	if log == nil {
		log = xlogger.Nop()
	}

	requests, missing := catalogRequests(cfg)
	for _, request := range requests {
		got, _, missingModels, err := fetchCatalogRequest(ctx, request)
		logCatalogResult(request, missingModels, err, log)
		if err != nil {
			continue
		}
		mergeCatalogInfo(info, request.id, got)
	}
	warnMissingProviders(missing, log)
	return info
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
		requests = append(requests, catalogRequest{
			id:          providerID,
			fingerprint: catalogProviderFingerprint(p),
			provider:    p,
			models:      models,
		})
	}
	return requests, wanted
}

// catalogProviderFingerprint identifies the parts of a provider that can change
// its upstream catalog. The display name and local model overrides do not.
func catalogProviderFingerprint(p gwconfig.Provider) string {
	p.Name = ""
	p.Models = nil
	data, err := json.Marshal(p)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func fetchCatalogRequest(ctx context.Context, request catalogRequest) (map[string]catalogModel, map[string]bool, []string, error) {
	prov, err := provider.New(request.provider)
	if err != nil {
		return nil, nil, nil, err
	}
	catalog, err := prov.Models(ctx)
	if err != nil {
		return nil, nil, nil, err
	}

	info := make(map[string]catalogModel)
	known := make(map[string]bool, len(catalog))
	for _, m := range catalog {
		modelID := strings.TrimSpace(m.ID)
		if modelID == "" {
			continue
		}
		known[modelID] = true
		if m.Output <= 0 && m.Efforts == nil {
			continue
		}
		info[modelID] = catalogModel{Output: m.Output, Efforts: slices.Clone(m.Efforts)}
	}
	missing := make([]string, 0)
	for modelID := range request.models {
		if !known[modelID] {
			missing = append(missing, modelID)
		}
	}
	sort.Strings(missing)
	return info, known, missing, nil
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

func mergeCatalogInfo(dst catalogInfo, providerID string, src map[string]catalogModel) {
	if len(src) == 0 {
		return
	}
	if dst[providerID] == nil {
		dst[providerID] = make(map[string]catalogModel, len(src))
	}
	for modelID, model := range src {
		dst[providerID][modelID] = cloneCatalogModel(model)
	}
}

func cloneCatalogModel(model catalogModel) catalogModel {
	model.Efforts = slices.Clone(model.Efforts)
	return model
}

func cloneCatalogModels(models map[string]catalogModel) map[string]catalogModel {
	if len(models) == 0 {
		return nil
	}
	out := make(map[string]catalogModel, len(models))
	for modelID, model := range models {
		out[modelID] = cloneCatalogModel(model)
	}
	return out
}
