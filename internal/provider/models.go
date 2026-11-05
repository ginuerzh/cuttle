package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// modelsTimeout bounds model-list requests so a stalled provider cannot hold
// the admin catalog endpoint open indefinitely.
const modelsTimeout = 30 * time.Second

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
			m := Model{
				ID:      item.ID,
				Name:    item.Name,
				Context: item.Context,
				Input:   item.Input,
				Output:  item.Output,
				Inputs:  item.Inputs,
				Outputs: item.Outputs,
				Price:   item.Price,
			}
			if classified := p.adapter.classify(item.ID); len(classified) > 0 {
				m.APIs = classified
			} else {
				m.APIs = []API{api}
			}
			m.APIs = intersectAPIs(normalizeAPIs(m.APIs), configured)

			if i, ok := byID[m.ID]; ok {
				models[i].APIs = mergeAPIs(models[i].APIs, m.APIs)
				mergeModelMetadata(&models[i], m)
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
	ID      string
	Name    string
	Context int
	Input   int
	Output  int
	Inputs  []string
	Outputs []string
	Price   *Price
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
	if p.CatalogKeyless() {
		key = ""
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

// openAIModelJSON is one entry in an OpenAI-compatible model list. Optional
// metadata is kept raw so a malformed metadata block cannot fail the whole
// catalog.
type openAIModelJSON struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	DisplayName string          `json:"display_name"`
	Modalities  json.RawMessage `json:"modalities"`
	Limit       json.RawMessage `json:"limit"`
	Cost        json.RawMessage `json:"cost"`
}

type openAIModalitiesJSON struct {
	Input  []string `json:"input"`
	Output []string `json:"output"`
}

type openAILimitJSON struct {
	Context int `json:"context"`
	Input   int `json:"input"`
	Output  int `json:"output"`
}

type openAICostJSON struct {
	Text openAICostTextJSON `json:"text"`
}

type openAICostTextJSON struct {
	Input  openAICostItemJSON `json:"input"`
	Output openAICostItemJSON `json:"output"`
}

type openAICostItemJSON struct {
	Price lenientPrice `json:"price"`
}

// lenientPrice accepts a JSON number or numeric string. Any other value is
// ignored and leaves Valid false, so a bad price never fails a catalog.
type lenientPrice struct {
	Value float64
	Valid bool
}

func (p *lenientPrice) UnmarshalJSON(data []byte) error {
	p.Value, p.Valid = 0, false
	raw := strings.TrimSpace(string(data))
	if raw == "" || raw == "null" {
		return nil
	}
	if v, ok := parsePrice(raw); ok {
		p.Value, p.Valid = v, true
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return nil
	}
	if v, ok := parsePrice(s); ok {
		p.Value, p.Valid = v, true
	}
	return nil
}

// parsePrice rejects non-finite values because they cannot be marshaled back
// into the admin response.
func parsePrice(raw string) (float64, bool) {
	v, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	return v, true
}

// toItem converts one list entry into the catalog's internal model item.
// Malformed optional metadata is ignored rather than reported.
func (m openAIModelJSON) toItem() modelItem {
	item := modelItem{ID: strings.TrimSpace(m.ID)}
	item.Name = m.Name
	if item.Name == "" {
		item.Name = m.DisplayName
	}
	if item.Name == "" {
		item.Name = item.ID
	}

	var modalities openAIModalitiesJSON
	if len(m.Modalities) > 0 {
		_ = json.Unmarshal(m.Modalities, &modalities)
	}
	item.Inputs = normalizeModalities(modalities.Input)
	item.Outputs = normalizeModalities(modalities.Output)

	var limit openAILimitJSON
	if len(m.Limit) > 0 {
		_ = json.Unmarshal(m.Limit, &limit)
	}
	item.Context, item.Input, item.Output = limit.Context, limit.Input, limit.Output

	var cost openAICostJSON
	if len(m.Cost) > 0 {
		_ = json.Unmarshal(m.Cost, &cost)
	}
	in, out := cost.Text.Input.Price, cost.Text.Output.Price
	// A single reported zero side is treated as unreported: it cannot be
	// distinguished from a missing side. Both sides reported means a real
	// price, including the free 0/0 case; a lone non-zero side is real too.
	if (in.Valid && out.Valid) || (in.Valid && in.Value != 0) || (out.Valid && out.Value != 0) {
		item.Price = &Price{Input: in.Value, Output: out.Value}
	}
	return item
}

type geminiModelJSON struct {
	Name             string `json:"name"`
	DisplayName      string `json:"displayName"`
	InputTokenLimit  int    `json:"inputTokenLimit"`
	OutputTokenLimit int    `json:"outputTokenLimit"`
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
			out = append(out, modelItem{
				ID:      id,
				Name:    name,
				Context: m.InputTokenLimit,
				Output:  m.OutputTokenLimit,
			})
		}
		return out, nil
	}

	for _, m := range payload.Data {
		if strings.TrimSpace(m.ID) == "" {
			continue
		}
		out = append(out, m.toItem())
	}
	return out, nil
}

// normalizeModalities lowercases modality names, reduces MIME values to their
// allowed top-level type ("image / jpeg" -> "image"), maps application MIME
// types to the generic "file" modality, drops unknown types and empty entries,
// and removes duplicates in first-seen order.
func normalizeModalities(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if i := strings.IndexByte(value, '/'); i >= 0 {
			value = strings.TrimSpace(value[:i])
		}
		if value == "application" {
			value = "file"
		}
		switch value {
		case "text", "image", "audio", "video", "file":
		default:
			continue
		}
		if seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	if len(out) == 0 {
		return nil
	}
	return out
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

// mergeModelMetadata fills empty metadata on dst from src. The first API that
// reports a field wins, so a later listing cannot overwrite richer earlier
// data.
func mergeModelMetadata(dst *Model, src Model) {
	if dst.Name == "" {
		dst.Name = src.Name
	}
	if dst.Context == 0 {
		dst.Context = src.Context
	}
	if dst.Input == 0 {
		dst.Input = src.Input
	}
	if dst.Output == 0 {
		dst.Output = src.Output
	}
	if len(dst.Inputs) == 0 {
		dst.Inputs = src.Inputs
	}
	if len(dst.Outputs) == 0 {
		dst.Outputs = src.Outputs
	}
	if dst.Price == nil {
		dst.Price = src.Price
	}
}
