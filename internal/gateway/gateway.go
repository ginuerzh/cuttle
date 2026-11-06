package gateway

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
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

	"github.com/go-gost/llm-gw/internal/capture"
	gwconfig "github.com/go-gost/llm-gw/internal/config"
	"github.com/go-gost/llm-gw/internal/stats"
)

// Gateway owns the data plane: one service, one forward handler and a hop
// group that can be replaced at runtime.
type Gateway struct {
	mu        sync.Mutex
	cfg       *gwconfig.Config
	log       logger.Logger
	router    *xchain.Router
	handler   handler.Handler
	forwarder handler.Forwarder
	hop       hop.Hop
	ln        listener.Listener
	svc       service.Service
	facts     *stats.Facts
	store     *stats.Store
	capture   *capture.Manager
	cancel    context.CancelFunc
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

	opts := []handler.Option{
		handler.RouterOption(router),
		handler.ServiceOption("llm-gw"),
		handler.LoggerOption(log),
	}
	if store != nil {
		opts = append(opts, handler.RecordersOption(recorder.RecorderObject{
			Recorder: stats.NewRecorder(store, facts, log),
			Record:   xrecorder.RecorderServiceHandler,
		}))
	}
	h := forwardlocal.NewHandler(opts...)
	if err := h.Init(xmetadata.NewMetadata(map[string]any{
		"sniffing":       true,
		"readTimeout":    "15s",
		"http.keepalive": false,
	})); err != nil {
		return nil, err
	}

	fwd, ok := h.(handler.Forwarder)
	if !ok {
		return nil, errors.New("gateway: handler does not support Forward")
	}

	g := &Gateway{
		cfg:       cfg,
		log:       log,
		router:    router,
		handler:   h,
		forwarder: fwd,
		facts:     facts,
		store:     store,
		capture:   capture.NewManager(capturePolicy(cfg.Stats.Capture)),
	}
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
		store.StartCleanup(ctx, time.Hour, retainRequests, retainRollups)
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
	g.svc = xservice.NewService("llm-gw", ln, h, xservice.LoggerOption(log))
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

func capturePolicy(c gwconfig.Capture) capture.Policy {
	return capture.Policy{
		Mode:       c.Mode,
		SampleRate: c.SampleRate,
		MaxBytes:   c.MaxBytes,
		Retain:     time.Duration(c.Retain),
	}
}

// Reload builds a new hop group and swaps it in. On build errors the old
// group keeps serving.
func (g *Gateway) Reload(cfg *gwconfig.Config) error {
	return g.apply(cfg)
}

func (g *Gateway) apply(cfg *gwconfig.Config) error {
	group, err := buildHopGroup(cfg, g.log, g.facts)
	if err != nil {
		return err
	}

	g.mu.Lock()
	old := g.hop
	g.hop = group
	g.cfg = cfg
	g.mu.Unlock()

	g.forwarder.Forward(group)
	if g.capture != nil {
		g.capture.SetPolicy(capturePolicy(cfg.Stats.Capture))
	}

	// In-flight requests hold the old nodes; give them time to finish before
	// closing probe goroutines and node resources.
	if closer, ok := old.(io.Closer); ok {
		time.AfterFunc(30*time.Second, func() { closer.Close() })
	}
	return nil
}

// Stats returns the statistics store, or nil when disabled.
func (g *Gateway) Stats() *stats.Store {
	return g.store
}

// Close stops serving and releases the listener, hop and router.
func (g *Gateway) Close() error {
	var err error
	if g.svc != nil {
		err = g.svc.Close()
	}

	g.mu.Lock()
	h := g.hop
	g.hop = nil
	g.mu.Unlock()

	if closer, ok := h.(io.Closer); ok {
		closer.Close()
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
