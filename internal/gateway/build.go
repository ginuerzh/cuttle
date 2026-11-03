package gateway

import (
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"

	"github.com/go-gost/core/hop"
	"github.com/go-gost/core/logger"
	corerouting "github.com/go-gost/core/routing"
	xconfig "github.com/go-gost/x/config"
	hop_parser "github.com/go-gost/x/config/parsing/hop"
	xhop "github.com/go-gost/x/hop"
	"github.com/go-gost/x/registry"
	xrouting "github.com/go-gost/x/routing"
	xs "github.com/go-gost/x/selector"

	"llm-api-converter/engine"

	gwrewriter "github.com/go-gost/llm-gw/internal/rewriter"
	"github.com/go-gost/llm-gw/internal/stats"

	gwconfig "github.com/go-gost/llm-gw/internal/config"

	_ "github.com/go-gost/x/connector/http"
	_ "github.com/go-gost/x/dialer/tcp"
)

// buildHopGroup compiles the configured routes into a hop group. Route
// declaration order is preserved; the group selector is FIFO so the first
// matching route wins.
func buildHopGroup(cfg *gwconfig.Config, log logger.Logger, facts *stats.Facts) (hop.Hop, error) {
	if err := registerRewriters(cfg, log, facts); err != nil {
		return nil, err
	}

	entries := make([]*xhop.HopEntry, 0, len(cfg.Routes))
	for _, r := range cfg.Routes {
		h, err := buildRouteHop(cfg, r, log)
		if err != nil {
			return nil, fmt.Errorf("route %q: %w", r.Name, err)
		}
		m, err := buildMatcher(cfg, r.Match)
		if err != nil {
			return nil, fmt.Errorf("route %q: matcher: %w", r.Name, err)
		}
		entries = append(entries, xhop.NewHopEntry(h, m, nil))
	}
	return xhop.NewHopGroup(
		xhop.WithEntriesOption(entries...),
		xhop.WithGroupSelectorOption(xs.NewSelector(xs.FIFOStrategy[hop.Hop]())),
		xhop.WithGroupLoggerOption(log.WithFields(map[string]any{
			"kind": "hopGroup",
		})),
	), nil
}

func buildRouteHop(cfg *gwconfig.Config, r gwconfig.Route, log logger.Logger) (hop.Hop, error) {
	providers := make(map[string]gwconfig.Provider, len(cfg.Providers))
	for _, p := range cfg.Providers {
		providers[p.Name] = p
	}
	bindings := make(map[string]gwconfig.Node, len(cfg.Nodes))
	for _, n := range cfg.Nodes {
		bindings[n.Name] = n
	}

	var nodes []*xconfig.NodeConfig
	for _, ref := range r.Nodes {
		n, ok := bindings[ref]
		if !ok {
			return nil, fmt.Errorf("unknown node %q", ref)
		}
		if !n.EnabledValue() {
			continue
		}
		p, ok := providers[n.Provider]
		if !ok {
			return nil, fmt.Errorf("node %q: unknown provider %q", n.Name, n.Provider)
		}
		if !p.EnabledValue() {
			continue
		}
		ns, err := buildNodes(p, n, r.Name, cfg.Effort)
		if err != nil {
			return nil, fmt.Errorf("node %q: %w", n.Name, err)
		}
		nodes = append(nodes, ns...)
	}

	strategy := r.Strategy
	if strategy == "" {
		strategy = "round"
	}
	return hop_parser.ParseHop(&xconfig.HopConfig{
		Name: "route/" + r.Name,
		Selector: &xconfig.SelectorConfig{
			Strategy: strategy,
		},
		Nodes: nodes,
	}, log)
}

func buildNodes(p gwconfig.Provider, n gwconfig.Node, route string, effort gwconfig.Effort) ([]*xconfig.NodeConfig, error) {
	u, err := url.Parse(strings.TrimSpace(p.BaseURL))
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("invalid base_url %q", p.BaseURL)
	}

	secure := u.Scheme == "https"
	port := u.Port()
	if port == "" {
		if secure {
			port = "443"
		} else {
			port = "80"
		}
	}
	addr := net.JoinHostPort(u.Hostname(), port)

	var tlsCfg *xconfig.TLSNodeConfig
	if secure {
		serverName := p.TLS.ServerName
		if serverName == "" {
			serverName = u.Hostname()
		}
		verify := true
		if p.TLS.Secure != nil {
			verify = *p.TLS.Secure
		}
		tlsCfg = &xconfig.TLSNodeConfig{ServerName: serverName, Secure: verify}
	}

	rewrites := buildRewrites(u.Path, p.PathMap)

	keys := p.Keys
	if len(keys) == 0 {
		keys = []string{""}
	}

	nodes := make([]*xconfig.NodeConfig, 0, len(keys))
	for i, key := range keys {
		headers := make(map[string]string, len(p.Auth.Extra)+1)
		for k, v := range p.Auth.Extra {
			headers[k] = v
		}
		if key != "" {
			headers[p.Auth.Header] = p.Auth.Prefix + key
			for _, kh := range p.Auth.KeyHeaders {
				headers[kh.Header] = kh.Prefix + key
			}
		}

		tier := strings.ToLower(strings.TrimSpace(n.Tier))
		if tier == "" {
			tier = "base"
		}
		name := fmt.Sprintf("%s/%s/%s/%s", route, p.Name, tier, n.Name)
		if len(keys) > 1 {
			name = fmt.Sprintf("%s/%d", name, i)
		}

		bodyRewrites := []xconfig.HTTPBodyRewriteConfig{{
			Rewriter: "n:" + n.Name,
			Type:     "*",
		}}

		var metadata map[string]any
		if n.Backup {
			metadata = map[string]any{"backup": true}
		}
		if n.Weight > 0 {
			if metadata == nil {
				metadata = make(map[string]any)
			}
			metadata["weight"] = n.Weight
		}

		nodeCfg := &xconfig.NodeConfig{
			Name:     name,
			Addr:     addr,
			TLS:      tlsCfg,
			Metadata: metadata,
			HTTP: &xconfig.HTTPNodeConfig{
				Host:                u.Host,
				RequestHeader:       headers,
				RewriteURL:          rewrites,
				RewriteRequestBody:  bodyRewrites,
				RewriteResponseBody: bodyRewrites,
				FailCodes:           "429,5xx",
			},
		}
		if tier != "base" {
			priority := 0
			switch tier {
			case "high":
				priority = 3
			case "medium":
				priority = 2
			case "low":
				priority = 1
			}
			nodeCfg.Matcher = &xconfig.NodeMatcherConfig{
				Rule:     effortRule(tier, effort),
				Priority: priority,
			}
		}

		nodes = append(nodes, nodeCfg)
	}
	return nodes, nil
}

// buildRewrites maps client paths to provider paths, keeping the base path
// from base_url (for example https://host/provider/v1).
func buildRewrites(basePath string, pathMap map[string]string) []xconfig.HTTPURLRewriteConfig {
	if len(pathMap) == 0 {
		return nil
	}

	out := make([]xconfig.HTTPURLRewriteConfig, 0, len(pathMap))
	for from, to := range pathMap {
		replacement := to
		if basePath != "" {
			replacement = strings.TrimSuffix(basePath, "/") + "/" + strings.TrimPrefix(to, "/")
		}
		out = append(out, xconfig.HTTPURLRewriteConfig{
			Match:       "^" + regexp.QuoteMeta(from) + "$",
			Replacement: replacement,
		})
	}
	return out
}

func buildMatcher(cfg *gwconfig.Config, m gwconfig.Match) (corerouting.Matcher, error) {
	var rules []string
	if m.Model != "" {
		pattern := globToRegexp(m.Model)
		// A route's model is a logical model: compile the union of the alias
		// globs that resolve to it. Fall back to a literal glob when no alias
		// targets this logical model (keeps older configs working).
		var globs []string
		for _, a := range cfg.Aliases {
			if strings.EqualFold(strings.TrimSpace(a.Model), strings.TrimSpace(m.Model)) {
				globs = append(globs, a.Match)
			}
		}
		if len(globs) > 0 {
			inner := make([]string, 0, len(globs))
			for _, g := range globs {
				rgx := globToRegexp(g)
				rgx = strings.TrimPrefix(rgx, "^")
				rgx = strings.TrimSuffix(rgx, "$")
				inner = append(inner, rgx)
			}
			pattern = "^(" + strings.Join(inner, "|") + ")$"
		}
		rules = append(rules, fmt.Sprintf("BodyJSON(%q, %q)", "model", pattern))
	}
	if m.Path != "" {
		rules = append(rules, fmt.Sprintf("PathPrefix(%q)", m.Path))
	}
	if m.Host != "" {
		rules = append(rules, fmt.Sprintf("HostRegexp(%q)", m.Host))
	}
	if len(rules) == 0 {
		return nil, nil
	}
	return xrouting.NewMatcher(strings.Join(rules, " && "))
}

// globToRegexp converts a small glob (* only) to an anchored Go regexp.
func globToRegexp(glob string) string {
	parts := strings.Split(glob, "*")
	var b strings.Builder
	b.WriteString("^")
	for i, part := range parts {
		if i > 0 {
			b.WriteString(".*")
		}
		b.WriteString(regexp.QuoteMeta(part))
	}
	b.WriteString("$")
	return b.String()
}

// registerRewriters installs one in-process rewriter per node. Called before
// ParseHop because node parsing resolves rewriters from the registry.
func registerRewriters(cfg *gwconfig.Config, log logger.Logger, facts *stats.Facts) error {
	providers := make(map[string]gwconfig.Provider, len(cfg.Providers))
	for _, p := range cfg.Providers {
		providers[p.Name] = p
	}
	for _, n := range cfg.Nodes {
		p, ok := providers[n.Provider]
		if !ok {
			continue
		}
		name := "n:" + n.Name
		registry.RewriterRegistry().Unregister(name)
		if err := registry.RewriterRegistry().Register(name, gwrewriter.New(newNodeEngine(cfg, p, n), p.Name, p.Protocol, facts)); err != nil {
			return fmt.Errorf("register rewriter %s: %w", name, err)
		}
	}
	return nil
}

func newNodeEngine(cfg *gwconfig.Config, p gwconfig.Provider, n gwconfig.Node) *engine.Engine {
	fallback := n.EffectiveModel("", "")
	binding := n
	return engine.New(engine.Options{
		Model:     fallback,
		MaxTokens: 8192,
		Cache:     "memory",
		ResolveModel: func(clientModel, effort string) (string, string, string) {
			logical := cfg.ResolveAlias(clientModel)
			if logical == "" {
				return "", "", ""
			}
			model := binding.EffectiveModel(logical, effort)
			if model == "" {
				return "", "", logical
			}
			return model, p.Protocol, logical
		},
	})
}

// effortRule builds a BodyJSON matcher for one effort band. String fields use
// the regexp form; budgets use the numeric comparison form. Bands overlap on
// purpose; node priority resolves the overlap (high > medium > low).
func effortRule(tier string, e gwconfig.Effort) string {
	switch tier {
	case "high":
		return fmt.Sprintf(`BodyJSON("reasoning_effort", "^(?i)(high|xhigh)$") || `+
			`BodyJSON("reasoning.effort", "^(?i)high$") || `+
			`BodyJSON("generationConfig.thinkingConfig.thinkingLevel", "^(?i)high$") || `+
			`BodyJSON("thinking.budget_tokens", "ge", "%d") || `+
			`BodyJSON("generationConfig.thinkingConfig.thinkingBudget", "ge", "%d")`,
			e.HighThreshold, e.HighThreshold)
	case "medium":
		return fmt.Sprintf(`BodyJSON("reasoning_effort", "^(?i)medium$") || `+
			`BodyJSON("reasoning.effort", "^(?i)medium$") || `+
			`BodyJSON("generationConfig.thinkingConfig.thinkingLevel", "^(?i)medium$") || `+
			`BodyJSON("thinking.budget_tokens", "ge", "%d") || `+
			`BodyJSON("generationConfig.thinkingConfig.thinkingBudget", "ge", "%d")`,
			e.MediumThreshold, e.MediumThreshold)
	default:
		return fmt.Sprintf(`BodyJSON("reasoning_effort", "^(?i)(low|minimal)$") || `+
			`BodyJSON("reasoning.effort", "^(?i)low$") || `+
			`BodyJSON("generationConfig.thinkingConfig.thinkingLevel", "^(?i)low$") || `+
			`BodyJSON("thinking.budget_tokens", "ge", "%d") || `+
			`BodyJSON("generationConfig.thinkingConfig.thinkingBudget", "ge", "%d")`,
			e.LowThreshold, e.LowThreshold)
	}
}
