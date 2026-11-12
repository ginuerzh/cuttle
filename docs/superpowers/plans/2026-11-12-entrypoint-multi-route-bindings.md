# Entrypoint Multi-Route Bindings Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let an entrypoint bind an ordered list of routes and serve from the first one whose command probe exits 0, so external logic can switch routes at runtime without a config change or a client change.

**Architecture:** `entrypoint.routes` becomes an ordered binding list; each entrypoint × inbound-variant entry wraps an inner x hop group (FIFO + `FailFilter`) over the bindings' route hops. A cuttle-owned goroutine runs one shell command per binding and marks every variant entry of that binding, so x's selector skips ineligible bindings. The outer hop group keeps its bare FIFO so an ineligible binding never falls through to another entrypoint.

**Tech Stack:** Go 1.27, `github.com/go-gost/x v0.32.0` (hop group, `selector.FailFilter`, `HopEntry.Marker`), SQLite stats (unchanged), React 18 + Ant Design + React Flow console.

**Spec:** `docs/superpowers/specs/2026-11-12-entrypoint-multi-route-design.md`

## Global Constraints

- Run every Go command with `GOWORK=off`; `go.mod` pins published tags and must not gain a `replace`.
- Do not edit `../core`, `../x` or `../llm-api-converter`. This plan needs no x change.
- Config is a breaking change: `entrypoint.route` is removed, `entrypoint.routes` replaces it, no compatibility alias. `yaml.KnownFields(true)` must keep rejecting the old key.
- The probe is command-only (`sh -c`), exit 0 = eligible, anything else = ineligible. It must never call a provider API, so a probe cannot spend quota.
- Exactly one command run per binding per interval, independent of how many protocol variants the binding compiles to.
- Binding markers are written only by the probe runner. Never mark, reset or read node `failCodes` markers from probe code.
- The outer hop group selector stays `xs.FIFOStrategy` with no filters. The inner group is `FIFO + FailFilter(1, failTimeout)`.
- All bindings ineligible → x's last resort serves the first binding. Do not add a cuttle dispatcher to change that.
- A binding without `probe` may appear at most once and only as the last binding; validation rejects other placements.
- `probe.command` is never passed through `config.ExpandEnv`.
- Bindings are inline config: no entity id, never addressable in an API path.
- Code, comments, docs and commit messages are English. Console source strings are Chinese and every new string gets an `en-US` key.
- One stats row per client request; the recorded route is whatever binding served it. Do not add attempt rows for probes.
- Commit after every task, including that task's Implementation record in the same commit. Never push.

## Review Focus

- A probe-less binding in front of a probed binding silently shadows it forever. Validation must reject that arrangement.
- Every binding ineligible must still serve binding 0 — not 502, and never another entrypoint's catch-all.
- A hung probe command must time out, mark the binding ineligible and keep the loop alive; `Stop` must not block on it.
- A reload (SIGHUP or admin save) must stop the old probe goroutines and start the new set immediately, with no double marking and no stale health rows.
- A binding compiles up to four variant entries; one switch must update all of them, or the route changes for one wire API only.

---

### Task 1: Config bindings and inner-group selection

**Files:**
- Modify: `internal/config/config.go` (`Entrypoint`, new `RouteBinding`/`Probe`, `expandEnv`, `applyDefaults`, `Validate`)
- Modify: `internal/config/migrate.go`
- Modify: `internal/config/config_test.go`, `internal/config/migrate_test.go`
- Modify: `internal/gateway/build.go` (`buildHopGroup`, new `buildBindingsHop`, `bindingFailTimeout`)
- Modify: `internal/gateway/count_tokens_policy.go` (bindings instead of one route)
- Modify: `internal/gateway/gateway_test.go` (helper), every gateway/admin/config test fixture that uses `Entrypoint.Route`
- Modify: `internal/admin/handlers.go` (`sameRouteRef` → binding scan)
- Modify: `example.yaml`, `README.md` (schema + upgrade note), `AGENTS.md` (routing invariants)

**Interfaces:**
- Produces: `config.RouteBinding{Route string; Probe *config.Probe}`; `config.Probe{Command string; Interval Duration; Timeout Duration}`; `config.Entrypoint.Routes []RouteBinding`.
- Produces: `buildBindingsHop(entrypointID string, bindings []gwconfig.RouteBinding, plans map[string]*routePlan, api provider.API, failTimeout time.Duration, log logger.Logger) (hop.Hop, []*xhop.HopEntry, error)` — returns the inner group plus the per-binding entries, index-aligned with `bindings` (the `entrypointID` parameter was added during implementation for the group log name).
- Produces: `bindingFailTimeout(bindings []gwconfig.RouteBinding) time.Duration`.
- Produces test helper: `func bindings(ids ...string) []gwconfig.RouteBinding` in `internal/gateway/gateway_test.go`.

- [x] **Step 1: Write the failing config tests**

Add to `internal/config/config_test.go`:

```go
func TestValidateEntrypointRouteBindings(t *testing.T)
// table: draft (no routes) OK; two bindings with probes OK; probe-less last OK;
// blank route rejected; duplicate route rejected; unknown route rejected;
// probe-less first rejected ("must be last"); two probe-less rejected;
// blank command rejected; timeout >= interval rejected; interval < 1s rejected.
// Every error must contain the entrypoint name and `routes[<i>]`.

func TestLoadAppliesProbeDefaults(t *testing.T)
// YAML with `probe: {command: "true"}` loads with Interval == 30s, Timeout == 5s.

func TestLoadRejectsLegacyEntrypointRoute(t *testing.T)
// YAML with `entrypoints: [{name: e, match: ["*"], route: "r_..."}]` fails and
// the error names the unknown `route` field.

func TestProbeCommandIsNotEnvExpanded(t *testing.T)
// t.Setenv("CUTTLE_PROBE_TOKEN", "secret"); command `echo "$CUTTLE_PROBE_TOKEN" | grep -q x`
// must load byte-identical.

func TestEntrypointRouteBindingExpandsEnv(t *testing.T)
// `route: ${R_ID}` expands; the rest of the binding does not.
```

Add to `internal/config/migrate_test.go`:

```go
func TestMigrateEntrypointRouteBindings(t *testing.T)
// A hand-written config with route names inside two bindings migrates both
// references to ids in one pass, independent of binding order.
```

- [x] **Step 2: Run the config tests to verify they fail**

Run: `GOWORK=off go test ./internal/config -run 'TestValidateEntrypointRouteBindings|TestLoadAppliesProbeDefaults|TestLoadRejectsLegacyEntrypointRoute|TestProbeCommandIsNotEnvExpanded|TestEntrypointRouteBindingExpandsEnv' -count=1`
Expected: FAIL (`undefined: RouteBinding`, or the legacy config loads).

- [x] **Step 3: Implement the config schema and pipeline**

In `internal/config/config.go`:

```go
type Entrypoint struct {
    ID     string         `yaml:"id" json:"id"`
    Name   string         `yaml:"name" json:"name"`
    Match  []string       `yaml:"match" json:"match"`
    Routes []RouteBinding `yaml:"routes,omitempty" json:"routes,omitempty"`
}

// RouteBinding binds a route to an entrypoint. Probe, when set, decides whether
// the binding may serve new requests: exit 0 means eligible.
type RouteBinding struct {
    Route string `yaml:"route" json:"route"`
    Probe *Probe `yaml:"probe,omitempty" json:"probe,omitempty"`
}

type Probe struct {
    Command  string   `yaml:"command" json:"command"`
    Interval Duration `yaml:"interval,omitempty" json:"interval,omitempty"`
    Timeout  Duration `yaml:"timeout,omitempty" json:"timeout,omitempty"`
}
```

- `expandEnv`: expand `binding.Route` only; never `Probe.Command`.
- `applyDefaults`: trim `Route` and `Command`; `Interval` default 30s, `Timeout` default 5s when zero.
- `Validate` (replacing the single-route check at the `route := strings.TrimSpace(e.Route)` block): every binding route non-blank, resolves to a route id, no duplicates; a `Probe` needs a non-blank command, `Interval >= 1s`, `Timeout >= 1s`, `Timeout < Interval`; if any binding has no probe it must be the last one. Error strings: `entrypoint %q: routes[%d]: <reason>`.
- `migrate.go`: loop `e.Routes` and resolve each `Route` reference through `resolveReference` with the existing route ids/names maps.

- [x] **Step 4: Run the config tests to verify they pass**

Run: `GOWORK=off go test ./internal/config -count=1`
Expected: PASS.

- [x] **Step 5: Write the failing selection tests**

Add `internal/gateway/bindings_test.go` (package `gateway`), using `matrixConfig`/`testConfig` helpers and two upstreams:

```go
func TestBindingOrderWins(t *testing.T)
// entrypoint with routes [{r_1}, {r_2}], both eligible: a request must hit
// r_1's upstream only.

func TestBindingFailFilterSkipsMarkedEntry(t *testing.T)
// Build the inner group with buildBindingsHop, Mark() entries[0], then Select:
// the chosen node must belong to r_2.

func TestAllBindingsIneligibleFallsBackToFirst(t *testing.T)
// Mark() every entry, Select must return r_1's node (x last resort), not nil.

func TestSingleBindingIgnoresMarker(t *testing.T)
// One binding, Mark() it, Select still returns its node (FailFilter no-op).

func TestBindingFailTimeoutDerivation(t *testing.T)
// intervals 5s and 40s -> 80s; no probes -> 10s.
```

- [x] **Step 6: Run the selection tests to verify they fail**

Run: `GOWORK=off go test ./internal/gateway -run 'TestBinding' -count=1`
Expected: FAIL (`undefined: buildBindingsHop`).

- [x] **Step 7: Implement the inner group and wire it into the build**

In `internal/gateway/build.go`:

```go
func bindingFailTimeout(bindings []gwconfig.RouteBinding) time.Duration
// 2 × the longest probe interval, floor 10s.

func buildBindingsHop(bindings []gwconfig.RouteBinding, plans map[string]*routePlan, api provider.API, failTimeout time.Duration, log logger.Logger) (hop.Hop, []*xhop.HopEntry, error)
```

- One entry per binding: `xhop.NewHopEntry(plan.hops[variantKey(api)], nil, nil)`; `api == ""` uses the fallback variant.
- Group name `entrypoint/<entrypointID>/<variant>`; selector `xs.NewSelector(xs.FIFOStrategy[hop.Hop](), xs.FailFilter[hop.Hop](1, failTimeout))`.
- In `buildHopGroup`, build a `plans []*routePlan` slice parallel to `e.Routes` from the existing `routePlans` cache, call `buildBindingsHop` per variant, and wrap the returned group in the usual `xhop.NewHopEntry(inner, matcher, nil)`. A draft entrypoint (no bindings) keeps the `buildDraftPlan` path and gains no inner group.
- `buildHopGroup`'s outer selector stays `xs.NewSelector(xs.FIFOStrategy[hop.Hop]())` with no filters; add a comment stating why.
- `count_tokens_policy.go`: estimate when any binding's plan has `countTokensEstimate`; a draft entrypoint estimates nothing.

- [x] **Step 8: Update fixtures and the admin route-reference check**

- `internal/gateway/gateway_test.go`: add `func bindings(ids ...string) []gwconfig.RouteBinding`; replace every `Route: <id>` in gateway/admin test entrypoints with `Routes: bindings(<id>)` (`grep -rn "Route: testRouteID\|Route: \"r_" internal`).
- `internal/admin/handlers.go`: replace `sameRouteRef(ref string, rt config.Route)` with `routeReferencedBy(e config.Entrypoint, routeID string) bool` scanning `e.Routes`, and use it in `handleDeleteRoute`; keep the existing error wording.
- Update config JSON fixtures in admin tests from `"route":"r_..."` to `"routes":[{"route":"r_..."}]` and add `TestConfigRoundTripEntrypointBindings` (PUT `/api/config` keeps a two-binding entrypoint, including the probe, and rejects a dangling route id).

Run: `GOWORK=off go build ./... && GOWORK=off go vet ./...`
Expected: clean.

- [x] **Step 9: Run the full Go gate**

Run: `GOWORK=off go test ./... -count=1`
Expected: PASS (the probe runner does not exist yet, so every binding is eligible and ordering is the only behavior).

- [x] **Step 10: Update the docs that the schema change invalidates**

- `example.yaml`: convert both entrypoints to `routes:` (one binding each, no probe).
- `README.md`: config example, an upgrade note ("`entrypoint.route` is gone; a file that still carries it fails to load; rewrite it as a one-element `routes` list before SIGHUP"), and the binding/probe semantics (order, probe-less-last, all-ineligible fallback).
- `AGENTS.md` "Routing invariants": entrypoint → ordered route bindings, first eligible wins, probe is command-only and per binding, outer group has no FailFilter, all-ineligible falls back to the first binding.

- [x] **Step 11: Commit**

```bash
git add internal/config internal/gateway internal/admin example.yaml README.md AGENTS.md \
  docs/superpowers/specs/2026-11-12-entrypoint-multi-route-design.md \
  docs/superpowers/plans/2026-11-12-entrypoint-multi-route-bindings.md
git commit -m "feat(config)!: bind entrypoints to an ordered list of routes"
```

**Implementation record (fill in after the task is complete)**
- Date/status: 2026-11-12 / done. Probe runner, health API and console are the
  later tasks; this task only prepares config, build and selection.
- Repository + commit: `cuttle` — `5138d930` `feat(config)!: bind entrypoints
  to an ordered list of routes`.
- Files changed:
  - `internal/config/config.go`: `Entrypoint.Routes []RouteBinding`, new
    `RouteBinding` and `Probe`; `expandEnv` expands only `binding.Route`;
    `applyDefaults` trims route/command and defaults interval `30s`, timeout
    `5s`; `Validate` implements the binding rules with
    `entrypoint %q: routes[%d]: ...` messages.
  - `internal/config/migrate.go`: resolves every `routes[].route` through
    `resolveReference`.
  - `internal/config/config_test.go`, `internal/config/migrate_test.go`: new
    tests plus every fixture moved to `routes:`.
  - `internal/gateway/build.go`: `bindingFailTimeout`, `buildBindingsHop`,
    `bindingsHaveVariant`; `buildHopGroup` wraps each entrypoint × protocol
    variant in an inner binding group and keeps the outer selector filter-free.
  - `internal/gateway/count_tokens_policy.go`: estimates when any binding's
    plan estimates; a draft estimates nothing.
  - `internal/gateway/bindings_test.go` (new): order, FailFilter, last-resort
    fallback, single-binding no-op and fail-timeout tests.
  - `internal/admin/handlers.go`: `sameRouteRef` replaced by
    `routeReferencedBy` scanning `e.Routes`.
  - Admin/gateway/e2e fixtures moved from `route:`/`Route:` to
    `routes`/`Routes`; `bindings(...)` helpers in both gateway test packages.
  - `internal/admin/entrypoint_test.go`: `TestConfigRoundTripEntrypointBindings`.
  - `example.yaml`, `README.md`, `AGENTS.md`: schema, upgrade note, binding and
    probe semantics, routing invariants.
- Tests/commands and results:
  - `GOWORK=off go test ./internal/config -run 'TestValidateEntrypointRouteBindings|TestLoadAppliesProbeDefaults|TestLoadRejectsLegacyEntrypointRoute|TestProbeCommandIsNotEnvExpanded|TestEntrypointRouteBindingExpandsEnv|TestMigrateEntrypointRouteBindings' -count=1` — RED first (`undefined: RouteBinding`), then PASS.
  - `GOWORK=off go test ./internal/gateway -run 'TestBinding' -count=1` — RED
    first (`undefined: buildBindingsHop`), then PASS.
  - `GOWORK=off go build ./... && GOWORK=off go vet ./...` — clean.
  - `GOWORK=off go test ./... -count=1` — PASS.
- Review findings and fixes: The brief's `buildBindingsHop` signature cannot
  express the required `entrypoint/<entrypointID>/<variant>` group name, so it
  takes `entrypointID` as its first argument. A binding whose route has no hop
  for the inbound variant uses the route's fallback hop, matching the old
  fallthrough to the entrypoint fallback entry. `entrypoint_e2e_test.go`
  (`package gateway_test`) needed its own `bindings` helper; the brief's helper
  lives in `package gateway`.
- Deferred items/limitations: no probe runner yet, so every binding is eligible
  and only order decides. Probe health, admin endpoint, console and topology
  edges follow in tasks 2-6. `web/src/api/types.ts` still carries
  `Entrypoint.route` until task 4; the API ignores it (the console sends no
  `routes`), so the console cannot edit bindings until then.

---

### Task 2: Command probe runner and its lifecycle

**Files:**
- Create: `internal/gateway/bindingprobe.go`
- Create: `internal/gateway/bindingprobe_test.go`
- Modify: `internal/health/health.go` (binding health payload types)
- Modify: `internal/gateway/build.go` (return a `*probeSet`; register entries per binding)
- Modify: `internal/gateway/gateway.go` (store, start, stop)
- Create: `internal/gateway/probe_e2e_test.go`

**Interfaces:**
- Consumes: `buildBindingsHop` entries from Task 1.
- Produces: `probeSet` with `add(e gwconfig.Entrypoint, b gwconfig.RouteBinding, index int, entries []*xhop.HopEntry, failTimeout time.Duration)`, `Start(ctx context.Context)`, `Stop()`, `probeOnce()`, `Snapshot() []health.Entrypoint`.
- Produces: `health.Entrypoint{ID, Name string; Active health.ActiveBinding; Bindings []health.Binding}`; `health.ActiveBinding{RouteID string; Fallback bool}`; `health.Binding{Index int; RouteID, RouteName string; Eligible bool; Last *health.Probe}`; `health.Probe{At time.Time; OK bool; ExitCode int; LatencyMS int64; Error string}` (JSON tags per the spec: `entrypointId`, `entrypointName`, `active.routeId`, `active.fallback`, `bindings[].routeName`, `last.latencyMs`).
- Produces: `buildHopGroup(...) (hop.Hop, *probeSet, []string, *countTokensPolicy, error)`; `Gateway.probes *probeSet`.

- [ ] **Step 1: Write the failing runner tests**

`internal/gateway/bindingprobe_test.go`:

```go
func TestProbeRunnerMarksEveryVariant(t *testing.T)
// Two entries with markers, command `test ! -f <flag>`; probeOnce: both marked.
// Create the flag, probeOnce: both reset.

func TestProbeRunnerEnvironment(t *testing.T)
// Command writes "$CUTTLE_ENTRYPOINT_ID $CUTTLE_ROUTE_ID $CUTTLE_BINDING_INDEX"
// to a file; assert the exact ids and "0".

func TestProbeRunnerTimeout(t *testing.T)
// Command `sleep 5`, timeout 100ms: run returns within ~1s and marks.

func TestProbeRunnerOncePerBinding(t *testing.T)
// One binding registered with four variant entries: probeOnce increments a
// counter file exactly once.

func TestProbeRunnerStopCancels(t *testing.T)
// Long command; Stop() returns within 2s and no goroutine stays in run.

func TestProbeSnapshotStates(t *testing.T)
// never probed -> eligible; failed probe -> eligible=false, active index 0,
// fallback=true; recovered first binding -> active index 0, fallback=false.

func TestReloadReplacesProbeSet(t *testing.T)
// startGateway, then Reload with a changed probe command; the new command's
// marker file is written within one interval and the old command's file stops
// advancing. Close(t) at cleanup must not hang.
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `GOWORK=off go test ./internal/gateway -run 'TestProbe|TestReloadReplacesProbeSet' -count=1`
Expected: FAIL (`undefined: probeSet`).

- [ ] **Step 3: Implement `internal/gateway/bindingprobe.go`**

- `bindingProbe` fields: entrypoint id/name, binding index, route id/name, `command`, `interval`, `timeout`, `failTimeout`, `entries []*xhop.HopEntry`, `mu sync.Mutex`, and `state{ran, ok bool; exitCode int; latencyMS int64; err string; at time.Time}`.
- `Start`: `context.WithCancel`, one goroutine per binding, `sync.WaitGroup`; run immediately, then `time.NewTicker(interval)`.
- One run: `exec.CommandContext` with `timeout`, shell from a local `shellCmd()` helper (`sh -c`, `cmd /C` on Windows), `Env` = `os.Environ()` plus `CUTTLE_ENTRYPOINT_ID`, `CUTTLE_ENTRYPOINT_NAME`, `CUTTLE_ROUTE_ID`, `CUTTLE_ROUTE_NAME`, `CUTTLE_BINDING_INDEX`. Exit code via `*exec.ExitError`; capture stderr capped at 1 KiB.
- Success: `Reset()` every entry; failure/timeout/exec error: `Mark()` every entry. Store the state.
- Log: debug per run, info only on eligible↔ineligible transitions; never log the command or full output.
- `internal/health/health.go` gains the payload types listed in Interfaces.
  `Snapshot()` groups bindings by entrypoint in configuration order, computing
  `active` with `xs.IsFailed(entry, 1, failTimeout)` over the binding entries:
  first non-failed binding wins, and when none survives the active index is 0
  with `Fallback: true`. A binding that has not probed yet is eligible.
- `Stop`: cancel, then wait for the WaitGroup; `Start` is idempotent.

- [ ] **Step 4: Wire the set through the build and the gateway**

- `buildHopGroup` returns `*probeSet`, creating it before the entrypoint loop, calling `add` for each binding with that binding's variant entries, and leaving it unstarted.
- `Gateway` gains `probes *probeSet`. `apply`: after the hop swap, call `probes.Start(context.Background())` and `oldProbes.Stop()` immediately — a previous set must never keep marking a hop that no new request uses. Only the old hop close and the rewriter unregister stay in the existing `time.AfterFunc(30*time.Second, ...)` grace block. A failed build starts nothing.
- `Gateway.EntrypointHealth()` returns `g.probes.Snapshot()` under `g.mu`, nil-safe.

- [ ] **Step 4b: Add the end-to-end switch test**

`internal/gateway/probe_e2e_test.go`:

```go
func TestEntrypointSwitchesRouteByProbe(t *testing.T)
// Two routes over two captureUpstream servers, bindings [r_1(probe), r_2(none)].
// Probe command: `test ! -f <flag>`. Start gateway with interval 20ms; first
// request hits upstream 1. Create the flag (r_1 ineligible); poll requests until
// upstream 2 is hit (2s budget). Remove the flag; poll until upstream 1 is hit
// again. Assert no probe ever reached an upstream (probe hit counts stay 0) and
// no node row reports dead.
```

- [ ] **Step 5: Run the probe tests**

Run: `GOWORK=off go test ./internal/gateway -run 'TestProbe|TestEntrypointSwitchesRouteByProbe|TestReloadReplacesProbeSet' -count=1 -race`
Expected: PASS, race clean.

- [ ] **Step 6: Run the full Go gate**

Run: `GOWORK=off go test ./... -count=1`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/gateway
git commit -m "feat(gateway): switch route bindings with a command probe"
```

**Implementation record (fill in after the task is complete)**
- Date/status: 2026-11-12 / done; review fix round 1 done the same day.
- Repository + commit: `cuttle` — `7c98872f` `feat(gateway): switch route
  bindings with a command probe`, `ead2fa62` `docs: record task 2 commit`.
  Review fix round 1 is one commit carrying this record on
  `feat/entrypoint-bindings`; its SHA is recorded in
  `.superpowers/sdd/2026-11-12-entrypoint-multi-route-bindings/task-2-report.md`
  under "Fix round 1" (a record cannot carry the SHA of the commit that contains
  it).
- Files changed:
  - `internal/gateway/bindingprobe.go` (new): `probeSet` (`add`, `Start`,
    `Stop`, `probeOnce`, `Snapshot`), `bindingProbe`, `probeState`, the
    `shellCmd` (`sh -c`, `cmd /C`) and `cappedBuffer` (1 KiB stderr) helpers.
    One goroutine per probed binding; it runs immediately and then once per
    interval, `exec.CommandContext` with the probe timeout, `WaitDelay`, and
    `CUTTLE_ENTRYPOINT_ID/NAME`, `CUTTLE_ROUTE_ID/NAME`,
    `CUTTLE_BINDING_INDEX` in the environment. Success resets, failure marks
    every variant `*xhop.HopEntry` of that binding; node `failCodes` markers and
    the stats store are untouched. Debug per run, info only on a verdict
    transition, never the command or its output. Fix round 1: the markers are
    written before `probeState` is published, and `Snapshot` derives `Eligible`
    and `Active` from one marker read per binding (`!p.failed()`); the process
    group helper comes from the platform pair below.
  - `internal/gateway/bindingprobe_posix.go` (new, `//go:build unix`): starts a
    probe in its own process group (`SysProcAttr{Setpgid: true}`) and overrides
    `cmd.Cancel` to kill the whole group, so a timeout or `Stop` also ends a
    probe's pipeline; exports the `probeProcessGroups` constant the tests use.
  - `internal/gateway/bindingprobe_other.go` (new, `//go:build !unix`): no-op
    `isolateProbeProcess` and `probeProcessGroups = false`; `cmd /C` stays the
    Windows shell.
  - `internal/gateway/bindingprobe_test.go` (new): the seven tests named in the
    brief plus `TestEntrypointHealthNilSafe`, and in fix round 1
    `TestProbeSnapshotActiveMatchesEligibility` (payload invariant under a
    flipping probe), `TestNewFailureStopsProbes` (no probe goroutines after a
    failed `New`) and child-process assertions in the timeout and stop tests.
  - `internal/gateway/probe_e2e_test.go` (new):
    `TestEntrypointSwitchesRouteByProbe` and the Task 1 review follow-up
    `TestIneligibleBindingDoesNotFallThrough`.
  - `internal/health/health.go`: `Entrypoint`, `ActiveBinding`, `Binding`,
    `Probe` with the spec's JSON names.
  - `internal/gateway/build.go`: `buildHopGroup` returns an unstarted
    `*probeSet`; the variant entries of each binding are collected per binding
    index and registered with one `add` call per entrypoint binding.
  - `internal/gateway/gateway.go`: `Gateway.probes`, start of the new set and
    stop of the old one in `apply`, stop in `Close`, and `EntrypointHealth()`.
    Fix round 1: a `New` that fails at listener init calls `g.Close()`, so the
    already-started probe runner is stopped.
  - `internal/gateway/bindings_test.go`: Task 1 review finding 1 — the fixture
    now carries a probe on the first binding and the probe-less binding last, so
    it passes `config.Validate`; `TestBindingFixtureIsSchemaValid` locks that in.
  - `internal/gateway/gateway_test.go`: `buildHopGroup` call site.
- Tests/commands and results:
  - `GOWORK=off go test ./internal/gateway -run 'TestProbe|TestReloadReplacesProbeSet|TestEntrypointHealthNilSafe' -count=1` — RED first (`undefined: probeSet`, `undefined: newProbeSet`, `gw.EntrypointHealth undefined`), then PASS.
  - `GOWORK=off go test ./internal/gateway -run 'TestProbe|TestEntrypointSwitchesRouteByProbe|TestIneligibleBindingDoesNotFallThrough|TestReloadReplacesProbeSet' -count=1 -race` — PASS, race clean (the Step 5 range plus the no-fallthrough test).
  - The same set with `-count=5 -race` — PASS, no flakes.
  - `GOWORK=off go build ./... && GOWORK=off go vet ./... && GOWORK=off go test ./... -count=1` — PASS.
  - `GOWORK=off go test ./internal/gateway -run TestBinding -count=1` — PASS.
  - Fix round 1: the same `-count=5 -race` set — PASS (`ok ... 4.263s`, no stray
    probe processes afterwards); `GOWORK=off go test ./internal/health
    ./internal/gateway -count=1` — PASS; `GOWORK=off go build ./... && GOWORK=off
    go vet ./... && GOWORK=off go test ./... -count=1` — PASS; `gofmt -l` clean.
    Each new regression test was run against the pre-fix code and failed there
    (see the report for the outputs).
- Review findings and fixes:
  - Task 1 finding 1 (schema-invalid fixture): fixed as described above. A
    literal `Interval: 1s` cannot satisfy `Validate` (the timeout must be
    smaller than the interval and at least 1s), so the fixture uses `2s`/`1s`.
  - Task 1 finding 2 (cross-entrypoint fallthrough): fixed by
    `TestIneligibleBindingDoesNotFallThrough`, which makes every binding of the
    first entrypoint ineligible and proves the later catch-all entrypoint serves
    nothing while the first binding still answers via x's last resort.
  - The plan's Step 4 prose stops the old probe set inside the 30s grace block,
    but Step 1's `TestReloadReplacesProbeSet` and the plan's Review Focus both
    require the old probes to stop immediately. The old set is therefore stopped
    right after the hop swap; only the hop close and the rewriter unregister stay
    in the grace block. A canceled run records no verdict, so a draining build
    cannot look failed.
  - The `test -f <flag>` probe of the Step 4b sketch means "eligible while the
    file exists", which contradicts its own "create the flag (r_1 ineligible)"
    step; the test uses `test ! -f <flag>` so the flag's presence makes the
    binding ineligible.
  - Fix round 1, finding 1 (verdict published before the markers): markers are
    now written first and `probeState` is published last, and `Snapshot` derives
    `Eligible` and `Active` from the same single marker read per binding
    (`!failed`), so the payload can never name an active binding it also reports
    ineligible and the first request after a wait on `Eligible` cannot reach the
    old binding. `TestProbeSnapshotActiveMatchesEligibility` guards it and fails
    on the pre-fix code.
  - Fix round 1, finding 3 (probe goroutines outliving a failed `New`): the
    listener-init error path calls `g.Close()`.
    `TestNewFailureStopsProbes` fails on the pre-fix code (0 -> 8 probe runs
    after `New` returned an error).
  - Fix round 1, finding 4 (timeout killed only the shell): `isolateProbeProcess`
    runs each probe in its own process group and kills the group on timeout and
    on `Stop`; the timeout and stop tests publish the child pid and assert it is
    gone, and both fail when the group kill is reverted.
- Deferred items/limitations: the admin endpoint, the events snapshot field and
  the console (tasks 3-6) are not wired yet; `Gateway.EntrypointHealth()` is the
  only new entry point. Probes start during `apply`, so they already run before
  `Gateway.Start` binds the listener, which is what the first-request test
  relies on. Bindings that bypass `config.Load` get the 30s/5s probe defaults.
  Process-group kill is POSIX-only; the Windows path keeps `cmd /C` and the
  child assertions are skipped there.

---

### Task 3: Binding health API and monitor snapshot

**Files:**
- Modify: `internal/admin/server.go` (field + wiring + route)
- Modify: `internal/admin/handlers.go` (handler)
- Modify: `internal/admin/handlers_events.go` (snapshot field)
- Create: `internal/admin/entrypoint_health_test.go`
- Modify: `internal/admin/events_test.go`

**Interfaces:**
- Consumes: `health.Entrypoint`, `probeSet.Snapshot()` and `Gateway.EntrypointHealth()` from Task 2.
- Produces: `GET /api/entrypoints/health` returning `[]health.Entrypoint` (never `null`); `GET /api/events` snapshot field `entrypointHealth`.

- [x] **Step 1: Write the failing tests**

```go
// internal/admin/entrypoint_health_test.go
func TestEntrypointHealthEndpointRequiresAuth(t *testing.T)   // 401 without session
func TestEntrypointHealthShape(t *testing.T)
// Two bindings, first failed: payload has eligible=false + last.exitCode,
// active.routeId == first, active.fallback == true; bindings order preserved.
func TestEntrypointHealthDraftEntrypoint(t *testing.T)        // no bindings -> empty list, not null

// internal/admin/events_test.go
func TestSnapshotCarriesEntrypointHealth(t *testing.T)        // snapshot JSON has the field
```

- [x] **Step 2: Run them to verify they fail**

Run: `GOWORK=off go test ./internal/admin -run 'TestEntrypointHealth|TestSnapshotCarriesEntrypointHealth' -count=1`
Expected: FAIL (404 or missing field).

- [x] **Step 3: Implement the endpoint and the snapshot field**

- `admin.Server`: `entrypointHealth func() []health.Entrypoint`; wire from the router in `New` the same way as `routeHealth`; `api.HandleFunc("GET /api/entrypoints/health", s.handleEntrypointHealth)`.
- `handleEntrypointHealth`: nil function → `writeJSON(w, 200, []health.Entrypoint{})`.
- `handlers_events.go` snapshot struct: `EntrypointHealth []health.Entrypoint \`json:"entrypointHealth,omitempty"\``, filled from the same function.

- [x] **Step 4: Run the admin tests and the full gate**

Run: `GOWORK=off go test ./internal/admin -count=1 && GOWORK=off go test ./... -count=1`
Expected: PASS.

- [x] **Step 5: Commit**

```bash
git add internal/admin
git commit -m "feat(admin): expose entrypoint binding health"
```

**Implementation record (fill in after the task is complete)**
- Date/status: 2026-11-12 / done. HTTP surface only: the data plane's
  `Gateway.EntrypointHealth()` is consumed, not changed.
- Repository + commit: `cuttle` — `33c98a71` `feat(admin): expose entrypoint
  binding health`, which carries this task's code, tests and record. A
  follow-up `docs: record task 3 commit` commit writes this SHA only.
- Files changed:
  - `internal/admin/server.go`: `entrypointHealth func() []health.Entrypoint`
    field, wired in `New` from the reloader's `EntrypointHealth()` when it
    implements it, and `GET /api/entrypoints/health` registered inside the
    authenticated `api` mux.
  - `internal/admin/handlers.go`: `handleEntrypointHealth`; a nil function and a
    nil slice both render `[]health.Entrypoint{}`.
  - `internal/admin/handlers_events.go`: `eventMessage.EntrypointHealth
    []health.Entrypoint \`json:"entrypointHealth,omitempty"\`` filled from
    `currentEntrypointHealth()` on the first snapshot and on every tick.
  - `internal/admin/entrypoint_health_test.go` (new): auth, payload shape and
    draft tests.
  - `internal/admin/events_test.go`: `eventSnapshot` carries the field;
    `TestSnapshotCarriesEntrypointHealth`.
  - `internal/admin/server_test.go`: `recordingReloader.entrypoints` plus its
    `EntrypointHealth()` method, so the existing test server can expose binding
    health. This test-support file is not in the brief's file list.
- Tests/commands and results:
  - RED: `GOWORK=off go test ./internal/admin -run
    'TestEntrypointHealth|TestSnapshotCarriesEntrypointHealth' -count=1` —
    `TestEntrypointHealthShape` and `TestEntrypointHealthDraftEntrypoint` got
    `404 page not found`, and `TestSnapshotCarriesEntrypointHealth` read
    `entrypoint health = [], want 1`. The auth test already passed because
    `/api/` is guarded before route matching; it is a regression guard.
  - GREEN: the same command — `ok github.com/ginuerzh/cuttle/internal/admin
    0.369s`.
  - `GOWORK=off go test ./internal/admin -count=1` — `ok ... 16.857s`.
  - `GOWORK=off go build ./... && GOWORK=off go vet ./...` — clean.
  - `GOWORK=off go test ./... -count=1` — PASS (admin 19.653s, gateway 19.336s,
    stats 29.279s, every other package ok).
- Review findings and fixes: none blocking. The shape test decodes a
  test-local payload struct instead of `health.Entrypoint`, so it pins the JSON
  names (`entrypointId`, `entrypointName`, `active.routeId`,
  `active.fallback`, `bindings[].index/routeId/routeName/eligible`,
  `last.at/ok/exitCode/latencyMs/error`) rather than the Go field names, and
  asserts binding order, `eligible=false` with `last.exitCode=3`,
  `active.routeId` = the first binding and `active.fallback=true`.
- Deferred items/limitations: the console types, graph edges and panels
  (tasks 4-5) and the README/AGENTS documentation (task 6) do not know these
  fields yet. `entrypointHealth` is omitted from the events snapshot when the
  data plane reports nothing, per the brief's `omitempty`; the REST endpoint
  always returns an array. Draft entrypoints are absent from the list by design
  (no bindings, no probe runner), so the payload cannot show them as `[]`
  bindings.

---

### Task 4: Console — types, graph edges, entrypoint list and form

**Files:**
- Modify: `web/src/api/types.ts`, `web/src/api/client.ts`, `web/src/api/events.ts`, `web/src/App.tsx`
- Modify: `web/src/graph/build.ts`, `web/src/graph/build.test.ts`
- Modify: `web/src/pages/Entrypoints.tsx`, `web/src/pages/Entrypoints.test.tsx`
- Modify: `web/src/pages/Edit.tsx` (`EntrypointForm`, `AddEntrypointModal`), `web/src/pages/Edit.test.tsx`
- Modify: `web/src/pages/entityParams.ts`, `web/src/pages/drafts.ts`
- Modify: `web/src/locales/en-US.ts`

**Interfaces:**
- Consumes: `GET /api/entrypoints/health`, `entrypointHealth` in the events snapshot (Task 3).
- Produces: `types.ts` `EntrypointRoute{route: string; probe?: RouteBindingProbe}`, `RouteBindingProbe{command: string; interval?: string; timeout?: string}`, `Entrypoint.routes: EntrypointRoute[]`, `EntrypointHealth`, `EntrypointHealthBinding`, `EntrypointHealthProbe`; `getEntrypointHealth(): Promise<EntrypointHealth[]>`; `App.tsx` state `entrypointHealth` set from the initial fetch and the events snapshot (Task 5 passes it to the topology; this task passes it to the Entrypoints page).
- Produces: `drafts.ts` `connectEntrypointRoute(e, routeID)` (append, no-op when bound) and `disconnectEntrypointBinding(e, routeID)` (remove one); `disconnectEntrypointRoute` clears all bindings.

- [x] **Step 1: Write the failing web tests**

- `graph/build.test.ts`: replace `route:` fixtures with `routes: [{route: 'r_...'}]`; add "draws one edge per binding" (one entrypoint, two bindings → two edges, ids `entrypoint:e->route:r1` and `entrypoint:e->route:r2`) and "a draft entrypoint has no bindings and links to unrouted".
- `Entrypoints.test.tsx`: card subtitle lists both route names with their eligibility (`未通过` / `可用`), and `回退` when the active binding is a fallback.
- `Edit.test.tsx`: saving the form round-trips an ordered binding list with a probe (command, interval, timeout) and preserves order.
- `drafts.test.ts`: `connectEntrypointRoute(e, 'r_2')` appends; connecting an already-bound route is a no-op; `disconnectEntrypointBinding(e, 'r_1')` removes only that binding; `disconnectEntrypointRoute` clears the list.

- [x] **Step 2: Run them to verify they fail**

Run: `cd web && npx vitest run src/graph/build.test.ts src/pages/Entrypoints.test.tsx src/pages/Edit.test.tsx src/pages/drafts.test.ts`
Expected: FAIL (type errors / missing bindings).

- [x] **Step 3: Implement the types and the data plumbing**

- `types.ts`: new binding/health types; `Entrypoint.route` removed; document id fields as ids.
- `client.ts`: `getEntrypointHealth()`.
- `events.ts`: `SnapshotMessage.entrypointHealth?: EntrypointHealth[]`.
- `App.tsx`: `entrypointHealth` state, initial `getEntrypointHealth()` fetch beside `getRouteHealth()` (same cancelled guard), `if (msg.entrypointHealth) setEntrypointHealth(...)`, pass to the Entrypoints page.

- [x] **Step 4: Implement the graph edges and the list page**

- `graph/build.ts`: `sub` joins route names; one edge per binding with the unchanged id `entrypoint:<id>->route:<id>`; `unconnected` when `routes.length === 0`; the unrouted edge appears only when there are no bindings.
- `Entrypoints.tsx`: subtitle from bindings plus health (`未通过` / `可用` / `回退`).

- [x] **Step 5: Implement the form and the helpers**

- `Edit.tsx`: `Form.List name="routes"` with route select, optional probe command/interval/timeout, add/remove/up-down; save maps to `routes`; `AddEntrypointModal` starts with one optional binding.
- `entityParams.ts`: show the binding list.
- `drafts.ts`: `connectEntrypointRoute`, `disconnectEntrypointBinding`, `disconnectEntrypointRoute` = clear all.
- `en-US.ts`: every new source string.

- [x] **Step 6: Run the web gate**

Run: `cd web && npm test && npx tsc --noEmit`
Expected: PASS.

- [x] **Step 7: Rebuild the bundle and the Go gate**

Run: `cd web && npm run build && cd .. && GOWORK=off go build ./... && GOWORK=off go test ./internal/admin ./internal/gateway -count=1`
Expected: PASS (`internal/admin/dist` must be fresh before the Go build).

- [x] **Step 8: Commit**

```bash
git add web
git commit -m "feat(web): edit and show entrypoint route bindings"
```

**Implementation record (fill in after the task is complete)**
- Date/status: 2026-11-12 / done. Console only: the Go API, the probe runner and
  the events snapshot are Task 3 and are consumed unchanged.
- Repository + commit: `cuttle` — `f3057424` `feat(web): edit and show
  entrypoint route bindings`, which carries this task's code, tests, the
  `AGENTS.md` invariant fix and this record. A follow-up
  `docs: record task 4 commit` commit writes this SHA only (a record cannot
  carry the SHA of the commit that contains it).
- Files changed:
  - `web/src/api/types.ts`: `EntrypointRoute` and `RouteBindingProbe`;
    `Entrypoint.routes` replaces `Entrypoint.route`; `EntrypointHealth`,
    `EntrypointHealthBinding`, `EntrypointHealthProbe` (Go JSON names
    `entrypointId`/`entrypointName`/`routeId`/`routeName`/`eligible`/`last`).
  - `web/src/api/client.ts`: `getEntrypointHealth()` (null-guarded to `[]`);
    `getConfig()` fills `routes: []` for an entrypoint the server serialized
    with `omitempty`.
  - `web/src/api/events.ts`: `SnapshotMessage.entrypointHealth` plus the same
    array-shape validation `health` gets.
  - `web/src/App.tsx`: `entrypointHealth` state, applied from the snapshot when
    the key is present (absent keeps the previous rows, matching the
    `omitempty` server side), refreshed beside `getRouteHealth()` in the
    edit-mode poll (same cancelled guard), passed to `EntrypointsPage`.
  - `web/src/graph/build.ts`: one `entrypoint:<id>->route:<id>` edge per
    binding (unchanged edge id), `sub` joins the binding route names,
    `unconnected` and the dashed amber `unrouted` edge only when there are no
    bindings. A binding whose route id is not in the config draws no edge.
  - `web/src/pages/Entrypoints.tsx`: `entrypointSubtitle` renders every
    binding with `可用`/`未通过` from health and `回退` on the active fallback
    binding; the page takes an optional `health` prop.
  - `web/src/pages/Edit.tsx`: `entrypointBindingInputs` (row list → ordered
    `routes`, blank rows and command-less probes dropped),
    `entrypointBindingRows` (a draft opens with one empty row),
    `EntrypointBindingsField` (`Form.List name="routes"` with route select,
    add/remove/up/down, probe command/interval/timeout collapsed behind a
    toggle, command required when a probe exists), `EntrypointForm` and
    `AddEntrypointModal` save `routes`; `Edit.connect` uses
    `connectEntrypointRoute` because the removed field no longer compiles; the
    entrypoint edge text now names the `routes` list.
  - `web/src/pages/entityParams.ts`: one `路由 {index}` row per binding, with
    the probe command after `·`, and `路由: 未连接` for a draft.
  - `web/src/pages/drafts.ts`: `connectEntrypointRoute` (append, no-op when the
    route is already bound), `disconnectEntrypointBinding` (remove one),
    `disconnectEntrypointRoute` (clear every binding).
  - `web/src/locales/en-US.ts`: every new Chinese string (binding form, probe
    fields, `可用`/`未通过`/`回退`, `路由 {index}`) plus the three replaced
    entrypoint strings; the retired single-`route` strings are removed.
  - `AGENTS.md`: the web invariant now lists `Entrypoint.routes[].route` and
    says a binding carries no id.
  - Tests: `web/src/graph/build.test.ts`, `web/src/pages/Entrypoints.test.tsx`,
    `web/src/pages/Edit.test.tsx`, `web/src/pages/Edit.dom.test.tsx` (real save
    round-trip, probe toggle, draft wipe, down-reorder, add modal),
    `web/src/pages/drafts.test.ts`, `web/src/pages/entityParams.test.ts`,
    `web/src/api/client.test.ts`, `web/src/api/events.test.ts`, and the
    `route:` fixtures in `web/src/App.test.tsx`, `web/src/App.dom.test.tsx`,
    `web/src/labels.test.ts`, `web/src/pages/selection.test.ts`,
    `web/src/pages/Requests.dom.test.tsx`.
- Tests/commands and results:
  - RED: `cd web && npx vitest run src/graph/build.test.ts
    src/pages/Entrypoints.test.tsx src/pages/Edit.test.tsx
    src/pages/drafts.test.ts` → 23 failed (old `route` fixtures produced
    `unrouted` edges, `connectEntrypointRoute`/`disconnectEntrypointBinding`
    missing, no binding rows).
  - GREEN: `cd web && npm test` → 404 passed (48 files); `npx tsc --noEmit` →
    clean.
  - `cd web && npm run build` → bundle rebuilt into `internal/admin/dist`
    (git-ignored); `GOWORK=off go build ./...` → clean;
    `GOWORK=off go test ./internal/admin ./internal/gateway -count=1` → ok
    (19.9s / 14.9s).
- Review findings and fixes (self-review of the diff):
  - The first version of the DOM round-trip test used an empty
    `config.entrypoints`, so the form replaced nothing; the fixture now holds
    the entrypoint being edited.
  - `UNROUTED_COLOR` became unreachable once a draft is the only unrouted
    case; removed so `noUnusedLocals` stays satisfied.
  - A probe toggled on and left blank would have saved an empty command the
    server rejects; the form requires the command and the mapping also drops a
    command-less probe.
  - English render tests (`Entrypoints`, entrypoint `entityParams`,
    `EntrypointForm`) assert no CJK leaks, so every new string is proven
    translated rather than merely present in `en-US.ts`.
- Deferred items/limitations:
  - Topology badges (`未通过`/`回退` on edges), the monitor panel's binding
    *health* rows and the edit-mode connect/disconnect rewiring (`EdgeForm`
    still clears every binding through `disconnectEntrypointRoute`) are Task 5.
    The connect path had to move to `connectEntrypointRoute` here because the
    removed field no longer compiles.
  - The binding form does not prevent picking the same route twice; the server
    rejects a duplicate with the entrypoint name and `routes[i]`, and
    `entrypointBindingInputs` deliberately does not deduplicate.
  - `entrypointHealth` is still polled only in edit mode and streamed only in
    monitor mode; nothing renders it on the topology until Task 5.

---

### Task 5: Topology badges and edit-mode wiring

**Files:**
- Modify: `web/src/pages/Topology.tsx`, `web/src/pages/edges.tsx` (edge decoration)
- Modify: `web/src/pages/Edit.tsx` (`connect`, `EdgeForm` disconnect)
- Modify: `web/src/App.tsx` (pass `entrypointHealth` to the monitor and to the entrypoint panel)
- Modify: `web/src/pages/entityParams.ts` (binding health rows in the monitor panel)
- Modify: `web/src/pages/Topology.test.tsx`, `web/src/pages/edges.test.tsx`, `web/src/pages/Edit.test.tsx`, `web/src/pages/entityParams.test.ts`

**Interfaces:**
- Consumes: `EntrypointHealth[]` and `connectEntrypointRoute` / `disconnectEntrypointBinding` from Task 4.
- Produces: `Topology` prop `entrypointHealth?: EntrypointHealth[]`; helper `bindingEdgeHealth(health)` (in `edges.tsx`) → `Map<string, {eligible: boolean; active: boolean; fallback: boolean}>` keyed by the edge id `entrypoint:<id>->route:<id>`.

- [x] **Step 1: Write the failing tests**

- `Topology.test.tsx`: an ineligible non-active binding edge is dashed amber labelled `未通过`; the active binding edge keeps its normal style; a fallback-active edge is labelled `回退`; node `dead` decoration is unchanged.
- `edges.test.tsx`: `bindingEdgeHealth` maps health rows to edge ids, keeps the first binding on duplicates, and the decoration preserves `selected`.
- `Edit.test.tsx`: dragging entrypoint → route appends a binding, a duplicate drag is a no-op, deleting an `entrypoint:<id>->route:<id>` edge removes only that binding, and deleting the last binding makes a draft.
- `entityParams.test.ts`: the monitor panel lists each binding with its state (`可用` / `未通过` / `回退`) and the last probe's exit code and latency.

- [x] **Step 2: Run them to verify they fail**

Run: `cd web && npx vitest run src/pages/Topology.test.tsx src/pages/edges.test.tsx src/pages/Edit.test.tsx src/pages/entityParams.test.ts`
Expected: FAIL.

- [x] **Step 3: Implement**

- `edges.tsx`: `bindingEdgeHealth`; extend `decorateEdge` (or a sibling) so an ineligible binding edge is dashed amber with label `未通过`, a fallback-active edge is labelled `回退`, and the existing `selected` flag is preserved by re-decorating the previous edge objects.
- `Topology`: accept `entrypointHealth` and apply the decoration in a memo that does not depend on `structure.edges`.
- `Edit.connect`: route → `connectEntrypointRoute`; edge form → `disconnectEntrypointBinding`; removing the last binding makes the entrypoint a draft.
- `App.tsx`: pass the state to the monitor; `entityParams` renders binding health.

- [x] **Step 4: Run the web gate and rebuild**

Run: `cd web && npm test && npx tsc --noEmit && npm run build`
Expected: PASS.

- [x] **Step 5: Commit**

```bash
git add web
git commit -m "feat(web): show binding eligibility on the topology"
```

**Implementation record (fill in after the task is complete)**
- Date/status: 2026-11-12 / complete
- Repository + commit: cuttle @ `b78f0563` (`feat(web): show binding eligibility
  on the topology`), recorded here by the follow-up `docs: record task 5 commit`
  commit; a record cannot carry the SHA of the commit that contains it.
- Files changed:
  - `web/src/pages/edges.tsx`: `bindingEdgeHealth(health?)` keys every binding by
    `entrypoint:<id>->route:<id>` (first row wins on duplicates), `BindingEdgeState`
    (`eligible`/`active`/`fallback`) and `bindingEdgeBadge` (`未通过` for an
    ineligible non-serving binding, `回退` for the fallback binding).
  - `web/src/pages/Topology.tsx`: `Topology` takes `entrypointHealth?`; the map is
    memoized and passed to `decorateEdge` as a sixth argument. An invalid binding
    edge is dashed amber (`#d97706`), the fallback edge keeps its normal style
    with a `回退` label, and the existing metrics/activity effect re-decorates the
    previous edge objects so `selected` survives a health snapshot. The structure
    effect still rebuilds only on config/layout changes. Node `dead` decoration
    is untouched and keeps precedence over a badge.
  - `web/src/pages/Edit.tsx`: `connectEntrypointEdge` (append; same list identity
    for a duplicate or unsupported connection) and `disconnectEntrypointEdge`
    (remove exactly the named binding via `disconnectEntrypointBinding`), used by
    `Edit.connect` and `EdgeForm`. `Edit.connect` skips the PUT on a duplicate
    drag. The entrypoint `EdgeForm` copy now says one edge is one binding and
    `断开` is disabled for an edge that names no binding (`->unrouted`).
  - `web/src/pages/entityParams.ts`: optional third argument
    `entrypointHealth`; each `路由 {index}` row appends `可用`/`未通过`, `回退` on
    the fallback binding, and `退出码 {code} · {latencyMs}ms` when `last` exists.
  - `web/src/App.tsx`: `NodePanel` takes `entrypointHealth` (used by
    `entityParams`), and the monitor `Topology` receives the state.
  - `web/src/locales/en-US.ts`: `退出码 {code}` and the replaced entrypoint-edge
    explanation; the retired string is removed.
  - Tests: `web/src/pages/Topology.test.tsx`, `web/src/pages/edges.test.tsx`,
    `web/src/pages/Edit.test.tsx`, `web/src/pages/Edit.dom.test.tsx`,
    `web/src/pages/entityParams.test.ts`, `web/src/pages/Entrypoints.test.tsx`.
- Tests/commands and results:
  - RED: `cd web && npx vitest run src/pages/Topology.test.tsx
    src/pages/edges.test.tsx src/pages/Edit.test.tsx src/pages/entityParams.test.ts`
    → 19 failed / 53 passed (`bindingEdgeHealth`, `connectEntrypointEdge` and
    `disconnectEntrypointEdge` were not exported yet; `entityParams` ignored the
    health argument).
  - `npx vitest run` on the five touched page test files plus
    `Edit.dom.test.tsx` → 101 passed.
  - GREEN: `cd web && npm test` → 428 passed (48 files); `npx tsc --noEmit` →
    clean; `npm run build` → bundle rebuilt into `internal/admin/dist`
    (git-ignored); `GOWORK=off go build ./...` → clean.
- Review findings and fixes:
  - The carried `Entrypoints.test.tsx` fixture had `active.fallback: true` with an
    eligible binding, which the server cannot produce. It is split into a
    healthy fixture (first binding eligible, active, no fallback) and an
    all-ineligible fallback fixture; the render tests assert `回退` is absent in
    the non-fallback case and that `可用` is absent when everything failed.
  - The carried `EdgeForm` mismatch is closed: disconnect no longer clears every
    binding, and the DOM test proves a two-binding entrypoint keeps its second
    binding and a single-binding entrypoint becomes a draft (`routes: []`).
  - `Topology` badges are applied by the effect that maps the *previous* edges,
    not by the structure effect, so `selected` is preserved (asserted in
    `edges.test.tsx`).
  - A duplicate drag returns the same entrypoint array, so `Edit.connect` does
    not round-trip an unchanged config (asserted in `Edit.dom.test.tsx`).
  - The badge tests pin the active language; the default test language follows
    `navigator.language`, which made `未通过` render as `Unavailable`.
- Deferred items/limitations:
  - The Entrypoints page still shows eligibility as fresh as the last topology
    visit, as accepted in the task brief.
  - The optional helper that enforces at most one probe-less binding and keeps it
    last was not added; the server still rejects the duplicate and the form does
    not enforce the order.
  - Edit-mode topology does not render the badges; the task scoped them to the
    monitor.
  - No browser visual test: the badge styling and the panel rows are verified
    only by Vitest static markup, TypeScript and the build.

---

### Task 6: Docs and end-to-end verification

**Files:**
- Modify: `README.md`, `AGENTS.md`, `docs/superpowers/specs/2026-11-12-entrypoint-multi-route-design.md` (status line), this plan
- Verify only: the running gateway

- [x] **Step 1: Finish the docs**

- README: routing section with a two-binding example and a probe script example; probe contract (`sh -c`, exit 0 = eligible, env vars, timeout, no upstream calls); ordering rule and the probe-less-last rule; all-ineligible fallback; `GET /api/entrypoints/health`; upgrade note.
- AGENTS.md: keep the invariants written in Task 1 consistent with the final implementation (probe runner ownership, outer group without filters, binding marker isolation).
- Design doc: set `Status: implemented` and record any deviation from the design.

- [x] **Step 2: Run the complete Go and web gates**

Run: `GOWORK=off go build ./... && GOWORK=off go vet ./... && GOWORK=off go test ./... -count=1`
Run: `cd web && npm test && npx tsc --noEmit && npm run build`
Expected: all PASS.

- [x] **Step 3: Run the manual smoke check**

1. Build `GOWORK=off go build -o /tmp/cuttle-smoke .`.
2. Start with a two-binding config over two local `httptest`-like upstreams (or `python3 -m http.server`-style stubs), probe command `test ! -f /tmp/cuttle-flag`.
3. With no flag: request served by binding 1 (check the recorded route in `GET /api/requests`).
4. `touch /tmp/cuttle-flag`; within one interval the next request is recorded under binding 2's route, and `GET /api/entrypoints/health` shows `eligible:false`, `active.routeId` = binding 2.
5. Remove the flag; traffic returns to binding 1.
6. Remove every binding's eligibility (make the command always fail, no probe-less binding): requests still serve binding 1 with `active.fallback:true`, and node `dead` stays false.
7. SIGHUP; confirm probe goroutines are replaced (`health.last.at` advances) and no stale entrypoint rows remain.

Record the observed route ids and health payloads in this record.

- [x] **Step 4: Commit**

```bash
git add README.md AGENTS.md docs
git commit -m "docs: document entrypoint route bindings and probe switching"
```

**Implementation record (fill in after the task is complete)**
- Date/status: 2026-11-12 / DONE. Docs finished, full gates green, manual smoke
  passed on the built binary with no data-plane defect.
- Repository + commit: `ginuerzh/cuttle`, branch `feat/entrypoint-bindings`.
  Task commit `TBD` (docs + cleanups + this record); the follow-up
  `docs: record task 6 commit` commit records its SHA and is not amended.
- Files changed:
  - `README.md` — new `Route bindings and command probes` subsection (ordered
    bindings, two-binding YAML, a probe script, the five `CUTTLE_*` variables,
    `sh -c`/exit-0 contract, no provider calls, no node `dead`, defaults and
    `timeout < interval`, one run per binding per interval, switch latency =
    interval, probe-less-last, all-ineligible fallback, health endpoint). Also:
    the literal `${VAR}` rule for entity references (`routes[].route` cannot be
    a placeholder because migration runs before `expandEnv`), the
    `GET /api/entrypoints/health` payload and `entrypointHealth` events field,
    the console table/topology description, and the binding-health vs node
    marker note.
  - `AGENTS.md` — `internal/gateway/bindingprobe.go` architecture row, probe-set
    lifecycle invariant (unstarted build, start/stop in `apply` after the swap,
    only hop close/rewriter unregister in the 30s grace), marker-derived health
    note, and `GET /api/entrypoints/health` + `entrypointHealth` admin
    invariants.
  - `docs/superpowers/specs/2026-11-12-entrypoint-multi-route-design.md` —
    `Status: implemented`, "stderr head" wording, and an
    `Implementation notes (deviations from this design)` section.
  - `internal/gateway/gateway.go` — the 30s grace comment no longer claims it
    stops probe goroutines (they stop right after the swap).
  - `internal/gateway/bindingprobe.go`, `internal/health/health.go` — comments
    say "stderr head (first 1 KiB)", matching `cappedBuffer`.
  - `web/src/pages/drafts.ts`, `web/src/pages/drafts.test.ts` — deleted the
    unused destructive `disconnectEntrypointRoute` (clear-all-bindings) helper
    and its test. `disconnectEntrypointBinding` keeps its test that a removal
    leaves the other binding intact.
  - this plan (Task 6 checkboxes + this record).
- Tests/commands and results:
  - `GOWORK=off go build ./... && GOWORK=off go vet ./... && GOWORK=off go test
    ./... -count=1` — PASS, every package `ok` (admin 19.631s, gateway 19.805s,
    stats 30.245s, root 0.694s).
  - `cd web && npm test && npx tsc --noEmit && npm run build` — `Test Files 48
    passed (48)`, `Tests 427 passed (427)` (one fewer than Task 5's 428: the
    deleted helper's test), `tsc` clean, Vite `✓ built in 8.74s` into the
    git-ignored `internal/admin/dist`.
  - `GOWORK=off go build -o /tmp/cuttle-smoke .` — PASS (`cuttle dev`).
  - `gofmt -l` was not needed (comment-only Go edits); `go vet` covers them.

  **Manual smoke** (`/tmp/cuttle-smoke serve -c /tmp/cuttle-smoke.yaml`; one
  entrypoint `e_q7a4m2xk9d` "smoke" with `match: ["*"]`; binding 0
  `r_7d2n5v8b1e` "primary" with `probe.command: test ! -f /tmp/cuttle-flag`,
  `interval: 2s`, `timeout: 1s`; binding 1 `r_f3k9x2m4qa` "secondary" with no
  probe; upstreams on `127.0.0.1:19001`/`19002`; admin on
  `127.0.0.1:18788`, password hashed with `cuttle-smoke hash-password`; stats
  `/tmp/cuttle-smoke-stats.sqlite`; data plane `127.0.0.1:18787`).

  3. No flag. Health:
     ```json
     [{"entrypointId":"e_q7a4m2xk9d","entrypointName":"smoke",
       "active":{"routeId":"r_7d2n5v8b1e","fallback":false},
       "bindings":[
         {"index":0,"routeId":"r_7d2n5v8b1e","routeName":"primary",
          "eligible":true,
          "last":{"at":"2026-11-12T05:14:57.877520293Z","ok":true,
                  "exitCode":0,"latencyMs":1}},
         {"index":1,"routeId":"r_f3k9x2m4qa","routeName":"secondary",
          "eligible":true}]}]
     ```
     Request recorded as `{"status":501,"route":"r_7d2n5v8b1e",
     "routeName":"primary","node":"n_4q7a9x2m3k","nodeName":"node-a"}`. The
     upstream was `python3 -m http.server` at this point, which answers 501 to
     POST; the assertion is the recorded route, and x marked the node dead from
     the 5xx (cleared by the SIGHUP in step 6).

  4. `touch /tmp/cuttle-flag`, waited 2 probe intervals (4s). The next request
     recorded `{"status":501,"route":"r_f3k9x2m4qa","routeName":"secondary",
     "node":"n_6b1w8z3p5c","nodeName":"node-b"}`. Health:
     ```json
     [{"entrypointId":"e_q7a4m2xk9d","entrypointName":"smoke",
       "active":{"routeId":"r_f3k9x2m4qa","fallback":false},
       "bindings":[
         {"index":0,"routeId":"r_7d2n5v8b1e","routeName":"primary",
          "eligible":false,
          "last":{"at":"2026-11-12T05:15:09.877586893Z","ok":false,
                  "exitCode":1,"latencyMs":1,"error":"exit status 1"}},
         {"index":1,"routeId":"r_f3k9x2m4qa","routeName":"secondary",
          "eligible":true}]}]
     ```

  5. `rm /tmp/cuttle-flag`, waited 4s. The next request recorded
     `"route":"r_7d2n5v8b1e"` again. Health (`active` and per-binding rows as
     returned):
     ```json
     {"active":{"routeId":"r_7d2n5v8b1e","fallback":false},
      "bindings":[
        {"index":0,"routeId":"r_7d2n5v8b1e","eligible":true,
         "last":{"ok":true,"exitCode":0,
                 "at":"2026-11-12T05:15:15.877469444Z"}},
        {"index":1,"routeId":"r_f3k9x2m4qa","eligible":true,"last":null}]}
     ```

  6. Every binding ineligible. Config edited to add
     `probe: {command: "false", interval: 2s, timeout: 1s}` to binding 1,
     `/tmp/cuttle-flag` touched, upstreams replaced by 200-returning stubs, then
     `SIGHUP`. The request recorded
     `{"status":200,"route":"r_7d2n5v8b1e","routeName":"primary",
     "node":"n_4q7a9x2m3k"}` with health:
     ```json
     [{"entrypointId":"e_q7a4m2xk9d","entrypointName":"smoke",
       "active":{"routeId":"r_7d2n5v8b1e","fallback":true},
       "bindings":[
         {"index":0,"routeId":"r_7d2n5v8b1e","routeName":"primary",
          "eligible":false,
          "last":{"at":"2026-11-12T05:15:53.753890889Z","ok":false,
                  "exitCode":1,"latencyMs":1,"error":"exit status 1"}},
         {"index":1,"routeId":"r_f3k9x2m4qa","routeName":"secondary",
          "eligible":false,
          "last":{"at":"2026-11-12T05:15:53.753893613Z","ok":false,
                  "exitCode":1,"latencyMs":1,"error":"exit status 1"}}]}]
     ```
     `GET /api/routes/health` reported for both routes
     `"failCount":0,"dead":false` (node-a and node-b), so no node was marked
     dead by the probes.

  7. Second `SIGHUP` with no config change. `bindings[0].last.at` advanced from
     `2026-11-12T05:15:57.754589231Z` to `2026-11-12T05:16:00.483719928Z` (and
     `bindings[1]` to `...00.483696948Z`), i.e. the new probe set ran
     immediately; `GET /api/entrypoints/health` answered 200 with the same
     single `e_q7a4m2xk9d` row (no stale rows), `active` still
     `{"routeId":"r_7d2n5v8b1e","fallback":true}`. Log showed
     `config reloaded` then both `binding ineligible (exit 1)` lines.
- Review findings and fixes: Task 6 had no external review round; self-review of
  the diff found and fixed one inaccuracy of my own: the README first said
  `last.error` was always stderr, but the runner falls back to the exec error
  string when stderr is empty (the smoke showed `"exit status 1"`). The carried
  cleanups are closed: the grace-window comment, the stderr head wording in both
  files, the deletion of `disconnectEntrypointRoute` + its test, and the
  `${VAR}` route-reference note. AGENTS.md was checked line by line against
  `build.go` (`FIFO` outer selector with no filters, inner
  `FIFO + FailFilter(1, failTimeout)`, `bindingFailTimeout` = 2× max interval
  floored at 10s), `bindingprobe.go` (one goroutine per probed binding, markers
  only, no `failCodes`/stats access) and `gateway.go` (start after swap, stop
  immediately, failed build starts nothing): no drift, only the missing
  lifecycle and admin-health invariants were added.
- Deferred items/limitations:
  - The smoke's step 3-5 upstream answered 501, which x treats as a node failure;
    the node badges were therefore transiently dead until the step-6 `SIGHUP`
    rebuilt the hop. Not a defect: node `failCodes` behavior is unchanged and
    separate from binding eligibility, as documented.
  - Probe health is memory-only. A restart or reload drops `last` until the
    immediate first run, so there is a sub-second window with no `last` row.
  - Unchanged Task 5 limitations: no browser visual regression tests (the
    bindings UI is covered by Vitest/TypeScript only), edit-mode topology does
    not show binding badges, and the probe-less-last rule is enforced by the
    server rather than the form.
  - `/api/events` omits `entrypointHealth` when the list is empty (`omitempty`);
    clients must treat an absent field as "no binding health".

