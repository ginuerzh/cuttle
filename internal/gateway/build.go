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

	"github.com/ginuerzh/llm-api-converter/engine"

	"github.com/go-gost/llm-gw/internal/capture"
	gwconfig "github.com/go-gost/llm-gw/internal/config"
	"github.com/go-gost/llm-gw/internal/provider"
	gwrewriter "github.com/go-gost/llm-gw/internal/rewriter"
	"github.com/go-gost/llm-gw/internal/stats"

	_ "github.com/go-gost/x/connector/http"
	_ "github.com/go-gost/x/dialer/tcp"
)

// buildHopGroup compiles the configured routes into a hop group and emits one
// entry per entrypoint and inbound protocol group, in declaration order,
// followed by the entrypoint's fallback entry. The FIFO group selector then
// makes entrypoint order the single precedence rule.
func buildHopGroup(cfg *gwconfig.Config, log logger.Logger, facts *stats.Facts, captureMgr *capture.Manager) (hop.Hop, error) {
	if err := registerHeaderRewriter(); err != nil {
		return nil, err
	}

	routes := make(map[string]gwconfig.Route, len(cfg.Routes))
	for _, r := range cfg.Routes {
		routes[r.Name] = r
	}
	routePlans := make(map[string]*routePlan, len(cfg.Routes))
	entries := make([]*xhop.HopEntry, 0, len(cfg.Entrypoints)*(len(inboundGroups)+1))
	for _, e := range cfg.Entrypoints {
		r, ok := routes[e.Route]
		if !ok {
			return nil, fmt.Errorf("entrypoint %q: unknown route %q", e.Name, e.Route)
		}
		plan, ok := routePlans[r.Name]
		if !ok {
			var err error
			plan, err = buildRoutePlan(cfg, r, log, facts, captureMgr)
			if err != nil {
				return nil, fmt.Errorf("route %q: %w", r.Name, err)
			}
			routePlans[r.Name] = plan
		}

		for _, group := range inboundGroups {
			h, ok := plan.hops[group.api]
			if !ok {
				continue
			}
			m, err := buildProtocolMatcher(e.Match, group.pathPattern)
			if err != nil {
				return nil, fmt.Errorf("entrypoint %q: %w", e.Name, err)
			}
			entries = append(entries, xhop.NewHopEntry(h, m, nil))
		}

		m, err := buildEntrypointMatcher(e.Match)
		if err != nil {
			return nil, fmt.Errorf("entrypoint %q: %w", e.Name, err)
		}
		entries = append(entries, xhop.NewHopEntry(plan.hops[""], m, nil))
	}
	return xhop.NewHopGroup(
		xhop.WithEntriesOption(entries...),
		xhop.WithGroupSelectorOption(xs.NewSelector(xs.FIFOStrategy[hop.Hop]())),
		xhop.WithGroupLoggerOption(log.WithFields(map[string]any{
			"kind": "hopGroup",
		})),
	), nil
}

// routePlan holds the compiled hops for one route: the fallback variant under
// the empty API key and one variant per inbound protocol that changes at least
// one node's target API.
type routePlan struct {
	hops map[provider.API]hop.Hop
}

// nodePlan is one enabled node and its compiled target plan.
type nodePlan struct {
	provider gwconfig.Provider
	node     gwconfig.Node
	targets  nodeTargets
}

func buildRoutePlan(cfg *gwconfig.Config, r gwconfig.Route, log logger.Logger, facts *stats.Facts, captureMgr *capture.Manager) (*routePlan, error) {
	providers := make(map[string]gwconfig.Provider, len(cfg.Providers))
	for _, p := range cfg.Providers {
		providers[p.Name] = p
	}
	bindings := make(map[string]gwconfig.Node, len(cfg.Nodes))
	for _, n := range cfg.Nodes {
		bindings[n.Name] = n
	}

	var plans []nodePlan
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
		targets, err := compileNodeTargets(p, n)
		if err != nil {
			if errors.Is(err, errModelDisabled) {
				log.Warnf("route %s node %s: %v", r.Name, n.Name, err)
				unregisterNodeRewriters(r.Name, n.Name)
				continue
			}
			return nil, fmt.Errorf("node %q: %w", n.Name, err)
		}
		plans = append(plans, nodePlan{provider: p, node: n, targets: targets})
	}

	plan := &routePlan{hops: make(map[provider.API]hop.Hop, len(inboundGroups)+1)}
	def, err := buildVariantHop(r, "", plans, log, facts, captureMgr)
	if err != nil {
		return nil, err
	}
	plan.hops[""] = def

	needed := make(map[provider.API]bool)
	for _, np := range plans {
		for api := range np.targets.variants {
			needed[api] = true
		}
	}
	for _, group := range inboundGroups {
		if !needed[group.api] {
			continue
		}
		h, err := buildVariantHop(r, group.api, plans, log, facts, captureMgr)
		if err != nil {
			return nil, err
		}
		plan.hops[group.api] = h
	}
	return plan, nil
}

// buildVariantHop compiles one route variant. inbound is empty for the fallback
// variant; otherwise every node uses its per-protocol target when it has one.
func buildVariantHop(r gwconfig.Route, inbound provider.API, plans []nodePlan, log logger.Logger, facts *stats.Facts, captureMgr *capture.Manager) (hop.Hop, error) {
	variant := variantKey(inbound)
	var nodes []*xconfig.NodeConfig
	for _, np := range plans {
		target := np.targets.defaultTarget
		if inbound != "" {
			if t, ok := np.targets.variants[inbound]; ok {
				target = t
			}
		}
		name := rewriterName(r.Name, variant, np.node.Name)
		if err := registerRewriter(name, np.provider, np.node, target, r.Name, facts, captureMgr, log); err != nil {
			return nil, err
		}
		ns, err := buildNodes(np.provider, np.node, r.Name, target, name)
		if err != nil {
			return nil, fmt.Errorf("node %q: %w", np.node.Name, err)
		}
		nodes = append(nodes, ns...)
	}

	strategy := r.Strategy
	if strategy == "" {
		strategy = "round"
	}
	return hop_parser.ParseHop(&xconfig.HopConfig{
		Name: "route/" + r.Name + "/" + variant,
		Selector: &xconfig.SelectorConfig{
			Strategy: strategy,
		},
		Nodes: nodes,
	}, log)
}

func buildNodes(p gwconfig.Provider, n gwconfig.Node, route string, target upstreamTarget, rewriter string) ([]*xconfig.NodeConfig, error) {
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
		// The x forwarder cannot rewrite compressed SSE. Drop the client's
		// Accept-Encoding so net/http negotiates gzip itself and transparently
		// decompresses the upstream body before the body rewriter sees it.
		// This is a transport concern, not an auth override, so it must win
		// over auth.Extra as well.
		headers["Accept-Encoding"] = ""

		name := fmt.Sprintf("%s/%s/%s", route, p.Name, n.Name)
		if len(keys) > 1 {
			name = fmt.Sprintf("%s/%d", name, i)
		}

		bodyRewrites := []xconfig.HTTPBodyRewriteConfig{{
			Rewriter: rewriter,
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
				RewriteResponseHeader: []xconfig.HTTPHeaderRewriteConfig{{
					Rewriter: gwrewriter.RequestIDRewriterName,
				}},
				FailCodes: "429,5xx",
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
	pattern, catchAll, err := modelGlobPattern(match)
	if err != nil {
		return nil, err
	}
	if catchAll {
		return nil, nil
	}
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

// variantKey names one route variant for rewriter keys and hop names.
func variantKey(api provider.API) string {
	if api == "" {
		return "default"
	}
	return string(api)
}

// rewriterName returns the registry key for a route-scoped node engine. The
// same node can select different target APIs in different routes and protocol
// variants, so engines cannot be shared. Route and node names forbid '/', and
// the variant is one of a fixed set, so the key cannot collide.
func rewriterName(route, variant, node string) string {
	return "r/" + route + "/" + variant + "/" + node
}

func registerHeaderRewriter() error {
	name := gwrewriter.RequestIDRewriterName
	registry.RewriterRegistry().Unregister(name)
	if err := registry.RewriterRegistry().Register(name, &gwrewriter.HeaderRewriter{}); err != nil {
		return fmt.Errorf("register header rewriter %s: %w", name, err)
	}
	return nil
}

// unregisterNodeRewriters removes every route-variant rewriter for a node that
// is no longer compiled, such as a newly disabled target model.
func unregisterNodeRewriters(route, node string) {
	variants := []string{variantKey("")}
	for _, group := range inboundGroups {
		variants = append(variants, variantKey(group.api))
	}
	for _, variant := range variants {
		registry.RewriterRegistry().Unregister(rewriterName(route, variant, node))
	}
}

// registerRewriter installs the in-process body rewriter for one route variant
// and node. It must run before ParseHop because node parsing resolves rewriters
// from the registry.
func registerRewriter(name string, p gwconfig.Provider, n gwconfig.Node, target upstreamTarget, route string, facts *stats.Facts, captureMgr *capture.Manager, log logger.Logger) error {
	registry.RewriterRegistry().Unregister(name)
	if err := registry.RewriterRegistry().Register(name, gwrewriter.New(newNodeEngine(target), gwrewriter.Options{
		Provider: p.Name,
		Protocol: string(target.api),
		Route:    route,
		Node:     n.Name,
		Sink:     facts,
		Capture:  captureMgr,
		Logger:   log,
	})); err != nil {
		return fmt.Errorf("register rewriter %s: %w", name, err)
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
