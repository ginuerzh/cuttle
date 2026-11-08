package provider

import "testing"

func TestSupportsAnthropicCountTokens(t *testing.T) {
	if SupportsAnthropicCountTokens("openrouter") {
		t.Fatal("openrouter has no native count_tokens endpoint")
	}
	if !SupportsAnthropicCountTokens("deepseek") {
		t.Fatal("deepseek keeps native count_tokens")
	}
	if !SupportsAnthropicCountTokens("") {
		t.Fatal("unknown/empty types default to native")
	}
}
