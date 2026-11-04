package gateway

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	xconfig "github.com/go-gost/x/config"

	gwconfig "github.com/go-gost/llm-gw/internal/config"
	"github.com/go-gost/llm-gw/internal/provider"
)

// errModelDisabled marks a node whose compiled target model is locally disabled
// by its provider. Routes skip such nodes instead of failing the whole
// configuration, matching the existing disabled-provider and disabled-node
// behavior.
var errModelDisabled = errors.New("target model is disabled")

// upstreamTarget is the result of compiling one route against one provider: a
// single base URL, wire API and provider model. A route has exactly one
// target, so its URL rewrites stay static.
type upstreamTarget struct {
	baseURL     string
	api         provider.API
	targetModel string
	auth        gwconfig.Auth
}

// compileRouteNode resolves the upstream target for one node in one route.
//
// The route, not the client model, decides the logical model: Match.Model for
// a normal route, or "" (the node's "*" mapping) for a catch-all. That keeps
// path rewrites and the runtime engine on the same route identity even when
// the node has an exact mapping for a model the client names differently.
func compileRouteNode(p gwconfig.Provider, n gwconfig.Node, r gwconfig.Route) (upstreamTarget, error) {
	prov, err := provider.New(p)
	if err != nil {
		return upstreamTarget{}, err
	}
	targetModel := n.EffectiveModel(r.Match.Model)
	if p.ModelDisabled(targetModel) {
		return upstreamTarget{}, fmt.Errorf("%w: %s", errModelDisabled, targetModel)
	}
	api, _, err := selectAPI(context.Background(), prov, targetModel)
	if err != nil {
		return upstreamTarget{}, err
	}
	baseURL, _ := prov.BaseURL(api)
	return upstreamTarget{
		baseURL:     baseURL,
		api:         api,
		targetModel: targetModel,
		auth:        prov.Auth(api),
	}, nil
}

// selectAPI resolves modelID and picks the first API the provider both
// advertises for that model and has a base URL for. Models without a static
// API classification fall back to the canonical provider order, which keeps
// unknown or newly listed models routable.
func selectAPI(_ context.Context, p provider.Provider, modelID string) (provider.API, provider.Model, error) {
	modelID = strings.TrimSpace(modelID)
	model, _ := p.Resolve(modelID)

	if len(model.APIs) > 0 {
		for _, api := range model.APIs {
			if _, ok := p.BaseURL(api); ok {
				return api, model, nil
			}
		}
		return "", model, fmt.Errorf("model %q: APIs %s have no base URL", modelID, joinAPIs(model.APIs))
	}

	for _, api := range []provider.API{provider.Chat, provider.Responses, provider.Anthropic, provider.Gemini} {
		if _, ok := p.BaseURL(api); ok {
			return api, model, nil
		}
	}
	return "", model, fmt.Errorf("model %q: no API base URL configured", modelID)
}

func joinAPIs(apis []provider.API) string {
	parts := make([]string, 0, len(apis))
	for _, api := range apis {
		parts = append(parts, string(api))
	}
	return strings.Join(parts, ", ")
}

// engineProtocol maps an upstream API to the llm-api-converter protocol name.
func engineProtocol(api provider.API) string {
	if api == provider.Chat {
		return "openai"
	}
	return string(api)
}

// upstreamRewrites maps every supported inbound path to the canonical path of
// the selected API. Query strings are left untouched by the rewrite engine.
func upstreamRewrites(baseURL string, api provider.API, targetModel string) []xconfig.HTTPURLRewriteConfig {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return nil
	}

	basePath := strings.TrimSuffix(u.Path, "/")
	sources := []string{
		`^/v1/chat/completions$`,
		`^/v1/responses$`,
		`^/v1/messages$`,
		`^/v1beta/models/[^/]+:generateContent$`,
		`^/v1beta/models/[^/]+:streamGenerateContent$`,
	}

	out := make([]xconfig.HTTPURLRewriteConfig, 0, len(sources))
	for _, source := range sources {
		replacement := canonicalPath(basePath, api, targetModel)
		if strings.HasSuffix(source, `:streamGenerateContent$`) && api == provider.Gemini {
			replacement = geminiPath(basePath, targetModel, true)
		}
		if replacement == "" {
			continue
		}
		out = append(out, xconfig.HTTPURLRewriteConfig{
			Match:       source,
			Replacement: strings.ReplaceAll(replacement, "$", "$$"),
		})
	}
	return out
}

func canonicalPath(basePath string, api provider.API, targetModel string) string {
	switch api {
	case provider.Chat:
		return joinURLPath(basePath, "/chat/completions")
	case provider.Responses:
		return joinURLPath(basePath, "/responses")
	case provider.Anthropic:
		// Anthropic bases are conventionally rooted at /v1 already; avoid a
		// duplicate segment when a user configures the full /v1 base.
		if strings.HasSuffix(basePath, "/v1") {
			return joinURLPath(basePath, "/messages")
		}
		return joinURLPath(basePath, "/v1/messages")
	case provider.Gemini:
		return geminiPath(basePath, targetModel, false)
	default:
		return ""
	}
}

func geminiPath(basePath, targetModel string, stream bool) string {
	model := strings.TrimPrefix(strings.TrimSpace(targetModel), "models/")
	action := ":generateContent"
	if stream {
		action = ":streamGenerateContent"
	}
	return joinURLPath(basePath, "/models/"+model+action)
}

func joinURLPath(basePath, suffix string) string {
	return strings.TrimSuffix(basePath, "/") + "/" + strings.TrimPrefix(suffix, "/")
}
