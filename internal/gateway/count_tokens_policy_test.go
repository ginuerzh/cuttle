package gateway

import (
	"regexp"
	"testing"

	gwconfig "github.com/ginuerzh/cuttle/internal/config"
	"github.com/ginuerzh/cuttle/internal/provider"
)

func TestRouteNeedsCountTokensEstimate(t *testing.T) {
	anthropic := nodeTargets{defaultTarget: upstreamTarget{api: provider.Anthropic}}
	chat := nodeTargets{defaultTarget: upstreamTarget{api: provider.Chat}}
	gemini := nodeTargets{defaultTarget: upstreamTarget{api: provider.Gemini}}
	anthropicVariant := nodeTargets{
		defaultTarget: upstreamTarget{api: provider.Chat},
		variants: map[provider.API]upstreamTarget{
			provider.Anthropic: {api: provider.Anthropic},
		},
	}
	openRouterAnthropic := nodeTargets{defaultTarget: upstreamTarget{api: provider.Anthropic}}

	cases := []struct {
		name  string
		plans []nodePlan
		want  bool
	}{
		{"empty", nil, false},
		{"all anthropic", []nodePlan{{targets: anthropic}}, false},
		{"chat", []nodePlan{{targets: chat}}, true},
		{"gemini", []nodePlan{{targets: gemini}}, true},
		{"anthropic variant wins", []nodePlan{{targets: anthropicVariant}}, false},
		{"openrouter anthropic target estimates", []nodePlan{{
			provider: gwconfig.Provider{Type: "openrouter"},
			targets:  openRouterAnthropic,
		}}, true},
		{"mixed", []nodePlan{{targets: anthropic}, {targets: chat}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := routeNeedsCountTokensEstimate(tc.plans); got != tc.want {
				t.Fatalf("routeNeedsCountTokensEstimate = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCountTokensPolicyPrecedence(t *testing.T) {
	p := &countTokensPolicy{entries: []countTokensEntry{
		{matcher: regexp.MustCompile(`(?i)^claude-.*$`), estimate: false},
		{matcher: nil, estimate: true},
	}}
	if p.estimateFor("claude-opus-4-5") {
		t.Fatal("claude model should pass through")
	}
	if !p.estimateFor("gpt-4") {
		t.Fatal("non-claude model should estimate")
	}
	if !p.estimateFor("") {
		t.Fatal("catch-all should match an empty model")
	}

	empty := &countTokensPolicy{}
	if empty.estimateFor("gpt-4") {
		t.Fatal("empty policy should pass through")
	}
	var nilPolicy *countTokensPolicy
	if nilPolicy.estimateFor("gpt-4") {
		t.Fatal("nil policy should pass through")
	}
}

func TestBuildCountTokensPolicyFromConfig(t *testing.T) {
	cfg := &gwconfig.Config{
		Entrypoints: []gwconfig.Entrypoint{
			{ID: "e_0000000001", Name: "anthropic", Match: []string{"claude-*"}, Route: "r_0000000001"},
			{ID: "e_0000000002", Name: "rest", Match: []string{"*"}, Route: "r_0000000002"},
		},
		Routes: []gwconfig.Route{
			{ID: "r_0000000001", Name: "a"},
			{ID: "r_0000000002", Name: "r"},
		},
	}
	plans := map[string]*routePlan{
		"r_0000000001": {countTokensEstimate: false},
		"r_0000000002": {countTokensEstimate: true},
	}
	p, err := buildCountTokensPolicy(cfg, plans)
	if err != nil {
		t.Fatal(err)
	}
	if p.estimateFor("claude-opus-4-5") {
		t.Fatal("claude model should pass through")
	}
	if !p.estimateFor("gpt-4") {
		t.Fatal("non-claude model should estimate")
	}
}

func TestBuildCountTokensPolicyDraftEntrypoint(t *testing.T) {
	cfg := &gwconfig.Config{
		Entrypoints: []gwconfig.Entrypoint{
			{ID: "e_0000000001", Name: "draft", Match: []string{"*"}},
			{ID: "e_0000000002", Name: "rest", Match: []string{"*"}, Route: "r_0000000002"},
		},
		Routes: []gwconfig.Route{{ID: "r_0000000002", Name: "r"}},
	}
	plans := map[string]*routePlan{"r_0000000002": {countTokensEstimate: true}}
	p, err := buildCountTokensPolicy(cfg, plans)
	if err != nil {
		t.Fatal(err)
	}
	if p.estimateFor("gpt-4") {
		t.Fatal("a draft entrypoint must pass through so the request fails, not estimate locally")
	}
}

func TestGatewayCompilesCountTokensPolicy(t *testing.T) {
	up, _ := captureUpstream(t)

	chat := startGateway(t, matrixConfig(up.URL, provider.Chat))
	if p := chat.countTokens.Load(); p == nil || !p.estimateFor("claude-opus-4-5") {
		t.Fatal("chat route should estimate")
	}

	anth := startGateway(t, matrixConfig(up.URL, provider.Anthropic))
	if p := anth.countTokens.Load(); p == nil || p.estimateFor("claude-opus-4-5") {
		t.Fatal("anthropic route should pass through")
	}
}
