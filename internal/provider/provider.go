// Package provider exposes the capability model of an upstream LLM provider:
// which API endpoints it serves, which models it lists, and how requests are
// authenticated. It is deliberately independent of the gateway and admin
// layers.
package provider

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-gost/llm-gw/internal/config"
)

// API is an upstream wire API.
type API string

const (
	Chat      API = "chat"
	Responses API = "responses"
	Anthropic API = "anthropic"
	Gemini    API = "gemini"
)

// apiOrder is the canonical order used for listing and default selection.
var apiOrder = []API{Chat, Responses, Anthropic, Gemini}

// Price is the model's list price. It is reserved for a later usage/billing
// feature and is not populated by the v1 adapters.
type Price struct {
	Input  float64 `json:"input,omitempty"`
	Output float64 `json:"output,omitempty"`
}

// Model is one entry in a provider's model catalog.
type Model struct {
	ID        string   `json:"id"`
	Name      string   `json:"name,omitempty"`
	APIs      []API    `json:"apis,omitempty"`
	Context   int      `json:"context,omitempty"`
	Output    int      `json:"output,omitempty"`
	Inputs    []string `json:"inputs,omitempty"`
	Outputs   []string `json:"outputs,omitempty"`
	Reasoning bool     `json:"reasoning,omitempty"`
	Efforts   []string `json:"efforts,omitempty"`
	Free      bool     `json:"free,omitempty"`
	Price     *Price   `json:"price,omitempty"`
}

// Provider is the control-plane interface of one configured upstream.
type Provider interface {
	// Models fetches the provider's model catalog. It may perform network I/O.
	Models(ctx context.Context) ([]Model, error)
	// Resolve resolves a model ID from static rules only and never performs
	// network I/O. It reports false when the ID is not statically known.
	Resolve(id string) (Model, bool)
	// BaseURL returns the configured base URL for api, if any.
	BaseURL(api API) (string, bool)
	// Auth returns the auth for api. When Auth.Header is empty, the configured
	// prefix, keyHeaders and extra are merged onto the adapter default; a
	// non-empty Header replaces the adapter default entirely.
	Auth(api API) config.Auth
}

// adapter supplies the vendor-specific classification and auth behavior.
type adapter interface {
	classify(id string) []API
	auth(api API) config.Auth
	listAll() bool
}

type baseProvider struct {
	cfg     config.Provider
	adapter adapter
	client  *http.Client
}

// New builds a Provider from the provider configuration. The type must be
// registered and at least one API base URL must be set.
func New(cfg config.Provider) (Provider, error) {
	typ := strings.ToLower(strings.TrimSpace(cfg.Type))
	if typ == "" {
		return nil, fmt.Errorf("provider %q: type is required", cfg.Name)
	}
	if !config.IsProviderType(typ) {
		return nil, fmt.Errorf("provider %q: unknown type %q", cfg.Name, cfg.Type)
	}
	if !hasAPIBase(cfg) {
		return nil, fmt.Errorf("provider %q: at least one API base URL is required", cfg.Name)
	}

	var a adapter
	switch typ {
	case "deepseek":
		a = deepSeekAdapter{}
	case "opencode-go", "opencode-zen":
		a = openCodeAdapter{}
	case "commandcode":
		a = commandCodeAdapter{}
	case "generic":
		a = genericAdapter{}
	default:
		return nil, fmt.Errorf("provider %q: unknown type %q", cfg.Name, cfg.Type)
	}
	return &baseProvider{
		cfg:     cfg,
		adapter: a,
		client:  NewHTTPClient(cfg.TLS, modelsTimeout),
	}, nil
}

func hasAPIBase(cfg config.Provider) bool {
	return strings.TrimSpace(cfg.Chat) != "" ||
		strings.TrimSpace(cfg.Responses) != "" ||
		strings.TrimSpace(cfg.Anthropic) != "" ||
		strings.TrimSpace(cfg.Gemini) != ""
}

func (p *baseProvider) Resolve(id string) (Model, bool) {
	m := Model{ID: id}
	apis := p.adapter.classify(id)
	if len(apis) == 0 {
		return m, false
	}
	m.APIs = normalizeAPIs(apis)
	return m, true
}

func (p *baseProvider) BaseURL(api API) (string, bool) {
	var raw string
	switch api {
	case Chat:
		raw = p.cfg.Chat
	case Responses:
		raw = p.cfg.Responses
	case Anthropic:
		raw = p.cfg.Anthropic
	case Gemini:
		raw = p.cfg.Gemini
	default:
		return "", false
	}
	raw = strings.TrimSpace(raw)
	return raw, raw != ""
}

func (p *baseProvider) Auth(api API) config.Auth {
	if p.cfg.Auth.Header != "" {
		return p.cfg.Auth
	}
	a := p.adapter.auth(api)
	if p.cfg.Auth.Prefix != "" {
		a.Prefix = p.cfg.Auth.Prefix
	}
	a.KeyHeaders = mergeKeyHeaders(a.KeyHeaders, p.cfg.Auth.KeyHeaders)
	if len(p.cfg.Auth.Extra) > 0 {
		if a.Extra == nil {
			a.Extra = make(map[string]string, len(p.cfg.Auth.Extra))
		}
		for k, v := range p.cfg.Auth.Extra {
			a.Extra[k] = v
		}
	}
	return a
}

// mergeKeyHeaders appends overrides to the adapter defaults. An override for
// an existing header replaces the default value instead of injecting the key
// into the same header twice.
func mergeKeyHeaders(base, overrides []config.KeyHeader) []config.KeyHeader {
	if len(overrides) == 0 {
		return base
	}
	out := make([]config.KeyHeader, 0, len(base)+len(overrides))
	index := make(map[string]int, len(base))
	for _, kh := range base {
		index[strings.ToLower(kh.Header)] = len(out)
		out = append(out, kh)
	}
	for _, kh := range overrides {
		key := strings.ToLower(kh.Header)
		if i, ok := index[key]; ok {
			out[i] = kh
			continue
		}
		index[key] = len(out)
		out = append(out, kh)
	}
	return out
}

func containsFold(s, sub string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(sub))
}
