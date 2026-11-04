package provider

import (
	"strings"

	"github.com/go-gost/llm-gw/internal/config"
)

// commandCodeAdapter serves Claude models on Anthropic Messages and every
// other model on both OpenAI-style APIs.
type commandCodeAdapter struct{}

func (commandCodeAdapter) classify(id string) []API {
	if strings.TrimSpace(id) == "" {
		return nil
	}
	if containsFold(id, "claude") {
		return []API{Anthropic}
	}
	return []API{Chat, Responses}
}

// commandCode authenticates Anthropic requests with Bearer as well, unlike
// Anthropic's own x-api-key convention.
func (commandCodeAdapter) auth(API) config.Auth { return bearerAuth() }

func (commandCodeAdapter) listAll() bool { return false }

func (commandCodeAdapter) keylessCatalog() bool { return false }
