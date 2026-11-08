package gateway

import (
	"strings"
	"testing"

	gwconfig "github.com/ginuerzh/cuttle/internal/config"
	"github.com/ginuerzh/cuttle/internal/provider"
)

func TestCompileNodeTargetsAutoDeepSeek(t *testing.T) {
	p := gwconfig.Provider{
		Name:      "ds",
		Type:      "deepseek",
		Chat:      "https://ds.example/v1",
		Responses: "https://ds.example/v1",
		Anthropic: "https://ds.example/anthropic",
	}
	n := gwconfig.Node{Name: "n", Provider: "ds", Model: "deepseek-v4-flash"}

	targets, err := compileNodeTargets(p, n)
	if err != nil {
		t.Fatal(err)
	}
	if got := targets.defaultTarget.api; got != provider.Chat {
		t.Fatalf("default api = %q, want chat", got)
	}
	if got := targets.defaultTarget.baseURL; got != p.Chat {
		t.Fatalf("default base = %q, want %q", got, p.Chat)
	}
	if _, ok := targets.variants[provider.Responses]; !ok {
		t.Fatalf("responses variant missing: %+v", targets.variants)
	}
	if got := targets.variants[provider.Anthropic].api; got != provider.Anthropic {
		t.Fatalf("anthropic variant api = %q, want anthropic", got)
	}
}

func TestCompileNodeTargetsOpenRouter(t *testing.T) {
	p := gwconfig.Provider{
		Name:      "or",
		Type:      "openrouter",
		Chat:      "https://openrouter.ai/api/v1",
		Responses: "https://openrouter.ai/api/v1",
		Anthropic: "https://openrouter.ai/api",
	}
	n := gwconfig.Node{Name: "n", Provider: "or", Model: "anthropic/claude-sonnet-4"}

	targets, err := compileNodeTargets(p, n)
	if err != nil {
		t.Fatal(err)
	}
	if got := targets.defaultTarget.api; got != provider.Chat {
		t.Fatalf("default api = %q, want chat", got)
	}
	if got := targets.defaultTarget.baseURL; got != p.Chat {
		t.Fatalf("default base = %q, want %q", got, p.Chat)
	}
	if got := targets.defaultTarget.targetModel; got != n.Model {
		t.Fatalf("target model = %q, want %q", got, n.Model)
	}
	if got := targets.variants[provider.Responses].baseURL; got != p.Responses {
		t.Fatalf("responses base = %q, want %q", got, p.Responses)
	}
	if got := targets.variants[provider.Anthropic].baseURL; got != p.Anthropic {
		t.Fatalf("anthropic base = %q, want %q", got, p.Anthropic)
	}
	if got := targets.variants[provider.Anthropic].auth; got.Header != "Authorization" || got.Prefix != "Bearer " {
		t.Fatalf("anthropic auth = %+v, want bearer", got)
	}
}

func TestCompileNodeTargetsGenericUnknownModel(t *testing.T) {
	p := gwconfig.Provider{
		Name:      "gm",
		Type:      "generic",
		Chat:      "https://gm.example/v1",
		Anthropic: "https://gm.example/anthropic",
	}
	n := gwconfig.Node{Name: "n", Provider: "gm", Model: "model-not-in-the-catalog"}

	targets, err := compileNodeTargets(p, n)
	if err != nil {
		t.Fatal(err)
	}
	if got := targets.defaultTarget.api; got != provider.Chat {
		t.Fatalf("default api = %q, want chat", got)
	}
	if got := targets.variants[provider.Anthropic].api; got != provider.Anthropic {
		t.Fatalf("anthropic variant api = %q, want anthropic", got)
	}
}

func TestCompileNodeTargetsStaticClassificationExcludes(t *testing.T) {
	p := gwconfig.Provider{
		Name:      "oc",
		Type:      "opencode-go",
		Chat:      "https://oc.example/v1",
		Anthropic: "https://oc.example",
	}
	n := gwconfig.Node{Name: "n", Provider: "oc", Model: "deepseek-v4-flash"}

	targets, err := compileNodeTargets(p, n)
	if err != nil {
		t.Fatal(err)
	}
	if got := targets.defaultTarget.api; got != provider.Chat {
		t.Fatalf("default api = %q, want chat", got)
	}
	if _, ok := targets.variants[provider.Anthropic]; ok {
		t.Fatalf("anthropic variant = %+v, want none for a chat-only static model", targets.variants)
	}
}

func TestCompileNodeTargetsPinnedRescuesDefault(t *testing.T) {
	p := gwconfig.Provider{
		Name: "cc",
		Type: "commandcode",
		Chat: "https://cc.example/provider/v1",
	}
	n := gwconfig.Node{Name: "n", Provider: "cc", Model: "claude-sonnet-4", API: "chat"}

	targets, err := compileNodeTargets(p, n)
	if err != nil {
		t.Fatal(err)
	}
	if got := targets.defaultTarget.api; got != provider.Chat {
		t.Fatalf("pinned default api = %q, want chat", got)
	}
	if len(targets.variants) != 0 {
		t.Fatalf("pinned variants = %+v, want none", targets.variants)
	}
}

func TestCompileNodeTargetsPinnedMissingBase(t *testing.T) {
	p := gwconfig.Provider{
		Name: "p",
		Type: "generic",
		Chat: "https://p.example/v1",
	}
	n := gwconfig.Node{Name: "n", Provider: "p", Model: "m", API: "anthropic"}

	_, err := compileNodeTargets(p, n)
	if err == nil {
		t.Fatal("compileNodeTargets accepted a pinned API without a base URL")
	}
	if !strings.Contains(err.Error(), `node "n"`) || !strings.Contains(err.Error(), string(provider.Anthropic)) {
		t.Fatalf("error = %v, want it to name the node and api", err)
	}
}

func TestCompileNodeTargetsDisabledModel(t *testing.T) {
	p := gwconfig.Provider{
		Name: "p",
		Type: "generic",
		Chat: "https://p.example/v1",
		Models: map[string]gwconfig.ModelOverride{
			"m": {Disabled: true},
		},
	}
	n := gwconfig.Node{Name: "n", Provider: "p", Model: "m"}

	if _, err := compileNodeTargets(p, n); err == nil || !strings.Contains(err.Error(), errModelDisabled.Error()) {
		t.Fatalf("error = %v, want errModelDisabled", err)
	}
}
