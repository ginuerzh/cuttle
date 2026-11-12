package gateway

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	corechain "github.com/go-gost/core/chain"
	"github.com/go-gost/core/handler"
	"github.com/go-gost/core/hop"
	"github.com/go-gost/core/listener"
	"github.com/go-gost/core/logger"
	"github.com/go-gost/core/recorder"
	"github.com/go-gost/core/service"
	xchain "github.com/go-gost/x/chain"
	forwardlocal "github.com/go-gost/x/handler/forward/local"
	tcplistener "github.com/go-gost/x/listener/tcp"
	xlogger "github.com/go-gost/x/logger"
	xmetadata "github.com/go-gost/x/metadata"
	xrecorder "github.com/go-gost/x/recorder"
	xservice "github.com/go-gost/x/service"

	"github.com/ginuerzh/cuttle/internal/authfail"
	"github.com/ginuerzh/cuttle/internal/capture"
	gwconfig "github.com/ginuerzh/cuttle/internal/config"
	"github.com/ginuerzh/cuttle/internal/health"
	"github.com/ginuerzh/cuttle/internal/identity"
	"github.com/ginuerzh/cuttle/internal/stats"
)

// Gateway owns the data plane: one service, one forward handler and a hop
// group that can be replaced at runtime.
type Gateway struct {
	buildMu      sync.Mutex
	mu           sync.Mutex
	cfg          *gwconfig.Config
	log          logger.Logger
	router       *xchain.Router
	handler      handler.Handler
	forwarder    handler.Forwarder
	hop          hop.Hop
	probes       *probeSet
	rewriters    []string
	countTokens  atomic.Pointer[countTokensPolicy]
	auth         atomic.Pointer[authPolicy]
	ln           listener.Listener
	svc          service.Service
	facts        *stats.Facts
	store        *stats.Store
	capture      *capture.Manager
	authFailures *authfail.Tracker
	cancel       context.CancelFunc
	// statsMigrationDone records the one identity-migration attempt this
	// process makes; a failure is reported once and stays visible through the
	// admin health endpoint instead of retrying on every reload.
	statsMigrationDone bool
}

// New builds the data plane from cfg. The listener is bound but not serving
// until Start is called.
func New(cfg *gwconfig.Config, log logger.Logger) (*Gateway, error) {
	if log == nil {
		log = xlogger.Nop()
	}

	router := xchain.NewRouter(
		corechain.TimeoutRouterOption(30*time.Second),
		corechain.LoggerRouterOption(log),
	)

	facts := stats.NewFacts()
	var store *stats.Store
	if cfg.Stats.Path != "" {
		var err error
		store, err = stats.Open(cfg.Stats.Path)
		if err != nil {
			return nil, err
		}
	}

	capMgr := capture.NewManager(capturePolicy(cfg.Stats.Capture))

	opts := []handler.Option{
		handler.RouterOption(router),
		handler.ServiceOption("cuttle"),
		handler.LoggerOption(log),
	}
	if store != nil {
		opts = append(opts, handler.RecordersOption(recorder.RecorderObject{
			Recorder: stats.NewRecorder(store, facts, log, capMgr),
			Record:   xrecorder.RecorderServiceHandler,
		}))
	}
	base := forwardlocal.NewHandler(opts...)

	g := &Gateway{
		cfg:          cfg,
		log:          log,
		router:       router,
		facts:        facts,
		store:        store,
		capture:      capMgr,
		authFailures: authfail.New(authfail.DefaultLimit),
	}

	ct := newCountTokensHandler(base, &g.countTokens, log)
	h := newHelloHandler(newGatewayAuthHandler(ct, &g.auth, g.authFailures, log), log)
	readTimeout := time.Duration(cfg.Gateway.ReadTimeout)
	if readTimeout <= 0 {
		readTimeout = 60 * time.Second
	}
	if err := h.Init(xmetadata.NewMetadata(map[string]any{
		"sniffing":       true,
		"readTimeout":    readTimeout.String(),
		"http.keepalive": false,
		"clientID":       clientIDFromRequest,
	})); err != nil {
		return nil, err
	}

	fwd, ok := base.(handler.Forwarder)
	if !ok {
		return nil, errors.New("gateway: handler does not support Forward")
	}
	g.handler = h
	g.forwarder = fwd
	if err := g.apply(cfg); err != nil {
		if store != nil {
			store.Close()
		}
		return nil, err
	}

	if store != nil {
		ctx, cancel := context.WithCancel(context.Background())
		retainRequests := time.Duration(cfg.Stats.RetainRequests)
		if retainRequests <= 0 {
			retainRequests = 168 * time.Hour
		}
		retainRollups := time.Duration(cfg.Stats.RetainRollups)
		if retainRollups <= 0 {
			retainRollups = 2160 * time.Hour
		}
		retainCaptures := time.Duration(cfg.Stats.Capture.Retain)
		if retainCaptures <= 0 {
			retainCaptures = 24 * time.Hour
		}
		store.StartCleanup(ctx, time.Hour, retainRequests, retainRollups, retainCaptures)
		g.cancel = cancel
	}

	ln := tcplistener.NewListener(
		listener.AddrOption(cfg.Gateway.Listen),
		listener.LoggerOption(log),
	)
	if err := ln.Init(xmetadata.NewMetadata(nil)); err != nil {
		return nil, err
	}
	g.ln = ln
	g.svc = xservice.NewService("cuttle", ln, h, xservice.LoggerOption(log))
	return g, nil
}

// Start begins serving. It returns once the service goroutine is started.
func (g *Gateway) Start() error {
	go func() {
		if err := g.svc.Serve(); err != nil {
			g.log.Errorf("serve: %v", err)
		}
	}()
	g.log.Infof("listening on %s", g.ln.Addr())
	return nil
}

// Addr returns the bound listener address.
func (g *Gateway) Addr() net.Addr {
	return g.ln.Addr()
}

// Capture returns the body-capture manager.
func (g *Gateway) Capture() *capture.Manager {
	return g.capture
}

// AuthFailures returns the bounded in-memory record of rejected data-plane
// authentication attempts.
func (g *Gateway) AuthFailures() *authfail.Tracker {
	return g.authFailures
}

// RouteHealth returns the current node marker state for each configured
// route. Markers are in-memory x state and reset when the hop is rebuilt.
// Routes are identified by id; the display names stay in the payload for the
// console.
func (g *Gateway) RouteHealth() []health.Route {
	g.mu.Lock()
	h := g.hop
	cfg := g.cfg
	g.mu.Unlock()
	if h == nil || cfg == nil {
		return nil
	}

	nl, ok := h.(hop.NodeList)
	if !ok {
		return nil
	}
	type routeNodeKey struct {
		routeID string
		nodeID  string
	}
	nodes := make(map[routeNodeKey]*corechain.Node)
	for _, n := range nl.Nodes() {
		if n == nil {
			continue
		}
		nodeID := configNodeID(n)
		if nodeID == "" {
			continue
		}
		key := routeNodeKey{configRouteID(n), nodeID}
		prev := nodes[key]
		if prev == nil || markerCount(n) > markerCount(prev) ||
			(markerCount(n) == markerCount(prev) && markerTime(n).After(markerTime(prev))) {
			nodes[key] = n
		}
	}

	out := make([]health.Route, 0, len(cfg.Routes))
	for _, r := range cfg.Routes {
		maxFails := r.MaxFails
		if maxFails <= 0 {
			maxFails = 1
		}
		failTimeout := time.Duration(r.FailTimeout)
		if failTimeout <= 0 {
			failTimeout = 10 * time.Second
		}
		hr := health.Route{
			ID:          r.ID,
			Name:        r.Name,
			MaxFails:    maxFails,
			FailTimeout: failTimeout.String(),
			Nodes:       make([]health.Node, 0, len(r.Nodes)),
		}
		for _, ref := range r.Nodes {
			configured, ok := cfg.Node(ref)
			if !ok {
				continue
			}
			n := nodes[routeNodeKey{r.ID, configured.ID}]
			if n == nil {
				n = nodes[routeNodeKey{"", configured.ID}]
			}
			if n == nil {
				continue
			}
			m := n.Marker()
			if m == nil {
				continue
			}
			count := m.Count()
			last := m.Time()
			dead := count >= int64(maxFails) && !last.IsZero() && time.Since(last) < failTimeout
			hn := health.Node{
				ID:        configured.ID,
				Name:      configured.Name,
				FailCount: count,
				Dead:      dead,
			}
			if p, ok := cfg.Provider(configured.Provider); ok {
				hn.ProviderID = p.ID
			}
			if !last.IsZero() {
				lastCopy := last
				hn.LastFailure = &lastCopy
			}
			if dead {
				until := last.Add(failTimeout)
				hn.DeadUntil = &until
			}
			hr.Nodes = append(hr.Nodes, hn)
		}
		out = append(out, hr)
	}
	return out
}

// EntrypointHealth returns the binding health of the current build: one row per
// entrypoint with bindings, in configuration order. Draft entrypoints have no
// bindings and no probe runner, so they are absent.
func (g *Gateway) EntrypointHealth() []health.Entrypoint {
	g.mu.Lock()
	probes := g.probes
	g.mu.Unlock()
	if probes == nil {
		return nil
	}
	return probes.Snapshot()
}

func configNodeID(n *corechain.Node) string {
	if n == nil {
		return ""
	}
	if md := n.Metadata(); md != nil {
		if v, ok := md.Get("configNode").(string); ok && v != "" {
			return v
		}
	}
	return n.Name
}

func configRouteID(n *corechain.Node) string {
	if n == nil {
		return ""
	}
	if md := n.Metadata(); md != nil {
		if v, ok := md.Get("configRoute").(string); ok {
			return v
		}
	}
	return ""
}

func markerCount(n *corechain.Node) int64 {
	if n == nil || n.Marker() == nil {
		return 0
	}
	return n.Marker().Count()
}

func markerTime(n *corechain.Node) time.Time {
	if n == nil || n.Marker() == nil {
		return time.Time{}
	}
	return n.Marker().Time()
}

func capturePolicy(c gwconfig.Capture) capture.Policy {
	rate := 0.0
	if c.SampleRate != nil {
		rate = *c.SampleRate
	}
	return capture.Policy{
		Mode:       c.Mode,
		SampleRate: rate,
		MaxBytes:   c.MaxBytes,
		Retain:     time.Duration(c.Retain),
	}
}

// statsMigrationTimeout bounds the one-time v1 identity rewrite, including the
// VACUUM INTO backup that copies the whole database.
const statsMigrationTimeout = 5 * time.Minute

// Reload builds a new hop group and swaps it in. On build errors the old
// group keeps serving.
func (g *Gateway) Reload(cfg *gwconfig.Config) error {
	return g.apply(cfg)
}

func (g *Gateway) apply(cfg *gwconfig.Config) error {
	// Serialize builds so two reloads cannot interleave registry writes.
	g.buildMu.Lock()
	defer g.buildMu.Unlock()

	build := nextBuildGeneration()
	group, probes, rewriters, policy, err := buildHopGroup(cfg, build, g.log, g.facts, g.capture)
	if err != nil {
		// The old hop is untouched; drop the rewriters this failed build
		// registered so they cannot leak.
		unregisterRewriters(rewriters)
		return err
	}

	g.mu.Lock()
	old := g.hop
	oldProbes := g.probes
	oldRewriters := g.rewriters
	g.hop = group
	g.probes = probes
	g.cfg = cfg
	g.rewriters = rewriters
	g.mu.Unlock()

	// The hop swap is done, so the old probe runner must stop now: it marks the
	// old entries only, and leaving it alive would keep forking commands for a
	// build that no longer serves. The new set starts immediately so the first
	// request already sees a verdict for every probed binding.
	oldProbes.Stop()
	probes.Start(context.Background())

	ap := newAuthPolicy(cfg.Gateway.Keys)
	g.countTokens.Store(policy)
	g.auth.Store(ap)
	if ap.required && !ap.hasEnabled() {
		g.log.Warnf("gateway keys: all configured keys are disabled; all data-plane requests will be rejected")
	}
	g.forwarder.Forward(group)
	if g.capture != nil {
		g.capture.SetPolicy(capturePolicy(cfg.Stats.Capture))
	}
	if g.store != nil {
		g.store.SetCaptureRetain(time.Duration(cfg.Stats.Capture.Retain))
		if !g.statsMigrationDone {
			g.statsMigrationDone = true
			g.migrateStatsIdentities(cfg)
		}
		// Migration is idempotent; syncing labels on every apply is what keeps
		// a rename visible in the history of an unchanged id.
		if err := g.store.SyncLabels(context.Background(), cfg.LabelsByID()); err != nil {
			g.log.Warnf("stats: syncing entity labels: %v", err)
		}
	}

	// In-flight requests hold the old nodes; give them time to finish before
	// closing probe goroutines and releasing the old generation's rewriters.
	if old != nil {
		time.AfterFunc(30*time.Second, func() {
			if closer, ok := old.(io.Closer); ok {
				closer.Close()
			}
			unregisterRewriters(oldRewriters)
		})
	}
	return nil
}

// Stats returns the statistics store, or nil when disabled.
func (g *Gateway) Stats() *stats.Store {
	return g.store
}

// migrateStatsIdentities runs the one-time v1 rewrite and reports both
// outcomes. A failure is logged at error level and stays visible through
// stats.Store.MigrationError, which GET /api/health exposes, because a failed
// backup leaves a deliberately unmigrated database in place.
func (g *Gateway) migrateStatsIdentities(cfg *gwconfig.Config) {
	ctx, cancel := context.WithTimeout(context.Background(), statsMigrationTimeout)
	defer cancel()

	names := cfg.NameToIDs()
	err := g.store.MigrateIdentities(ctx, func(kind, name string) string {
		if id, ok := names[kind][name]; ok {
			return id
		}
		return identity.Derive(identity.Kind(kind), name)
	})
	if err != nil {
		g.log.Errorf("stats: entity id migration failed, history keeps v1 names until it succeeds: %v; GET /api/health reports statsMigrationError", err)
		return
	}
	if backup, _ := g.store.MigrationStatus(); backup != "" {
		g.log.Infof("stats: migrated entity ids, backup %s", backup)
	}
}

// Close stops serving and releases the listener, hop and router.
func (g *Gateway) Close() error {
	g.buildMu.Lock()
	defer g.buildMu.Unlock()

	var err error
	if g.svc != nil {
		err = g.svc.Close()
	}

	g.mu.Lock()
	h := g.hop
	names := g.rewriters
	probes := g.probes
	g.hop = nil
	g.probes = nil
	g.rewriters = nil
	g.mu.Unlock()

	// Stop the probe runner before releasing the hop it marks.
	probes.Stop()

	if closer, ok := h.(io.Closer); ok {
		closer.Close()
	}
	// Give in-flight requests the same drain window used by Reload before
	// releasing the current generation's rewriters.
	if len(names) > 0 {
		time.AfterFunc(30*time.Second, func() { unregisterRewriters(names) })
	}
	if g.router != nil {
		g.router.Close()
	}
	if g.cancel != nil {
		g.cancel()
	}
	if g.store != nil {
		g.store.Close()
	}
	return err
}
