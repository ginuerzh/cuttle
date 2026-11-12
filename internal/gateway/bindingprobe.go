package gateway

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-gost/core/logger"
	xhop "github.com/go-gost/x/hop"
	xlogger "github.com/go-gost/x/logger"
	xs "github.com/go-gost/x/selector"

	gwconfig "github.com/ginuerzh/cuttle/internal/config"
	"github.com/ginuerzh/cuttle/internal/health"
)

// Probe defaults mirror the config defaults. A loaded config always carries
// them, but a build that bypasses config.Load must not fetch an infinite
// interval or a zero timeout.
const (
	defaultProbeInterval = 30 * time.Second
	defaultProbeTimeout  = 5 * time.Second
	// probeStderrLimit bounds the stderr head kept for the health payload: the
	// first 1 KiB is recorded, so a probe that spews output cannot grow the
	// process.
	probeStderrLimit = 1024
	// probeWaitDelay bounds how long a probe process's pipes may outlive the
	// process itself. A shell that leaves a grandchild holding the stderr pipe
	// would otherwise block the runner until that grandchild exits.
	probeWaitDelay = 500 * time.Millisecond
)

// probeSet owns every command probe of one build. buildHopGroup creates it and
// registers one probe per binding, each holding every protocol-variant entry of
// that binding, so one command decides the eligibility of all variants.
//
// Nothing in this file touches the stats store or node failCodes markers:
// binding eligibility is a separate, memory-only signal written only here.
type probeSet struct {
	routeName func(string) string
	log       logger.Logger

	// probes is immutable once buildHopGroup has returned, so Snapshot and
	// Start can read it without a lock.
	probes []*bindingProbe

	mu      sync.Mutex
	started bool
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

// probeState is the memory-only result of the last probe run.
type probeState struct {
	ran       bool
	ok        bool
	exitCode  int
	latencyMS int64
	err       string
	at        time.Time
}

// bindingProbe runs one binding's command and marks or resets the entries of
// every protocol variant the binding compiled to.
type bindingProbe struct {
	entrypointID   string
	entrypointName string
	index          int
	routeID        string
	routeName      string
	command        string
	interval       time.Duration
	timeout        time.Duration
	failTimeout    time.Duration
	entries        []*xhop.HopEntry
	log            logger.Logger

	mu    sync.Mutex
	state probeState
}

// probeGroup collects one entrypoint's probes in configuration order.
type probeGroup struct {
	id     string
	name   string
	probes []*bindingProbe
}

// newProbeSet creates an empty, unstarted set. routeName resolves a binding's
// route id to its display label, which the health payload and the command
// environment carry.
func newProbeSet(routeName func(string) string, log logger.Logger) *probeSet {
	if log == nil {
		log = xlogger.Nop()
	}
	if routeName == nil {
		routeName = func(id string) string { return id }
	}
	return &probeSet{routeName: routeName, log: log}
}

// add registers one binding. A binding without a probe is registered with an
// empty command: it never runs, always reports eligible, and appears in the
// health payload in selection priority order (probed bindings first).
func (s *probeSet) add(e gwconfig.Entrypoint, b gwconfig.RouteBinding, index int, entries []*xhop.HopEntry, failTimeout time.Duration) {
	if s == nil {
		return
	}

	var command string
	interval, timeout := defaultProbeInterval, defaultProbeTimeout
	if b.Probe != nil {
		command = strings.TrimSpace(b.Probe.Command)
		if d := time.Duration(b.Probe.Interval); d > 0 {
			interval = d
		}
		if d := time.Duration(b.Probe.Timeout); d > 0 {
			timeout = d
		}
	}
	if failTimeout <= 0 {
		failTimeout = 10 * time.Second
	}

	s.probes = append(s.probes, &bindingProbe{
		entrypointID:   e.ID,
		entrypointName: e.Name,
		index:          index,
		routeID:        strings.TrimSpace(b.Route),
		routeName:      s.routeName(strings.TrimSpace(b.Route)),
		command:        command,
		interval:       interval,
		timeout:        timeout,
		failTimeout:    failTimeout,
		entries:        entries,
		log:            s.log,
	})
}

// Start runs every command probe immediately and then once per interval. It is
// idempotent; Stop cancels the goroutines and waits for them. Start and Stop
// must not run concurrently: callers serialize them (the gateway does so under
// buildMu), because Stop drops its own lock before waiting.
func (s *probeSet) Start(ctx context.Context) {
	if s == nil {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started {
		return
	}
	s.started = true

	ctx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	for _, p := range s.probes {
		if p.command == "" {
			continue
		}
		s.wg.Add(1)
		go func(p *bindingProbe) {
			defer s.wg.Done()
			p.loop(ctx)
		}(p)
	}
}

// Stop cancels the probe goroutines and waits for them. A canceled run records
// no verdict, so a reload cannot make a fresh binding look failed. Like Start,
// it relies on the caller to serialize the two (buildMu in the gateway).
func (s *probeSet) Stop() {
	if s == nil {
		return
	}

	s.mu.Lock()
	cancel := s.cancel
	s.cancel = nil
	s.started = false
	s.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	s.wg.Wait()
}

// probeOnce runs every registered command exactly once, independent of how many
// protocol variants a binding compiled to.
func (s *probeSet) probeOnce(ctx context.Context) {
	if s == nil {
		return
	}
	for _, p := range s.probes {
		if p.command == "" {
			continue
		}
		p.run(ctx)
	}
}

// Snapshot reports binding health grouped by entrypoint. Each entrypoint's
// bindings are emitted in selection-priority order: probed bindings in
// configuration order, then probe-less ones in configuration order, with Index
// staying the configured position. Both Eligible and the active binding come
// from the entry markers with the same xs.IsFailed rule the inner hop group
// applies, so the payload can never name an active binding that it also reports
// ineligible. Fallback is true exactly when at least one binding carries a probe
// and none of the probed bindings is eligible: then the first probe-less binding
// serves, or the first probed binding when there is no probe-less one.
func (s *probeSet) Snapshot() []health.Entrypoint {
	if s == nil {
		return nil
	}

	groups := s.groups()
	out := make([]health.Entrypoint, 0, len(groups))
	for _, g := range groups {
		if len(g.probes) == 0 {
			continue
		}
		he := health.Entrypoint{
			ID:       g.id,
			Name:     g.name,
			Bindings: make([]health.Binding, 0, len(g.probes)),
		}

		// Read the markers once per binding: Eligible, the active candidate
		// and the fallback flag must come from the same read, or a probe
		// landing between two reads could make the payload contradict itself.
		failed := make([]bool, len(g.probes))
		hasProbe, probedEligible := false, false
		for i, p := range g.probes {
			failed[i] = p.failed()
			if p.command == "" {
				continue
			}
			hasProbe = true
			if !failed[i] {
				probedEligible = true
			}
		}

		order := probePriorityOrder(g.probes)
		active := order[0]
		for _, i := range order {
			if !failed[i] {
				active = i
				break
			}
		}
		he.Active = health.ActiveBinding{
			RouteID:  g.probes[active].routeID,
			Fallback: hasProbe && !probedEligible,
		}

		for _, i := range order {
			p := g.probes[i]
			hb := health.Binding{
				Index:     p.index,
				RouteID:   p.routeID,
				RouteName: p.routeName,
				Eligible:  !failed[i],
			}
			if last := p.last(); last != nil {
				hb.Last = last
			}
			he.Bindings = append(he.Bindings, hb)
		}
		out = append(out, he)
	}
	return out
}

// probePriorityOrder returns the health order of one entrypoint's probes: probed
// bindings first, then probe-less ones, each in configuration order. It mirrors
// bindingOrder, which cannot be reused here because the probe set does not see
// the configured bindings.
func probePriorityOrder(probes []*bindingProbe) []int {
	order := make([]int, 0, len(probes))
	plain := make([]int, 0, len(probes))
	for i, p := range probes {
		if p.command == "" {
			plain = append(plain, i)
			continue
		}
		order = append(order, i)
	}
	return append(order, plain...)
}

func (s *probeSet) groups() []probeGroup {
	out := make([]probeGroup, 0, len(s.probes))
	index := make(map[string]int, len(s.probes))
	for _, p := range s.probes {
		i, ok := index[p.entrypointID]
		if !ok {
			i = len(out)
			index[p.entrypointID] = i
			out = append(out, probeGroup{id: p.entrypointID, name: p.entrypointName})
		}
		out[i].probes = append(out[i].probes, p)
	}
	return out
}

func (p *bindingProbe) last() *health.Probe {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.state.ran {
		return nil
	}
	return &health.Probe{
		At:        p.state.at,
		OK:        p.state.ok,
		ExitCode:  p.state.exitCode,
		LatencyMS: p.state.latencyMS,
		Error:     p.state.err,
	}
}

// failed reports whether every entry of the binding is currently marked failed.
// This is the single eligibility source for both the health payload and x's
// inner binding group: the probe marks or resets all variants together, so it
// matches the group's FailFilter verdict, and a binding that never probed (or
// has no probe) has no marker set and reads as eligible.
func (p *bindingProbe) failed() bool {
	if len(p.entries) == 0 {
		return false
	}
	for _, e := range p.entries {
		if e == nil || !xs.IsFailed(e, 1, p.failTimeout) {
			return false
		}
	}
	return true
}

// loop runs the probe once immediately and then once per interval.
func (p *bindingProbe) loop(ctx context.Context) {
	p.run(ctx)
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.run(ctx)
		}
	}
}

// run executes the command once and applies the verdict to every variant entry.
func (p *bindingProbe) run(ctx context.Context) {
	if p.command == "" {
		return
	}

	start := time.Now()
	runCtx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	name, args := shellCmd(p.command)
	cmd := exec.CommandContext(runCtx, name, args...)
	cmd.Env = append(os.Environ(),
		"CUTTLE_ENTRYPOINT_ID="+p.entrypointID,
		"CUTTLE_ENTRYPOINT_NAME="+p.entrypointName,
		"CUTTLE_ROUTE_ID="+p.routeID,
		"CUTTLE_ROUTE_NAME="+p.routeName,
		"CUTTLE_BINDING_INDEX="+strconv.Itoa(p.index),
	)
	cmd.WaitDelay = probeWaitDelay
	// A probe is a shell that may start a pipeline: cancelling the timeout must
	// kill every process the command spawned, not just the shell.
	isolateProbeProcess(cmd)
	var stderr cappedBuffer
	stderr.max = probeStderrLimit
	cmd.Stderr = &stderr
	err := cmd.Run()
	latency := time.Since(start).Milliseconds()

	// A canceled parent context is a reload or a shutdown, not a verdict: the
	// old entries are draining and the new set owns the markers.
	if ctx.Err() != nil {
		return
	}

	state := probeState{ran: true, ok: err == nil, latencyMS: latency, at: time.Now()}
	switch {
	case err == nil:
	case errors.Is(runCtx.Err(), context.DeadlineExceeded):
		state.exitCode = -1
		state.err = "timeout after " + p.timeout.String()
		if out := strings.TrimSpace(stderr.String()); out != "" {
			state.err += ": " + out
		}
	default:
		state.exitCode = -1
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			state.exitCode = exitErr.ExitCode()
		}
		if out := strings.TrimSpace(stderr.String()); out != "" {
			state.err = out
		} else {
			state.err = err.Error()
		}
	}

	// Apply the verdict to the markers first: they are the single source of
	// truth for both routing and the health payload, so publishing the state
	// first would let a snapshot report Eligible=false for a binding whose
	// active row still names it.
	for _, e := range p.entries {
		if e == nil {
			continue
		}
		if state.ok {
			e.Marker().Reset()
			continue
		}
		e.Marker().Mark()
	}

	p.mu.Lock()
	prev := p.state
	p.state = state
	p.mu.Unlock()

	// The command and its full output never reach the log: a probe command can
	// carry credentials. Debug per run, info only on a verdict change.
	if prev.ran && prev.ok == state.ok {
		p.log.Debugf("entrypoint %s routes[%d]: probe ok=%t exit=%d latency=%dms",
			p.entrypointID, p.index, state.ok, state.exitCode, state.latencyMS)
		return
	}
	if state.ok {
		p.log.Infof("entrypoint %s routes[%d]: binding eligible after %dms",
			p.entrypointID, p.index, state.latencyMS)
		return
	}
	p.log.Infof("entrypoint %s routes[%d]: binding ineligible (exit %d) after %dms",
		p.entrypointID, p.index, state.exitCode, state.latencyMS)
}

// shellCmd returns the interpreter for a probe command. The command is never
// split or expanded by cuttle: the shell owns quoting and "$".
func shellCmd(command string) (string, []string) {
	if runtime.GOOS == "windows" {
		return "cmd", []string{"/C", command}
	}
	return "sh", []string{"-c", command}
}

// cappedBuffer keeps at most max bytes of a probe's stderr.
type cappedBuffer struct {
	buf bytes.Buffer
	max int
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if room := b.max - b.buf.Len(); room > 0 {
		if len(p) > room {
			p = p[:room]
		}
		b.buf.Write(p)
	}
	return n, nil
}

func (b *cappedBuffer) String() string { return b.buf.String() }
