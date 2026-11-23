package gateway

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	xhop "github.com/go-gost/x/hop"
	xlogger "github.com/go-gost/x/logger"
	xs "github.com/go-gost/x/selector"

	gwconfig "github.com/ginuerzh/cuttle/internal/config"
	"github.com/ginuerzh/cuttle/internal/health"
)

// probeEntries builds the variant entries of one binding. The probe runner only
// touches their markers, so the hop behind them stays nil.
func probeEntries(n int) []*xhop.HopEntry {
	out := make([]*xhop.HopEntry, n)
	for i := range out {
		out[i] = xhop.NewHopEntry(nil, nil, nil)
	}
	return out
}

// testProbeSet builds an unstarted probe set whose route names are derived from
// the route id, so the environment assertions can name the resolved route.
func testProbeSet() *probeSet {
	return newProbeSet(func(id string) string { return "name-" + id }, xlogger.Nop())
}

// probeCommand builds a shell command that appends one line to path, which is
// how the runner tests count invocations.
func probeCommand(path string) string {
	return "echo run >> " + strconv.Quote(path)
}

// probeChildCommand builds a probe that starts one child process and publishes
// its pid, then blocks. A test can therefore prove that a timeout or a Stop
// kills the whole process group instead of leaving the child behind.
func probeChildCommand(pidPath string) string {
	return "sleep 30 & echo $! > " + strconv.Quote(pidPath) + "; wait"
}

// waitProbeChild waits for a probe to publish the pid of its child process.
func waitProbeChild(t *testing.T, path string) int {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for {
		if b, err := os.ReadFile(path); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil {
				return pid
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s was never written", path)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// waitProbeProcessGone waits until pid no longer runs. A child killed together
// with its group can linger as a zombie when nothing reaps it, so a zombie
// counts as gone: it can no longer run.
func waitProbeProcessGone(t *testing.T, pid int) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for probeProcessAlive(pid) {
		if time.Now().After(deadline) {
			t.Fatalf("probe child %d is still running after the probe stopped", pid)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// probeProcessAlive reports whether pid still runs. Linux exposes the process
// state in /proc, where a zombie is dead for this purpose; elsewhere the signal-0
// probe suffices because the platform reaps orphans.
func probeProcessAlive(pid int) bool {
	if b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat"); err == nil {
		// The state is the field after the parenthesized command name.
		if i := strings.LastIndexByte(string(b), ')'); i >= 0 && i+2 < len(b) {
			return b[i+2] != 'Z'
		}
		return true
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}

func waitForFile(t *testing.T, path string) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s was never created", path)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func countLines(t *testing.T, path string) int {
	t.Helper()

	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0
		}
		t.Fatal(err)
	}
	return strings.Count(string(b), "run")
}

func TestProbeRunnerMarksEveryVariant(t *testing.T) {
	flag := filepath.Join(t.TempDir(), "flag")
	entries := probeEntries(2)

	set := testProbeSet()
	set.add(
		gwconfig.Entrypoint{ID: "e_0000000001", Name: "all"},
		gwconfig.RouteBinding{Route: "r_0000000001", Probe: &gwconfig.Probe{
			Command:  "test -f " + strconv.Quote(flag),
			Interval: gwconfig.Duration(2 * time.Second),
			Timeout:  gwconfig.Duration(time.Second),
		}},
		0, entries, 10*time.Second,
	)

	set.probeOnce(context.Background())
	for i, e := range entries {
		if !xs.IsFailed(e, 1, 10*time.Second) {
			t.Fatalf("entry %d is not marked after a failing probe", i)
		}
	}

	if err := os.WriteFile(flag, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	set.probeOnce(context.Background())
	for i, e := range entries {
		if xs.IsFailed(e, 1, 10*time.Second) {
			t.Fatalf("entry %d is still marked after the probe recovered", i)
		}
	}
}

func TestProbeRunnerEnvironment(t *testing.T) {
	out := filepath.Join(t.TempDir(), "env")
	command := `printf '%s %s %s %s %s' "$CUTTLE_ENTRYPOINT_ID" "$CUTTLE_ENTRYPOINT_NAME" ` +
		`"$CUTTLE_ROUTE_ID" "$CUTTLE_ROUTE_NAME" "$CUTTLE_BINDING_INDEX" > ` + strconv.Quote(out)

	set := testProbeSet()
	set.add(
		gwconfig.Entrypoint{ID: "e_0000000001", Name: "claude"},
		gwconfig.RouteBinding{Route: "r_0000000001", Probe: &gwconfig.Probe{
			Command:  command,
			Interval: gwconfig.Duration(2 * time.Second),
			Timeout:  gwconfig.Duration(time.Second),
		}},
		0, probeEntries(1), 10*time.Second,
	)

	set.probeOnce(context.Background())

	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.TrimSpace(string(b)), "e_0000000001 claude r_0000000001 name-r_0000000001 0"; got != want {
		t.Fatalf("probe environment = %q, want %q", got, want)
	}
}

func TestProbeRunnerTimeout(t *testing.T) {
	entries := probeEntries(1)
	pidPath := filepath.Join(t.TempDir(), "child.pid")

	set := testProbeSet()
	set.add(
		gwconfig.Entrypoint{ID: "e_0000000001", Name: "all"},
		gwconfig.RouteBinding{Route: "r_0000000001", Probe: &gwconfig.Probe{
			Command:  probeChildCommand(pidPath),
			Interval: gwconfig.Duration(2 * time.Second),
			Timeout:  gwconfig.Duration(300 * time.Millisecond),
		}},
		0, entries, 10*time.Second,
	)

	start := time.Now()
	set.probeOnce(context.Background())
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("timed-out probe took %s, want about the 300ms timeout", elapsed)
	}
	if !xs.IsFailed(entries[0], 1, 10*time.Second) {
		t.Fatal("a timed-out probe did not mark its binding ineligible")
	}
	// The timeout must reach the shell's children too, or a pipeline such as
	// `curl … | grep …` would keep running after the probe gave up.
	if probeProcessGroups {
		waitProbeProcessGone(t, waitProbeChild(t, pidPath))
	}

	snap := set.Snapshot()
	if len(snap) != 1 || len(snap[0].Bindings) != 1 || snap[0].Bindings[0].Last == nil {
		t.Fatalf("snapshot = %+v, want one binding with a last probe", snap)
	}
	last := snap[0].Bindings[0].Last
	if last.OK {
		t.Fatal("timed-out probe reported ok")
	}
	if !strings.Contains(strings.ToLower(last.Error), "timeout") {
		t.Fatalf("probe error = %q, want a timeout", last.Error)
	}
}

func TestProbeRunnerOncePerBinding(t *testing.T) {
	counter := filepath.Join(t.TempDir(), "count")

	set := testProbeSet()
	set.add(
		gwconfig.Entrypoint{ID: "e_0000000001", Name: "all"},
		gwconfig.RouteBinding{Route: "r_0000000001", Probe: &gwconfig.Probe{
			Command:  probeCommand(counter),
			Interval: gwconfig.Duration(2 * time.Second),
			Timeout:  gwconfig.Duration(time.Second),
		}},
		0, probeEntries(4), 10*time.Second,
	)

	set.probeOnce(context.Background())

	if got := countLines(t, counter); got != 1 {
		t.Fatalf("probe ran %d times for one binding with four variants, want exactly once", got)
	}
}

func TestProbeRunnerStopCancels(t *testing.T) {
	pidPath := filepath.Join(t.TempDir(), "child.pid")

	set := testProbeSet()
	set.add(
		gwconfig.Entrypoint{ID: "e_0000000001", Name: "all"},
		gwconfig.RouteBinding{Route: "r_0000000001", Probe: &gwconfig.Probe{
			Command:  probeChildCommand(pidPath),
			Interval: gwconfig.Duration(2 * time.Second),
			Timeout:  gwconfig.Duration(time.Minute),
		}},
		0, probeEntries(1), 10*time.Second,
	)

	set.Start(context.Background())
	pid := waitProbeChild(t, pidPath)

	done := make(chan struct{})
	go func() {
		set.Stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not return within 2s")
	}

	// Stop must kill the whole process group: stopping only the shell would
	// leave the probe's children running for as long as they please.
	if probeProcessGroups {
		waitProbeProcessGone(t, pid)
	}

	// Stop waits for the goroutine, so the canceled run must not have recorded
	// a verdict: otherwise a reload could mark a fresh binding ineligible.
	if h := set.Snapshot(); len(h) == 1 && len(h[0].Bindings) == 1 && h[0].Bindings[0].Last != nil {
		t.Fatalf("a canceled probe recorded a verdict: %+v", h[0].Bindings[0].Last)
	}

	// A second Stop is a no-op and must not hang.
	set.Stop()
}

func TestProbeSnapshotStates(t *testing.T) {
	dir := t.TempDir()
	firstFlag := filepath.Join(dir, "first")
	secondFlag := filepath.Join(dir, "second")

	set := testProbeSet()
	e := gwconfig.Entrypoint{ID: "e_0000000001", Name: "all"}
	set.add(e, gwconfig.RouteBinding{Route: "r_0000000001", Probe: &gwconfig.Probe{
		Command:  "test -f " + strconv.Quote(firstFlag),
		Interval: gwconfig.Duration(2 * time.Second),
		Timeout:  gwconfig.Duration(time.Second),
	}}, 0, probeEntries(2), 10*time.Second)
	set.add(e, gwconfig.RouteBinding{Route: "r_0000000002", Probe: &gwconfig.Probe{
		Command:  "test -f " + strconv.Quote(secondFlag),
		Interval: gwconfig.Duration(2 * time.Second),
		Timeout:  gwconfig.Duration(time.Second),
	}}, 1, probeEntries(2), 10*time.Second)

	// Never probed: every binding is eligible and the first one is active.
	snap := set.Snapshot()
	if len(snap) != 1 || len(snap[0].Bindings) != 2 {
		t.Fatalf("snapshot = %+v, want one entrypoint with two bindings", snap)
	}
	if !snap[0].Bindings[0].Eligible || snap[0].Bindings[0].Last != nil {
		t.Fatalf("unprobed binding = %+v, want eligible without a last probe", snap[0].Bindings[0])
	}
	if snap[0].Bindings[0].Index != 0 || snap[0].Bindings[1].Index != 1 {
		t.Fatalf("binding order = %+v", snap[0].Bindings)
	}
	if snap[0].Active.RouteID != "r_0000000001" || snap[0].Active.Fallback {
		t.Fatalf("active = %+v, want the first binding without fallback", snap[0].Active)
	}

	// Every probe fails: all bindings are ineligible, so binding 0 is the
	// fallback the data plane will serve.
	set.probeOnce(context.Background())
	snap = set.Snapshot()
	if snap[0].Bindings[0].Eligible || snap[0].Bindings[1].Eligible {
		t.Fatalf("bindings = %+v, want both ineligible", snap[0].Bindings)
	}
	last := snap[0].Bindings[0].Last
	if last == nil || last.OK || last.ExitCode != 1 || last.LatencyMS < 0 || last.Error == "" {
		t.Fatalf("last probe = %+v, want a failed probe with exit code 1 and an error", last)
	}
	if snap[0].Active.RouteID != "r_0000000001" || !snap[0].Active.Fallback {
		t.Fatalf("active = %+v, want binding 0 as the fallback", snap[0].Active)
	}

	// The second binding recovers: it becomes the active binding.
	if err := os.WriteFile(secondFlag, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	set.probeOnce(context.Background())
	snap = set.Snapshot()
	if !snap[0].Bindings[1].Eligible {
		t.Fatalf("binding 1 = %+v, want eligible", snap[0].Bindings[1])
	}
	if snap[0].Active.RouteID != "r_0000000002" || snap[0].Active.Fallback {
		t.Fatalf("active = %+v, want binding 1 without fallback", snap[0].Active)
	}

	// The first binding recovers too: the declaration order wins again.
	if err := os.WriteFile(firstFlag, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	set.probeOnce(context.Background())
	snap = set.Snapshot()
	if snap[0].Active.RouteID != "r_0000000001" || snap[0].Active.Fallback {
		t.Fatalf("active = %+v, want binding 0 without fallback", snap[0].Active)
	}
}

// probeReloadConfig builds a one-entrypoint config whose only binding runs
// command, writing one line per run to path.
func probeReloadConfig(t *testing.T, path string) *gwconfig.Config {
	t.Helper()

	cfg := testConfig("http://127.0.0.1:1")
	// The sub-second interval deliberately bypasses config.Validate (which
	// requires at least 1s) so several runs fit in a test.
	cfg.Entrypoints[0].Routes = []gwconfig.RouteBinding{{
		Route: testRouteID,
		Probe: &gwconfig.Probe{
			Command:  probeCommand(path),
			Interval: gwconfig.Duration(20 * time.Millisecond),
			Timeout:  gwconfig.Duration(time.Second),
		},
	}}
	return cfg
}

func TestReloadReplacesProbeSet(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "first")
	second := filepath.Join(dir, "second")

	gw, err := New(probeReloadConfig(t, first), xlogger.Nop())
	if err != nil {
		t.Fatal(err)
	}
	if err := gw.Start(); err != nil {
		t.Fatal(err)
	}
	closed := false
	t.Cleanup(func() {
		if !closed {
			gw.Close()
		}
	})

	waitForFile(t, first)

	if err := gw.Reload(probeReloadConfig(t, second)); err != nil {
		t.Fatal(err)
	}
	waitForFile(t, second)

	// The replaced probe set must stop immediately: only the new command keeps
	// running.
	before := countLines(t, first)
	time.Sleep(150 * time.Millisecond)
	if after := countLines(t, first); after != before {
		t.Fatalf("the old probe set kept running after a reload: %d -> %d runs", before, after)
	}

	closed = true
	done := make(chan struct{})
	go func() {
		gw.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Close hung after a probe reload")
	}
}

// TestSnapshotPriorityOrderAndFallback pins the health mapping of the category
// priority: bindings are emitted probed-first with their configuration indexes,
// active follows that order, and fallback means a probe exists and none of the
// probed bindings is eligible.
func TestSnapshotPriorityOrderAndFallback(t *testing.T) {
	flag := filepath.Join(t.TempDir(), "flag")

	set := testProbeSet()
	mixed := gwconfig.Entrypoint{ID: "e_0000000001", Name: "mixed"}
	set.add(mixed, gwconfig.RouteBinding{Route: "r_0000000001"}, 0, probeEntries(1), 10*time.Second)
	set.add(mixed, gwconfig.RouteBinding{Route: "r_0000000002", Probe: &gwconfig.Probe{
		Command:  "test -f " + strconv.Quote(flag),
		Interval: gwconfig.Duration(2 * time.Second),
		Timeout:  gwconfig.Duration(time.Second),
	}}, 1, probeEntries(1), 10*time.Second)
	set.add(mixed, gwconfig.RouteBinding{Route: "r_0000000003"}, 2, probeEntries(1), 10*time.Second)

	// An entrypoint without any probe must never report a fallback.
	static := gwconfig.Entrypoint{ID: "e_0000000002", Name: "static"}
	set.add(static, gwconfig.RouteBinding{Route: "r_0000000004"}, 0, probeEntries(1), 10*time.Second)
	set.add(static, gwconfig.RouteBinding{Route: "r_0000000005"}, 1, probeEntries(1), 10*time.Second)

	indexes := func(he health.Entrypoint) []int {
		out := make([]int, 0, len(he.Bindings))
		for _, b := range he.Bindings {
			out = append(out, b.Index)
		}
		return out
	}

	// Before the first run every binding is eligible: the probed binding is
	// listed first and serves, without a fallback.
	snap := set.Snapshot()
	if len(snap) != 2 {
		t.Fatalf("snapshot = %+v, want two entrypoints", snap)
	}
	if got, want := indexes(snap[0]), []int{1, 0, 2}; !slices.Equal(got, want) {
		t.Fatalf("mixed binding order = %v, want priority order %v", got, want)
	}
	if snap[0].Bindings[0].Last != nil {
		t.Fatalf("unprobed binding carries a last probe: %+v", snap[0].Bindings[0])
	}
	if snap[0].Active.RouteID != "r_0000000002" || snap[0].Active.Fallback {
		t.Fatalf("active = %+v, want the probed binding without fallback", snap[0].Active)
	}
	if got, want := indexes(snap[1]), []int{0, 1}; !slices.Equal(got, want) {
		t.Fatalf("static binding order = %v, want configuration order %v", got, want)
	}
	if snap[1].Active.RouteID != "r_0000000004" || snap[1].Active.Fallback {
		t.Fatalf("static active = %+v, want the first binding without fallback", snap[1].Active)
	}

	// The probe fails: the first probe-less binding in configuration order
	// serves, and the payload reports the fallback because a probe exists and
	// none passed.
	set.probeOnce(context.Background())
	snap = set.Snapshot()
	if snap[0].Bindings[0].Eligible || snap[0].Bindings[0].Last == nil {
		t.Fatalf("probed binding = %+v, want ineligible with a last probe", snap[0].Bindings[0])
	}
	if got, want := indexes(snap[0]), []int{1, 0, 2}; !slices.Equal(got, want) {
		t.Fatalf("mixed binding order after a failed probe = %v, want %v", got, want)
	}
	if snap[0].Active.RouteID != "r_0000000001" || !snap[0].Active.Fallback {
		t.Fatalf("active = %+v, want the first probe-less binding as the fallback", snap[0].Active)
	}
	if snap[1].Active.RouteID != "r_0000000004" || snap[1].Active.Fallback {
		t.Fatalf("static active = %+v, want no fallback without probes", snap[1].Active)
	}

	// The probe recovers: the probed binding serves again and is listed first.
	if err := os.WriteFile(flag, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	set.probeOnce(context.Background())
	snap = set.Snapshot()
	if !snap[0].Bindings[0].Eligible {
		t.Fatalf("probed binding = %+v, want eligible", snap[0].Bindings[0])
	}
	if snap[0].Active.RouteID != "r_0000000002" || snap[0].Active.Fallback {
		t.Fatalf("active = %+v, want the recovered probed binding without fallback", snap[0].Active)
	}
}

// assertSnapshotConsistent checks the invariant the switching tests rely on: a
// snapshot never names an active binding that it also reports ineligible; and a
// fallback means no probed binding is eligible, so only a probe-less binding may
// be serving. probed lists the configuration indexes that carry a probe.
func assertSnapshotConsistent(t *testing.T, snap []health.Entrypoint, probed ...int) {
	t.Helper()

	if len(snap) != 1 || len(snap[0].Bindings) != 2 {
		t.Fatalf("snapshot = %+v, want one entrypoint with two bindings", snap)
	}
	ep := snap[0]
	active := -1
	for i, b := range ep.Bindings {
		if b.RouteID == ep.Active.RouteID {
			active = i
			break
		}
	}
	if active < 0 {
		t.Fatalf("active route %q is not one of the bindings: %+v", ep.Active.RouteID, ep.Bindings)
	}
	if !ep.Active.Fallback && !ep.Bindings[active].Eligible {
		t.Fatalf("active binding %d is reported ineligible: %+v", active, ep)
	}
	if ep.Active.Fallback {
		for _, idx := range probed {
			for _, b := range ep.Bindings {
				if b.Index == idx && b.Eligible {
					t.Fatalf("probed binding %d is eligible but the payload fell back: %+v", idx, ep)
				}
			}
		}
	}
}

// TestProbeSnapshotActiveMatchesEligibility reads the health payload while the
// probed binding keeps flipping. Eligibility and the active binding must come
// from the same marker state: otherwise a request sent right after a snapshot
// could reach a binding that the snapshot itself calls ineligible.
func TestProbeSnapshotActiveMatchesEligibility(t *testing.T) {
	flag := filepath.Join(t.TempDir(), "flag")

	set := testProbeSet()
	e := gwconfig.Entrypoint{ID: "e_0000000001", Name: "all"}
	set.add(e, gwconfig.RouteBinding{Route: "r_0000000001", Probe: &gwconfig.Probe{
		Command:  "test -f " + strconv.Quote(flag),
		Interval: gwconfig.Duration(2 * time.Second),
		Timeout:  gwconfig.Duration(time.Second),
	}}, 0, probeEntries(2), 10*time.Second)
	set.add(e, gwconfig.RouteBinding{Route: "r_0000000002"}, 1, probeEntries(2), 10*time.Second)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 100; i++ {
			if i%2 == 0 {
				_ = os.Remove(flag)
			} else {
				_ = os.WriteFile(flag, nil, 0o600)
			}
			set.probeOnce(context.Background())
		}
	}()

	for {
		select {
		case <-done:
			return
		default:
		}
		assertSnapshotConsistent(t, set.Snapshot(), 0)
	}
}

func TestEntrypointHealthNilSafe(t *testing.T) {
	var gw Gateway
	if got := gw.EntrypointHealth(); got != nil {
		t.Fatalf("EntrypointHealth = %+v, want nil without a data plane", got)
	}
}

// TestNewFailureStopsProbes covers a New that fails after apply already started
// the probe runner: the failed build must not leave goroutines forking probe
// commands for a hop nobody serves.
func TestNewFailureStopsProbes(t *testing.T) {
	counter := filepath.Join(t.TempDir(), "count")

	// Hold the port so listener init fails after apply started the probes.
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()

	cfg := probeReloadConfig(t, counter)
	cfg.Gateway.Listen = busy.Addr().String()

	if _, err := New(cfg, xlogger.Nop()); err == nil {
		t.Fatal("New succeeded on an occupied listen address")
	}

	before := countLines(t, counter)
	time.Sleep(150 * time.Millisecond)
	if after := countLines(t, counter); after != before {
		t.Fatalf("probes kept running after New failed: %d -> %d runs", before, after)
	}
}

// TestOrderAgreement is the guard against the console and the data plane
// disagreeing: bindingOrder (which feeds the group) and probePriorityOrder
// (which feeds the health payload) must produce the same index sequence for
// every arrangement of when/probe.
func TestOrderAgreement(t *testing.T) {
	probe := func(cmd string) *gwconfig.Probe {
		if cmd == "" {
			return nil
		}
		return &gwconfig.Probe{Command: cmd}
	}
	cases := [][]gwconfig.RouteBinding{
		{{Route: "r_0000000001"}, {Route: "r_0000000002"}},
		{{Route: "r_0000000001", Probe: probe("x")}, {Route: "r_0000000002"}},
		// The two arrangements the class tier exists for, and the two a
		// probe-less entrypoint would skip if probePriorityOrder bailed out
		// early on "nothing probes".
		{{Route: "r_0000000001", When: "subagent"}, {Route: "r_0000000002"}},
		{{Route: "r_0000000001"}, {Route: "r_0000000002", When: "subagent"}},
		{
			{Route: "r_0000000001"},
			{Route: "r_0000000002", When: "subagent", Probe: probe("x")},
			{Route: "r_0000000003", When: "subagent"},
			{Route: "r_0000000004", Probe: probe("y")},
		},
	}
	for i, bindings := range cases {
		hasProbe := false
		probes := make([]*bindingProbe, 0, len(bindings))
		for j, b := range bindings {
			cmd := ""
			if b.Probe != nil {
				cmd = b.Probe.Command
				hasProbe = true
			}
			probes = append(probes, &bindingProbe{index: j, command: cmd, when: b.When})
		}

		order, _ := bindingOrder(bindings)
		got := probePriorityOrder(probes, hasProbe)
		if got == nil {
			// The nil return means "configuration order is already the
			// priority order"; materialize it so the two can be compared.
			if hasConditioned(probes) {
				t.Fatalf("case %d: probePriorityOrder returned nil for a conditioned entrypoint", i)
			}
			got = make([]int, len(bindings))
			for j := range bindings {
				got[j] = j
			}
		}
		if !slices.Equal(order, got) {
			t.Fatalf("case %d: bindingOrder = %v but probePriorityOrder = %v", i, order, got)
		}
	}
}

// TestBindingHealthCarriesWhen pins the payload: conditioned rows report their
// class and Active still names the first eligible unconditional binding.
func TestBindingHealthCarriesWhen(t *testing.T) {
	cfg, _, _ := classFixture(t)
	plans := bindingPlans(t, cfg)
	_ = plans
	set := newProbeSet(func(id string) string { return "name-" + id }, xlogger.Nop())
	for i, b := range cfg.Entrypoints[0].Routes {
		set.add(cfg.Entrypoints[0], b, i, probeEntries(1), 10*time.Second)
	}
	snap := set.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("snapshot rows = %d, want 1", len(snap))
	}
	rows := snap[0].Bindings
	byIndex := map[int]health.Binding{}
	for _, b := range rows {
		byIndex[b.Index] = b
	}
	if got := byIndex[0].When; got != gwconfig.BindingWhenSubagent {
		t.Fatalf("bindings[0].When = %q, want %q", got, gwconfig.BindingWhenSubagent)
	}
	if got := byIndex[1].When; got != "" {
		t.Fatalf("bindings[1].When = %q, want empty", got)
	}
	if got := snap[0].Active.RouteID; got != "r_0000000002" {
		t.Fatalf("active route = %q, want the unconditional route %q", got, "r_0000000002")
	}
}

// TestFallbackScopedToUnconditional pins Fallback to the unconditional subset,
// the same subset Active is drawn from. Both directions matter:
//
//   - a healthy conditioned binding must not hide a dead unconditional one
//     (false negative: the console would call a degraded lead path normal);
//   - a dead conditioned binding must not mark lead as falling back (false
//     positive: the console would claim a path lead never takes).
func TestFallbackScopedToUnconditional(t *testing.T) {
	build := func(bindings []gwconfig.RouteBinding, marked []int) health.Entrypoint {
		t.Helper()
		set := testProbeSet()
		for i, b := range bindings {
			set.add(gwconfig.Entrypoint{ID: "e_0000000001", Name: "all"}, b, i, probeEntries(1), 10*time.Second)
		}
		for _, i := range marked {
			set.probes[i].entries[0].Marker().Mark()
		}
		snap := set.Snapshot()
		if len(snap) != 1 {
			t.Fatalf("snapshot rows = %d, want 1", len(snap))
		}
		return snap[0]
	}
	probed := func(route, when string) gwconfig.RouteBinding {
		return gwconfig.RouteBinding{Route: route, When: when, Probe: &gwconfig.Probe{Command: "true"}}
	}
	plain := func(route, when string) gwconfig.RouteBinding {
		return gwconfig.RouteBinding{Route: route, When: when}
	}

	t.Run("healthy conditioned binding must not hide a dead unconditional one", func(t *testing.T) {
		got := build([]gwconfig.RouteBinding{
			// Both probed: the conditioned one healthy, the unconditional one
			// dead. Lead can only reach the dead binding.
			probed("r_0000000001", gwconfig.BindingWhenSubagent),
			probed("r_0000000002", ""),
		}, []int{1})
		if got.Active.RouteID != "r_0000000002" {
			t.Fatalf("active = %q, want the unconditional route", got.Active.RouteID)
		}
		if !got.Active.Fallback {
			t.Fatal("fallback = false while lead can only reach a dead binding, want true")
		}
	})

	t.Run("dead conditioned binding must not mark lead as fallback", func(t *testing.T) {
		got := build([]gwconfig.RouteBinding{
			probed("r_0000000001", gwconfig.BindingWhenSubagent), // dead, conditioned
			plain("r_0000000002", ""),                            // unconditional, probe-less
		}, []int{0})
		if got.Active.RouteID != "r_0000000002" {
			t.Fatalf("active = %q, want the unconditional route", got.Active.RouteID)
		}
		if got.Active.Fallback {
			t.Fatal("fallback = true although lead never considered the conditioned binding, want false")
		}
	})

	t.Run("an unconditional dead probe with no conditioned binding still reports fallback", func(t *testing.T) {
		got := build([]gwconfig.RouteBinding{
			probed("r_0000000001", ""),
			plain("r_0000000002", ""),
		}, []int{0})
		if !got.Active.Fallback {
			t.Fatal("fallback = false, want true (the pre-class behavior must survive)")
		}
	})
}
