package provider

import "strings"

// SupportsAnthropicCountTokens reports whether typ implements the Anthropic
// count_tokens endpoint. OpenRouter does not; routes using it fall back to the
// gateway's local estimate.
func SupportsAnthropicCountTokens(typ string) bool {
	return strings.ToLower(strings.TrimSpace(typ)) != "openrouter"
}
