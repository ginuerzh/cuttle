package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// modelsTimeout bounds model-list requests so a stalled provider cannot hold
// the admin catalog endpoint open indefinitely.
const modelsTimeout = 10 * time.Second

// Models fetches the model catalog from the adapter's primary listing
// endpoint. Generic providers list every configured API; the vendor adapters
// prefer chat and otherwise use the first configured API. A failed endpoint
// does not discard models returned by another one.
func (p *baseProvider) Models(ctx context.Context) ([]Model, error) {
	apis := p.listAPIs()
	if len(apis) == 0 {
		return nil, fmt.Errorf("provider %q: no API base URL configured", p.cfg.Name)
	}

	var (
		models     = make([]Model, 0)
		byID       = make(map[string]int)
		errs       []error
		fetched    int
		configured = p.configuredAPIs()
	)
	for _, api := range apis {
		base, ok := p.BaseURL(api)
		if !ok {
			continue
		}
		items, err := p.fetchModels(ctx, api, base)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", api, err))
			continue
		}
		fetched++

		for _, item := range items {
			m := Model{ID: item.ID, Name: item.Name}
			if classified := p.adapter.classify(item.ID); len(classified) > 0 {
				m.APIs = classified
			} else {
				m.APIs = []API{api}
			}
			m.APIs = intersectAPIs(normalizeAPIs(m.APIs), configured)

			if i, ok := byID[m.ID]; ok {
				models[i].APIs = mergeAPIs(models[i].APIs, m.APIs)
				if models[i].Name == "" {
					models[i].Name = m.Name
				}
				continue
			}
			byID[m.ID] = len(models)
			models = append(models, m)
		}
	}

	if fetched == 0 {
		if len(errs) == 0 {
			return nil, fmt.Errorf("provider %q: no models endpoint available", p.cfg.Name)
		}
		return nil, fmt.Errorf("provider %q: list models: %w", p.cfg.Name, errors.Join(errs...))
	}
	return models, nil
}

func (p *baseProvider) listAPIs() []API {
	if !p.adapter.listAll() {
		if _, ok := p.BaseURL(Chat); ok {
			return []API{Chat}
		}
		apis := p.configuredAPIs()
		if len(apis) > 0 {
			return apis[:1]
		}
		return nil
	}
	return p.configuredAPIs()
}

func (p *baseProvider) configuredAPIs() []API {
	out := make([]API, 0, len(apiOrder))
	for _, api := range apiOrder {
		if _, ok := p.BaseURL(api); ok {
			out = append(out, api)
		}
	}
	return out
}

type modelItem struct {
	ID   string
	Name string
}

func (p *baseProvider) fetchModels(ctx context.Context, api API, base string) ([]modelItem, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ModelsURL(base, api), nil)
	if err != nil {
		return nil, err
	}
	key := ""
	if len(p.cfg.Keys) > 0 {
		key = p.cfg.Keys[0]
	}
	applyAuth(req, p.Auth(api), key)
	ApplyAnthropicVersion(req, api)

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body := io.LimitReader(resp.Body, 8<<20)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, body)
		return nil, fmt.Errorf("unexpected status %s", resp.Status)
	}
	return decodeModels(body, api)
}

// ModelsURL returns the model-list URL for api at base. Anthropic bases are
// rooted at /v1 unless they already include it, so a base such as
// https://host/v1 does not produce a duplicated /v1/v1/models path.
func ModelsURL(base string, api API) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if api == Anthropic && !strings.HasSuffix(base, "/v1") {
		base += "/v1"
	}
	return base + "/models"
}

type openAIModelJSON struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
}

type geminiModelJSON struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
}

type modelListJSON struct {
	Data   []openAIModelJSON `json:"data"`
	Models []geminiModelJSON `json:"models"`
}

func decodeModels(r io.Reader, api API) ([]modelItem, error) {
	var payload modelListJSON
	if err := json.NewDecoder(r).Decode(&payload); err != nil {
		return nil, err
	}

	var out []modelItem
	if api == Gemini {
		for _, m := range payload.Models {
			id := strings.TrimPrefix(strings.TrimSpace(m.Name), "models/")
			if id == "" {
				continue
			}
			name := m.DisplayName
			if name == "" {
				name = id
			}
			out = append(out, modelItem{ID: id, Name: name})
		}
		return out, nil
	}

	for _, m := range payload.Data {
		id := strings.TrimSpace(m.ID)
		if id == "" {
			continue
		}
		name := m.Name
		if name == "" {
			name = m.DisplayName
		}
		if name == "" {
			name = id
		}
		out = append(out, modelItem{ID: id, Name: name})
	}
	return out, nil
}

func normalizeAPIs(apis []API) []API {
	seen := make(map[API]bool, len(apis))
	out := make([]API, 0, len(apis))
	for _, api := range apis {
		if api == "" || seen[api] {
			continue
		}
		seen[api] = true
		out = append(out, api)
	}
	return out
}

// intersectAPIs keeps apis in their original order while dropping entries the
// provider has no base URL for. Empty means the model is listed upstream but
// cannot be served by this provider configuration.
func intersectAPIs(apis, allowed []API) []API {
	keep := make(map[API]bool, len(allowed))
	for _, api := range allowed {
		keep[api] = true
	}
	out := make([]API, 0, len(apis))
	for _, api := range apis {
		if keep[api] {
			out = append(out, api)
		}
	}
	return out
}

func mergeAPIs(a, b []API) []API {
	merged := make([]API, 0, len(a)+len(b))
	merged = append(merged, a...)
	merged = append(merged, b...)
	return normalizeAPIs(merged)
}
