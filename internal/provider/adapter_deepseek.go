package provider

import "github.com/go-gost/llm-gw/internal/config"

// deepSeekAdapter covers the official DeepSeek endpoints. Its models are
// served on all three text APIs; the configured base URLs decide which one a
// route can actually use.
type deepSeekAdapter struct{}

func (deepSeekAdapter) classify(id string) []API {
	if !containsFold(id, "deepseek") {
		return nil
	}
	return []API{Chat, Responses, Anthropic}
}

func (deepSeekAdapter) auth(api API) config.Auth { return defaultAuth(api) }

func (deepSeekAdapter) listAll() bool { return false }
