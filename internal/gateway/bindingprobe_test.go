package gateway

import (
	"context"
	"net"
	"os"
	"path/filepath"
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

// assertSnapshotConsistent checks the invariant the switching tests rely on: a
// snapshot never names an active binding that it also reports ineligible, and a
// fallback means no binding is eligible.
func assertSnapshotConsistent(t *testing.T, snap []health.Entrypoint) {
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
		for _, b := range ep.Bindings {
			if b.Eligible {
				t.Fatalf("binding %d is eligible but the payload fell back: %+v", b.Index, ep)
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
		assertSnapshotConsistent(t, set.Snapshot())
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
