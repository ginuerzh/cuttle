package gateway

import (
	"fmt"
	"regexp"

	gwconfig "github.com/go-gost/llm-gw/internal/config"
	"github.com/go-gost/llm-gw/internal/provider"
)

// countTokensPolicy maps the ordered entrypoint model globs to a per-route
// estimate decision. The first matching entry wins, mirroring routing
// precedence.
type countTokensPolicy struct {
	entries []countTokensEntry
}

type countTokensEntry struct {
	matcher  *regexp.Regexp // nil for a catch-all entry
	estimate bool
}

func (p *countTokensPolicy) estimateFor(model string) bool {
	if p == nil {
		return false
	}
	for _, e := range p.entries {
		if e.matcher == nil || e.matcher.MatchString(model) {
			return e.estimate
		}
	}
	return false
}

// routeNeedsCountTokensEstimate reports whether any enabled node in the route
// lacks an Anthropic target for the Anthropic inbound group. A mixed route
// estimates for every count_tokens request.
func routeNeedsCountTokensEstimate(plans []nodePlan) bool {
	for _, np := range plans {
		target := np.targets.defaultTarget
		if v, ok := np.targets.variants[provider.Anthropic]; ok {
			target = v
		}
		if target.api != provider.Anthropic || !provider.SupportsAnthropicCountTokens(np.provider.Type) {
			return true
		}
	}
	return false
}

func buildCountTokensPolicy(cfg *gwconfig.Config, plans map[string]*routePlan) (*countTokensPolicy, error) {
	p := &countTokensPolicy{entries: make([]countTokensEntry, 0, len(cfg.Entrypoints))}
	for i := range cfg.Entrypoints {
		e := &cfg.Entrypoints[i]
		pattern, catchAll, err := modelGlobPattern(e.Match)
		if err != nil {
			return nil, fmt.Errorf("entrypoint %q: %w", e.Name, err)
		}
		plan, ok := plans[e.Route]
		if !ok {
			return nil, fmt.Errorf("entrypoint %q: unknown route %q", e.Name, e.Route)
		}
		entry := countTokensEntry{estimate: plan.countTokensEstimate}
		if !catchAll {
			entry.matcher = regexp.MustCompile(pattern)
		}
		p.entries = append(p.entries, entry)
	}
	return p, nil
}
