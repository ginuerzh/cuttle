package gateway

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/go-gost/core/hop"
	"github.com/go-gost/core/logger"
	corerewriter "github.com/go-gost/core/rewriter"
	corerouting "github.com/go-gost/core/routing"
	xconfig "github.com/go-gost/x/config"
	hop_parser "github.com/go-gost/x/config/parsing/hop"
	xhop "github.com/go-gost/x/hop"
	"github.com/go-gost/x/registry"
	xrouting "github.com/go-gost/x/routing"
	xs "github.com/go-gost/x/selector"

	"github.com/ginuerzh/llm-api-converter/engine"

	"github.com/ginuerzh/cuttle/internal/capture"
	gwconfig "github.com/ginuerzh/cuttle/internal/config"
	"github.com/ginuerzh/cuttle/internal/provider"
	gwrewriter "github.com/ginuerzh/cuttle/internal/rewriter"
	"github.com/ginuerzh/cuttle/internal/stats"
	"github.com/ginuerzh/cuttle/internal/version"

	_ "github.com/go-gost/x/connector/http"
	_ "github.com/go-gost/x/dialer/tcp"
)

// buildSeq namespaces rewriters per build so an old hop keeps resolving its own
// targets while a new build is being prepared. x's rewriter registry resolves
// names lazily, so reusing a name would mutate the live data plane before the
// hop swap.
var buildSeq atomic.Uint64

func nextBuildGeneration() string {
	return strconv.FormatUint(buildSeq.Add(1), 10)
}

// buildHopGroup compiles the configured routes into a hop group and emits one
// entry per entrypoint and inbound protocol group, in declaration order,
// followed by the entrypoint's fallback entry. Each entry wraps an inner group
// over the entrypoint's ordered route bindings, so a probe can make a binding
// ineligible without touching the outer group. The FIFO group selector then
// makes entrypoint order the single precedence rule. The returned probe set is
// unstarted; the caller starts and stops it. The returned names are the
// rewriters registered for this build; the caller owns their lifecycle.
func buildHopGroup(cfg *gwconfig.Config, build string, log logger.Logger, facts *stats.Facts, captureMgr *capture.Manager) (hop.Hop, *probeSet, []string, *countTokensPolicy, error) {
	if err := registerHeaderRewriter(); err != nil {
		return nil, nil, nil, nil, err
	}

	probes := newProbeSet(func(id string) string {
		if r, ok := cfg.Route(id); ok {
			return r.Name
		}
		return id
	}, log)

	routePlans := make(map[string]*routePlan, len(cfg.Routes))
	var rewriters []string
	entries := make([]*xhop.HopEntry, 0, len(cfg.Entrypoints)*(len(inboundGroups)+1))
	for _, e := range cfg.Entrypoints {
		if len(e.Routes) == 0 {
			// A draft entrypoint compiles against the shared zero-node hop and
			// gains no inner group. It keeps its matcher and its place in
			// declaration order, which is what stops it from falling through
			// to a later entrypoint.
			plan, ok := routePlans[draftPlanKey]
			if !ok {
				var err error
				plan, err = buildDraftPlan(log)
				if err != nil {
					return nil, nil, rewriters, nil, fmt.Errorf("entrypoint %q: %w", e.Name, err)
				}
				routePlans[draftPlanKey] = plan
			}
			log.Warnf("entrypoint %s: no routes, compiled as a draft", e.Name)
			m, err := buildEntrypointMatcher(e.Match)
			if err != nil {
				return nil, nil, rewriters, nil, fmt.Errorf("entrypoint %q: %w", e.Name, err)
			}
			entries = append(entries, xhop.NewHopEntry(plan.hops[""], m, nil))
			continue
		}

		for i := range e.Routes {
			ref := strings.TrimSpace(e.Routes[i].Route)
			r, ok := cfg.Route(ref)
			if !ok {
				return nil, nil, rewriters, nil, fmt.Errorf("entrypoint %q: routes[%d]: unknown route %q", e.Name, i, ref)
			}
			if _, ok := routePlans[r.ID]; ok {
				continue
			}
			plan, err := buildRoutePlan(cfg, build, *r, log, facts, captureMgr)
			if plan != nil {
				rewriters = append(rewriters, plan.rewriters...)
			}
			if err != nil {
				return nil, nil, rewriters, nil, fmt.Errorf("route %q: %w", r.Name, err)
			}
			routePlans[r.ID] = plan
		}

		// One probe per binding must control every protocol variant the
		// binding compiles to, so the variant entries are collected per
		// binding index before the probe is registered.
		failTimeout := bindingFailTimeout(e.Routes)
		bindingEntries := make([][]*xhop.HopEntry, len(e.Routes))
		for _, group := range inboundGroups {
			if !bindingsHaveVariant(e.Routes, routePlans, group.api) {
				continue
			}
			m, err := buildProtocolMatcher(e.Match, group.pathPattern)
			if err != nil {
				return nil, nil, rewriters, nil, fmt.Errorf("entrypoint %q: %w", e.Name, err)
			}
			inner, variantEntries, err := buildBindingsHop(e.ID, e.Routes, routePlans, group.api, failTimeout, log)
			if err != nil {
				return nil, nil, rewriters, nil, err
			}
			entries = append(entries, xhop.NewHopEntry(inner, m, nil))
			for i := range variantEntries {
				bindingEntries[i] = append(bindingEntries[i], variantEntries[i])
			}
		}

		m, err := buildEntrypointMatcher(e.Match)
		if err != nil {
			return nil, nil, rewriters, nil, fmt.Errorf("entrypoint %q: %w", e.Name, err)
		}
		inner, fallbackEntries, err := buildBindingsHop(e.ID, e.Routes, routePlans, "", failTimeout, log)
		if err != nil {
			return nil, nil, rewriters, nil, err
		}
		entries = append(entries, xhop.NewHopEntry(inner, m, nil))
		for i := range fallbackEntries {
			bindingEntries[i] = append(bindingEntries[i], fallbackEntries[i])
		}

		for i, b := range e.Routes {
			probes.add(e, b, i, bindingEntries[i], failTimeout)
		}
	}

	policy, err := buildCountTokensPolicy(cfg, routePlans)
	if err != nil {
		return nil, nil, rewriters, nil, err
	}

	return xhop.NewHopGroup(
		xhop.WithEntriesOption(entries...),
		// FIFO with no filters on purpose: a FailFilter here would let an
		// entrypoint whose bindings are all ineligible fall through to the next
		// entrypoint. Cross-entrypoint fallthrough is forbidden; the inner
		// per-entrypoint group owns binding eligibility.
		xhop.WithGroupSelectorOption(xs.NewSelector(xs.FIFOStrategy[hop.Hop]())),
		xhop.WithGroupLoggerOption(log.WithFields(map[string]any{
			"kind": "hopGroup",
		})),
	), probes, rewriters, policy, nil
}

// bindingsHaveVariant reports whether any bound route compiles a hop for the
// inbound protocol group. When none does, the entrypoint falls through to its
// fallback entry, exactly as a single-route entrypoint did before bindings.
func bindingsHaveVariant(routes []gwconfig.RouteBinding, plans map[string]*routePlan, api provider.API) bool {
	for _, b := range routes {
		plan := plans[strings.TrimSpace(b.Route)]
		if plan == nil {
			continue
		}
		if _, ok := plan.hops[api]; ok {
			return true
		}
	}
	return false
}

// bindingFailTimeout derives the inner binding group's fail timeout from the
// probe intervals: twice the longest interval, floored at 10s. A probe that
// keeps failing re-marks on every run, so the timeout must outlast one interval
// or eligibility would flap between probe runs.
func bindingFailTimeout(bindings []gwconfig.RouteBinding) time.Duration {
	timeout := 10 * time.Second
	for _, b := range bindings {
		if b.Probe == nil {
			continue
		}
		if d := 2 * time.Duration(b.Probe.Interval); d > timeout {
			timeout = d
		}
	}
	return timeout
}

// buildBindingsHop compiles one entrypoint's ordered route bindings into the
// inner hop group for one inbound variant. A route without a protocol-specific
// variant serves the inbound through its default hop, which is what the
// entrypoint fallback entry did before bindings existed.
//
// The returned entries are the per-binding entries, index-aligned with
// bindings; the probe runner marks and resets them. The group selector is FIFO
// plus FailFilter(1, failTimeout): the first eligible binding in order wins,
// and x falls back to the first binding when every entry is ineligible.
func buildBindingsHop(entrypointID string, bindings []gwconfig.RouteBinding, plans map[string]*routePlan, api provider.API, failTimeout time.Duration, log logger.Logger) (hop.Hop, []*xhop.HopEntry, error) {
	if len(bindings) == 0 {
		return nil, nil, fmt.Errorf("entrypoint %q: no route bindings", entrypointID)
	}

	variant := variantKey(api)
	entries := make([]*xhop.HopEntry, 0, len(bindings))
	for i, b := range bindings {
		plan := plans[strings.TrimSpace(b.Route)]
		if plan == nil {
			return nil, nil, fmt.Errorf("entrypoint %q: routes[%d]: unknown route %q", entrypointID, i, b.Route)
		}
		h, ok := plan.hops[api]
		if !ok {
			h = plan.hops[""]
		}
		if h == nil {
			return nil, nil, fmt.Errorf("entrypoint %q: routes[%d]: route %q has no hop", entrypointID, i, b.Route)
		}
		entries = append(entries, xhop.NewHopEntry(h, nil, nil))
	}

	return xhop.NewHopGroup(
		xhop.WithEntriesOption(entries...),
		xhop.WithGroupSelectorOption(xs.NewSelector(
			xs.FIFOStrategy[hop.Hop](),
			xs.FailFilter[hop.Hop](1, failTimeout),
		)),
		xhop.WithGroupLoggerOption(log.WithFields(map[string]any{
			"kind": "hopGroup",
			"name": "entrypoint/" + entrypointID + "/" + variant,
		})),
	), entries, nil
}

// routePlan holds the compiled hops for one route: the fallback variant under
// the empty API key and one variant per inbound protocol that changes at least
// one node's target API. rewriters are the names registered by this plan.
type routePlan struct {
	hops                map[provider.API]hop.Hop
	rewriters           []string
	countTokensEstimate bool
}

// draftPlanKey is the routePlans key of the shared plan compiled for
// entrypoints that have no route yet. Route ids are never empty, so it cannot
// collide with a real route.
const draftPlanKey = ""

// buildDraftPlan compiles the hop shared by every draft entrypoint. It has no
// nodes, so selecting from it yields no node and the request fails; the
// entrypoint keeps its matcher and its place in declaration order, which is
// what stops a draft from falling through to a later entrypoint.
func buildDraftPlan(log logger.Logger) (*routePlan, error) {
	h, err := hop_parser.ParseHop(&xconfig.HopConfig{
		Name: "route/draft",
		Selector: &xconfig.SelectorConfig{
			Strategy:    "round",
			MaxFails:    1,
			FailTimeout: 10 * time.Second,
		},
	}, log)
	if err != nil {
		return nil, err
	}
	return &routePlan{hops: map[provider.API]hop.Hop{"": h}}, nil
}

// nodePlan is one enabled node and its compiled target plan.
type nodePlan struct {
	provider gwconfig.Provider
	node     gwconfig.Node
	targets  nodeTargets
	keys     []gwconfig.ProviderKey
}

func buildRoutePlan(cfg *gwconfig.Config, build string, r gwconfig.Route, log logger.Logger, facts *stats.Facts, captureMgr *capture.Manager) (*routePlan, error) {
	var plans []nodePlan
	for _, ref := range r.Nodes {
		n, ok := cfg.Node(ref)
		if !ok {
			return nil, fmt.Errorf("unknown node %q", ref)
		}
		if !n.EnabledValue() {
			continue
		}
		if strings.TrimSpace(n.Provider) == "" {
			// A providerless node is a draft: it keeps its card and name but is
			// skipped until it is wired to a provider.
			log.Warnf("route %s node %s: no provider, skipped", r.Name, n.Name)
			continue
		}
		p, ok := cfg.Provider(n.Provider)
		if !ok {
			return nil, fmt.Errorf("node %q: unknown provider %q", n.Name, n.Provider)
		}
		if !p.EnabledValue() {
			continue
		}
		provider := *p
		if strings.TrimSpace(provider.UserAgent) == "" {
			provider.UserAgent = cfg.Gateway.UserAgent
		}
		targets, err := compileNodeTargets(provider, *n)
		if err != nil {
			if errors.Is(err, errModelDisabled) {
				log.Warnf("route %s node %s: %v", r.Name, n.Name, err)
				continue
			}
			return nil, fmt.Errorf("node %q: %w", n.Name, err)
		}
		keys, err := resolveNodeKeys(provider, *n)
		if err != nil {
			if errors.Is(err, errNodeKeyDisabled) {
				log.Warnf("route %s node %s: %v", r.Name, n.Name, err)
				continue
			}
			return nil, fmt.Errorf("node %q: %w", n.Name, err)
		}
		plans = append(plans, nodePlan{provider: provider, node: *n, targets: targets, keys: keys})
	}

	plan := &routePlan{
		hops:                make(map[provider.API]hop.Hop, len(inboundGroups)+1),
		countTokensEstimate: routeNeedsCountTokensEstimate(plans),
	}
	def, err := buildVariantHop(r, build, "", plans, log, facts, captureMgr, &plan.rewriters)
	if err != nil {
		return plan, err
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
		h, err := buildVariantHop(r, build, group.api, plans, log, facts, captureMgr, &plan.rewriters)
		if err != nil {
			return plan, err
		}
		plan.hops[group.api] = h
	}
	return plan, nil
}

// buildVariantHop compiles one route variant. inbound is empty for the fallback
// variant; otherwise every node uses its per-protocol target when it has one.
func buildVariantHop(r gwconfig.Route, build string, inbound provider.API, plans []nodePlan, log logger.Logger, facts *stats.Facts, captureMgr *capture.Manager, names *[]string) (hop.Hop, error) {
	variant := variantKey(inbound)
	var nodes []*xconfig.NodeConfig
	for _, np := range plans {
		target := np.targets.defaultTarget
		if inbound != "" {
			if t, ok := np.targets.variants[inbound]; ok {
				target = t
			}
		}
		// One engine per node and variant: a shared name would be
		// unregistered and replaced by the next node's engine, so every node
		// of the route would run the last node's target model.
		name := buildRewriterName(build, r.ID, variant, np.node.ID)
		if err := registerRewriter(name, np.provider, np.node, target, r.ID, facts, captureMgr, log); err != nil {
			return nil, err
		}
		*names = append(*names, name)
		ns, err := buildNodes(np.provider, np.node, np.keys, r, target, name)
		if err != nil {
			return nil, fmt.Errorf("node %q: %w", np.node.Name, err)
		}
		if r.Retries > 0 {
			// x reads the route retry policy from node metadata, and the
			// selector picks one node per request, so every node built for
			// the route (per-key and backup alike) must carry it. An explicit
			// default here keeps builds that bypass config.Load on the same
			// codes x would use anyway.
			codes := r.RetryCodes
			if codes == "" {
				codes = "429,5xx"
			}
			for _, n := range ns {
				if n.Metadata == nil {
					n.Metadata = make(map[string]any, 2)
				}
				n.Metadata["retry.max"] = r.Retries
				n.Metadata["retry.codes"] = codes
			}
		}
		nodes = append(nodes, ns...)
	}

	strategy := r.Strategy
	if strategy == "" {
		strategy = "round"
	}
	maxFails := r.MaxFails
	if maxFails <= 0 {
		maxFails = 1
	}
	failTimeout := time.Duration(r.FailTimeout)
	if failTimeout <= 0 {
		failTimeout = 10 * time.Second
	}
	return hop_parser.ParseHop(&xconfig.HopConfig{
		Name: "route/" + r.ID + "/" + variant,
		Selector: &xconfig.SelectorConfig{
			Strategy:    strategy,
			MaxFails:    maxFails,
			FailTimeout: failTimeout,
		},
		Nodes: nodes,
	}, log)
}

// errNodeKeyDisabled marks a node whose key set is empty, either because every
// provider key is disabled or because the pinned key is disabled.
var errNodeKeyDisabled = errors.New("node has no enabled key")

// resolveNodeKeys returns the provider keys a node should build. A pinned node
// names its key by id and uses exactly that key; an unpinned node uses every
// enabled key. A provider with no keys yields one keyless entry. A pinned id
// that does not exist is a regular error; an empty usable set is
// errNodeKeyDisabled.
func resolveNodeKeys(p gwconfig.Provider, n gwconfig.Node) ([]gwconfig.ProviderKey, error) {
	if pin := strings.TrimSpace(n.Key); pin != "" {
		for _, k := range p.Keys {
			if k.ID != pin {
				continue
			}
			if !k.EnabledValue() {
				return nil, fmt.Errorf("%w: %s is disabled", errNodeKeyDisabled, pin)
			}
			return []gwconfig.ProviderKey{k}, nil
		}
		return nil, fmt.Errorf("key %q is not configured on provider %q", pin, p.Name)
	}

	if len(p.Keys) == 0 {
		return []gwconfig.ProviderKey{{}}, nil
	}
	keys := make([]gwconfig.ProviderKey, 0, len(p.Keys))
	for _, k := range p.Keys {
		if k.EnabledValue() {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("%w: every provider key is disabled", errNodeKeyDisabled)
	}
	return keys, nil
}

// buildNodes compiles the x nodes for one provider node. The route, provider
// and node ids (plus the key id when the node spreads across several keys) form
// the x node name that stats parses back into entities. The node metadata keeps
// the route and node ids for route health.
func buildNodes(p gwconfig.Provider, n gwconfig.Node, keys []gwconfig.ProviderKey, route gwconfig.Route, target upstreamTarget, rewriter string) ([]*xconfig.NodeConfig, error) {
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

	nodes := make([]*xconfig.NodeConfig, 0, len(keys))
	for _, k := range keys {
		key := k.Key
		headers := make(map[string]string, len(auth.Extra)+len(p.Headers)+6)
		// The gateway owns upstream authentication. Inbound credentials, such
		// as the Authorization header Claude Code sends or an SDK's x-api-key,
		// must never be forwarded: some upstreams (DeepSeek's Anthropic
		// endpoint, for example) prefer Authorization over x-api-key, so a
		// leaked client token would turn the valid provider key into a 401.
		// Empty values delete the header in x's HTTP forwarder.
		for _, h := range []string{"Authorization", "x-api-key", "x-goog-api-key", "api-key"} {
			headers[h] = ""
		}
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
		// Own the upstream User-Agent. Forwarding an arbitrary client UA
		// (Python-urllib, for example) can trigger Cloudflare bot rules; a
		// stable gateway UA is safe and still leaves client attribution in
		// stats. Provider headers can override it, and auth.extra wins last.
		ua := strings.TrimSpace(p.UserAgent)
		if ua == "" {
			ua = "cuttle/" + version.Current().Version
		}
		headers["User-Agent"] = ua
		for k, v := range p.Headers {
			headers[http.CanonicalHeaderKey(k)] = v
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

		name := fmt.Sprintf("%s/%s/%s", route.ID, p.ID, n.ID)
		if len(keys) > 1 {
			name = fmt.Sprintf("%s/%s", name, k.ID)
		}

		bodyRewrites := []xconfig.HTTPBodyRewriteConfig{{
			Rewriter: rewriter,
			Type:     "*",
		}}

		var metadata map[string]any
		if n.Backup {
			metadata = map[string]any{"backup": true}
		}
		if metadata == nil {
			metadata = make(map[string]any)
		}
		// Keep the operator-facing node id and route id beside the internal
		// route/provider/node name used by x.
		metadata["configNode"] = n.ID
		metadata["configRoute"] = route.ID
		if n.Weight > 0 {
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
		if strings.EqualFold(strings.TrimSpace(p.Type), "opencode-go") {
			// Session affinity is an OpenCode Go requirement. Attach this
			// rewriter only to that provider's nodes; it must never run for
			// generic, CommandCode or DeepSeek providers.
			nodeCfg.HTTP.RewriteRequestHeader = []xconfig.HTTPHeaderRewriteConfig{{
				Rewriter: gwrewriter.OpenCodeSessionRewriterName,
			}}
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
// variants, so engines cannot be shared. Ids forbid '/', and the variant is one
// of a fixed set, so an id-based key cannot collide.
func rewriterName(route, variant, node string) string {
	return "r/" + route + "/" + variant + "/" + node
}

func registerHeaderRewriter() error {
	for _, item := range []struct {
		name     string
		rewriter corerewriter.Rewriter
	}{
		{gwrewriter.RequestIDRewriterName, &gwrewriter.HeaderRewriter{}},
		{gwrewriter.OpenCodeSessionRewriterName, &gwrewriter.OpenCodeSessionRewriter{}},
	} {
		registry.RewriterRegistry().Unregister(item.name)
		if err := registry.RewriterRegistry().Register(item.name, item.rewriter); err != nil {
			return fmt.Errorf("register header rewriter %s: %w", item.name, err)
		}
	}
	return nil
}

// buildRewriterName namespaces a rewriter by build generation. x resolves
// rewriter names lazily, so a stable name would let a new build mutate the old
// hop before the swap.
func buildRewriterName(build, route, variant, node string) string {
	return "b/" + build + "/" + rewriterName(route, variant, node)
}

// unregisterRewriters removes names registered by one build or retained for the
// drain window of a previous build.
func unregisterRewriters(names []string) {
	for _, name := range names {
		registry.RewriterRegistry().Unregister(name)
	}
}

// registerRewriter installs the in-process body rewriter for one route variant
// and node. It must run before ParseHop because node parsing resolves rewriters
// from the registry. The engine reports ids, so facts and captures are
// attributed by id.
func registerRewriter(name string, p gwconfig.Provider, n gwconfig.Node, target upstreamTarget, routeID string, facts *stats.Facts, captureMgr *capture.Manager, log logger.Logger) error {
	registry.RewriterRegistry().Unregister(name)
	if err := registry.RewriterRegistry().Register(name, gwrewriter.New(newNodeEngine(target), gwrewriter.Options{
		Provider:    p.ID,
		Protocol:    string(target.api),
		Route:       routeID,
		Node:        n.ID,
		TargetModel: target.targetModel,
		Sink:        facts,
		Capture:     captureMgr,
		Logger:      log,
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
