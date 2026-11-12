package gateway

import (
	"fmt"
	"regexp"
	"strings"

	gwconfig "github.com/ginuerzh/cuttle/internal/config"
	"github.com/ginuerzh/cuttle/internal/provider"
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
		var entry countTokensEntry
		// A draft entrypoint has no upstream, so count_tokens must pass through
		// and fail with the rest of its routing instead of being answered by a
		// local estimate. Otherwise the entrypoint estimates when any of its
		// bindings needs it: the probe verdict is not evaluated on the
		// count_tokens path, so the conservative rule is the only sound one.
		for j := range e.Routes {
			ref := strings.TrimSpace(e.Routes[j].Route)
			r, ok := cfg.Route(ref)
			if !ok {
				return nil, fmt.Errorf("entrypoint %q: routes[%d]: unknown route %q", e.Name, j, ref)
			}
			plan, ok := plans[r.ID]
			if !ok {
				return nil, fmt.Errorf("entrypoint %q: routes[%d]: unknown route %q", e.Name, j, ref)
			}
			if plan.countTokensEstimate {
				entry.estimate = true
			}
		}
		if !catchAll {
			entry.matcher = regexp.MustCompile(pattern)
		}
		p.entries = append(p.entries, entry)
	}
	return p, nil
}
