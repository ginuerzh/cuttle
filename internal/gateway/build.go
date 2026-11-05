package gateway

import (
	"errors"
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

// buildHopGroup compiles the configured routes into a hop group and emits one
// hop entry per entrypoint, in declaration order. The FIFO group selector then
// makes entrypoint order the single precedence rule.
func buildHopGroup(cfg *gwconfig.Config, log logger.Logger, facts *stats.Facts) (hop.Hop, error) {
	if err := registerRewriters(cfg, log, facts); err != nil {
		return nil, err
	}

	routes := make(map[string]gwconfig.Route, len(cfg.Routes))
	for _, r := range cfg.Routes {
		routes[r.Name] = r
	}
	routeHops := make(map[string]hop.Hop, len(cfg.Routes))
	entries := make([]*xhop.HopEntry, 0, len(cfg.Entrypoints))
	for _, e := range cfg.Entrypoints {
		r, ok := routes[e.Route]
		if !ok {
			return nil, fmt.Errorf("entrypoint %q: unknown route %q", e.Name, e.Route)
		}
		h, ok := routeHops[r.Name]
		if !ok {
			var err error
			h, err = buildRouteHop(cfg, r, log)
			if err != nil {
				return nil, fmt.Errorf("route %q: %w", r.Name, err)
			}
			routeHops[r.Name] = h
		}
		m, err := buildEntrypointMatcher(e.Match)
		if err != nil {
			return nil, fmt.Errorf("entrypoint %q: %w", e.Name, err)
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
		target, err := compileRouteNode(p, n)
		if err != nil {
			if errors.Is(err, errModelDisabled) {
				log.Warnf("route %s node %s: %v", r.Name, n.Name, err)
				continue
			}
			return nil, fmt.Errorf("node %q: %w", n.Name, err)
		}
		ns, err := buildNodes(p, n, r.Name, target)
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

func buildNodes(p gwconfig.Provider, n gwconfig.Node, route string, target upstreamTarget) ([]*xconfig.NodeConfig, error) {
	auth := target.auth

	u, err := url.Parse(strings.TrimSpace(target.baseURL))
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("invalid base URL %q", target.baseURL)
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

	rewrites := upstreamRewrites(target.baseURL, target.api, target.targetModel)

	keys := p.Keys
	if len(keys) == 0 {
		keys = []string{""}
	}

	nodes := make([]*xconfig.NodeConfig, 0, len(keys))
	for i, key := range keys {
		headers := make(map[string]string, len(auth.Extra)+1)
		if key != "" {
			if auth.Header != "" {
				headers[auth.Header] = auth.Prefix + key
			}
			for _, kh := range auth.KeyHeaders {
				if kh.Header == "" {
					continue
				}
				headers[kh.Header] = kh.Prefix + key
			}
		}
		// Extra is the user's final override: apply it after every
		// key-derived header so an explicit value always wins.
		for k, v := range auth.Extra {
			headers[k] = v
		}

		name := fmt.Sprintf("%s/%s/%s", route, p.Name, n.Name)
		if len(keys) > 1 {
			name = fmt.Sprintf("%s/%d", name, i)
		}

		bodyRewrites := []xconfig.HTTPBodyRewriteConfig{{
			Rewriter: rewriterName(route, n.Name),
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
		nodes = append(nodes, nodeCfg)
	}
	return nodes, nil
}

// buildEntrypointMatcher compiles one entrypoint's model globs into a single
// anchored body-model matcher. The entrypoint order in the hop group, not the
// order inside this list, decides precedence.
func buildEntrypointMatcher(match []string) (corerouting.Matcher, error) {
	globs := make([]string, 0, len(match))
	for _, g := range match {
		if g = strings.TrimSpace(g); g != "" {
			globs = append(globs, g)
		}
	}
	if len(globs) == 0 {
		return nil, fmt.Errorf("match is required")
	}

	inner := make([]string, 0, len(globs))
	for _, g := range globs {
		if g == "*" {
			// An explicit catch-all matches requests without a model body too.
			return nil, nil
		}
		rgx := globToRegexp(g)
		rgx = strings.TrimPrefix(rgx, "^")
		rgx = strings.TrimSuffix(rgx, "$")
		inner = append(inner, rgx)
	}
	pattern := "(?i)^(" + strings.Join(inner, "|") + ")$"
	return xrouting.NewMatcher(fmt.Sprintf("BodyJSON(%q, %q)", "model", pattern))
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

// rewriterName returns the registry key for a route-scoped node engine. The
// same node can select different target APIs in different routes, so engines
// cannot be shared across routes. Route and node names forbid '/', so the
// separator cannot make two (route, node) pairs collide.
func rewriterName(route, node string) string {
	return "r/" + route + "/" + node
}

// registerRewriters installs one in-process rewriter per (route, referenced
// enabled node). Called before ParseHop because node parsing resolves
// rewriters from the registry. Routing by node alone is not enough: a node
// shared by two routes compiles to different APIs and paths per route.
func registerRewriters(cfg *gwconfig.Config, log logger.Logger, facts *stats.Facts) error {
	providers := make(map[string]gwconfig.Provider, len(cfg.Providers))
	for _, p := range cfg.Providers {
		providers[p.Name] = p
	}
	nodes := make(map[string]gwconfig.Node, len(cfg.Nodes))
	for _, n := range cfg.Nodes {
		nodes[n.Name] = n
	}

	for _, r := range cfg.Routes {
		for _, ref := range r.Nodes {
			n, ok := nodes[ref]
			if !ok || !n.EnabledValue() {
				continue
			}
			p, ok := providers[n.Provider]
			if !ok || !p.EnabledValue() {
				continue
			}

			name := rewriterName(r.Name, n.Name)
			target, err := compileRouteNode(p, n)
			if err != nil {
				if errors.Is(err, errModelDisabled) {
					registry.RewriterRegistry().Unregister(name)
					continue
				}
				return fmt.Errorf("register rewriter %s: %w", name, err)
			}
			// Record the same API that built the node's path rewrites,
			// including for non-chat inbound paths where the engine returns
			// before ResolveModel runs.
			protocol := string(target.api)

			registry.RewriterRegistry().Unregister(name)
			if err := registry.RewriterRegistry().Register(name, gwrewriter.New(newNodeEngine(target), p.Name, protocol, facts)); err != nil {
				return fmt.Errorf("register rewriter %s: %w", name, err)
			}
		}
	}
	return nil
}

func newNodeEngine(target upstreamTarget) *engine.Engine {
	targetModel := target.targetModel
	engineProto := engineProtocol(target.api)
	return engine.New(engine.Options{
		Model:     targetModel,
		MaxTokens: 8192,
		Cache:     "memory",
		ResolveModel: func(string, string) (string, string, string) {
			return targetModel, engineProto, ""
		},
	})
}
