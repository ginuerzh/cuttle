package provider

import (
	"strings"

	"github.com/ginuerzh/cuttle/internal/config"
)

// openRouterAdapter covers OpenRouter's OpenAI-compatible Chat, Responses and
// Anthropic Messages endpoints. The catalog refines per-model capabilities; the
// static fallback advertises all three text APIs.
type openRouterAdapter struct{}

func (openRouterAdapter) classify(id string) []API {
	if strings.TrimSpace(id) == "" {
		return nil
	}
	return []API{Chat, Responses, Anthropic}
}

// OpenRouter accepts a bearer token on every endpoint, including Messages.
func (openRouterAdapter) auth(API) config.Auth { return bearerAuth() }

func (openRouterAdapter) listAll() bool { return false }

// openRouter's model catalog is public and ignores the API key.
func (openRouterAdapter) keylessCatalog() bool { return true }
