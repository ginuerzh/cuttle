package provider

import (
	"net/http"

	"github.com/ginuerzh/cuttle/internal/config"
)

// defaultAuth is the conventional auth for an API: Bearer for OpenAI-style
// APIs, x-api-key for Anthropic, and x-goog-api-key for Gemini.
func defaultAuth(api API) config.Auth {
	switch api {
	case Anthropic:
		return xAPIKeyAuth()
	case Gemini:
		return geminiAuth()
	default:
		return bearerAuth()
	}
}

func bearerAuth() config.Auth {
	return config.Auth{Header: "Authorization", Prefix: "Bearer "}
}

func xAPIKeyAuth() config.Auth {
	return config.Auth{Header: "x-api-key"}
}

func geminiAuth() config.Auth {
	return config.Auth{Header: "x-goog-api-key"}
}

// openCodeAuth adds x-api-key to the Bearer header on OpenAI-style APIs:
// OpenCode's gateway accepts either, and agents commonly send both.
func openCodeAuth(api API) config.Auth {
	switch api {
	case Anthropic:
		return xAPIKeyAuth()
	case Gemini:
		return geminiAuth()
	default:
		a := bearerAuth()
		a.KeyHeaders = []config.KeyHeader{{Header: "x-api-key"}}
		return a
	}
}

// AnthropicVersion is the default Anthropic API version sent on catalog and
// health requests.
const AnthropicVersion = "2023-06-01"

// ApplyAnthropicVersion sets the default anthropic-version header for
// Anthropic requests. An explicitly configured value (for example via
// auth.extra) wins.
func ApplyAnthropicVersion(req *http.Request, api API) {
	if api != Anthropic || req.Header.Get("anthropic-version") != "" {
		return
	}
	req.Header.Set("anthropic-version", AnthropicVersion)
}

// applyAuth injects key into req according to a. Extra headers are applied
// even for keyless providers; key-derived headers are skipped when key is
// empty.
func applyAuth(req *http.Request, a config.Auth, key string) {
	if key != "" {
		if a.Header != "" {
			req.Header.Set(a.Header, a.Prefix+key)
		}
		for _, kh := range a.KeyHeaders {
			if kh.Header == "" {
				continue
			}
			req.Header.Set(kh.Header, kh.Prefix+key)
		}
	}
	for k, v := range a.Extra {
		req.Header.Set(k, v)
	}
}
