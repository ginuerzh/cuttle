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
// Two entries with markers, command `test -f <flag>`; probeOnce: both marked.
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
- `Gateway` gains `probes *probeSet`. `apply`: after the hop swap and `g.forwarder.Forward(group)`, call `probes.Start(context.Background())`; in the existing `time.AfterFunc(30*time.Second, ...)` grace block, call `oldProbes.Stop()` beside the old hop close and rewriter unregister. A failed build starts nothing.
- `Gateway.EntrypointHealth()` returns `g.probes.Snapshot()` under `g.mu`, nil-safe.

- [ ] **Step 4b: Add the end-to-end switch test**

`internal/gateway/probe_e2e_test.go`:

```go
func TestEntrypointSwitchesRouteByProbe(t *testing.T)
// Two routes over two captureUpstream servers, bindings [r_1(probe), r_2(none)].
// Probe command: `test -f <flag>`. Start gateway with interval 20ms; first
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
- Date/status: 2026-11-12 / done.
- Repository + commit: `cuttle` — `7c98872f` `feat(gateway): switch route
  bindings with a command probe`.
- Files changed:
  - `internal/gateway/bindingprobe.go` (new): `probeSet` (`add`, `Start`,
    `Stop`, `probeOnce`, `Snapshot`), `bindingProbe`, `probeState`, the
    `shellCmd` (`sh -c`, `cmd /C`) and `cappedBuffer` (1 KiB stderr) helpers.
    One goroutine per probed binding; it runs immediately and then once per
    interval, `exec.CommandContext` with the probe timeout, `WaitDelay` so a
    shell's grandchild cannot block the runner, and
    `CUTTLE_ENTRYPOINT_ID/NAME`, `CUTTLE_ROUTE_ID/NAME`,
    `CUTTLE_BINDING_INDEX` in the environment. Success resets, failure marks
    every variant `*xhop.HopEntry` of that binding; node `failCodes` markers and
    the stats store are untouched. Debug per run, info only on a verdict
    transition, never the command or its output.
  - `internal/gateway/bindingprobe_test.go` (new): the seven tests named in the
    brief plus `TestEntrypointHealthNilSafe`.
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
- Deferred items/limitations: the admin endpoint, the events snapshot field and
  the console (tasks 3-6) are not wired yet; `Gateway.EntrypointHealth()` is the
  only new entry point. Probes start during `apply`, so they already run before
  `Gateway.Start` binds the listener, which is what the first-request test
  relies on. Bindings that bypass `config.Load` get the 30s/5s probe defaults.

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

- [ ] **Step 1: Write the failing tests**

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

- [ ] **Step 2: Run them to verify they fail**

Run: `GOWORK=off go test ./internal/admin -run 'TestEntrypointHealth|TestSnapshotCarriesEntrypointHealth' -count=1`
Expected: FAIL (404 or missing field).

- [ ] **Step 3: Implement the endpoint and the snapshot field**

- `admin.Server`: `entrypointHealth func() []health.Entrypoint`; wire from the router in `New` the same way as `routeHealth`; `api.HandleFunc("GET /api/entrypoints/health", s.handleEntrypointHealth)`.
- `handleEntrypointHealth`: nil function → `writeJSON(w, 200, []health.Entrypoint{})`.
- `handlers_events.go` snapshot struct: `EntrypointHealth []health.Entrypoint \`json:"entrypointHealth,omitempty"\``, filled from the same function.

- [ ] **Step 4: Run the admin tests and the full gate**

Run: `GOWORK=off go test ./internal/admin -count=1 && GOWORK=off go test ./... -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/admin
git commit -m "feat(admin): expose entrypoint binding health"
```

**Implementation record (fill in after the task is complete)**
- Date/status:
- Repository + commit:
- Files changed:
- Tests/commands and results:
- Review findings and fixes:
- Deferred items/limitations:

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

- [ ] **Step 1: Write the failing web tests**

- `graph/build.test.ts`: replace `route:` fixtures with `routes: [{route: 'r_...'}]`; add "draws one edge per binding" (one entrypoint, two bindings → two edges, ids `entrypoint:e->route:r1` and `entrypoint:e->route:r2`) and "a draft entrypoint has no bindings and links to unrouted".
- `Entrypoints.test.tsx`: card subtitle lists both route names with their eligibility (`未通过` / `可用`), and `回退` when the active binding is a fallback.
- `Edit.test.tsx`: saving the form round-trips an ordered binding list with a probe (command, interval, timeout) and preserves order.
- `drafts.test.ts`: `connectEntrypointRoute(e, 'r_2')` appends; connecting an already-bound route is a no-op; `disconnectEntrypointBinding(e, 'r_1')` removes only that binding; `disconnectEntrypointRoute` clears the list.

- [ ] **Step 2: Run them to verify they fail**

Run: `cd web && npx vitest run src/graph/build.test.ts src/pages/Entrypoints.test.tsx src/pages/Edit.test.tsx src/pages/drafts.test.ts`
Expected: FAIL (type errors / missing bindings).

- [ ] **Step 3: Implement the types and the data plumbing**

- `types.ts`: new binding/health types; `Entrypoint.route` removed; document id fields as ids.
- `client.ts`: `getEntrypointHealth()`.
- `events.ts`: `SnapshotMessage.entrypointHealth?: EntrypointHealth[]`.
- `App.tsx`: `entrypointHealth` state, initial `getEntrypointHealth()` fetch beside `getRouteHealth()` (same cancelled guard), `if (msg.entrypointHealth) setEntrypointHealth(...)`, pass to the Entrypoints page.

- [ ] **Step 4: Implement the graph edges and the list page**

- `graph/build.ts`: `sub` joins route names; one edge per binding with the unchanged id `entrypoint:<id>->route:<id>`; `unconnected` when `routes.length === 0`; the unrouted edge appears only when there are no bindings.
- `Entrypoints.tsx`: subtitle from bindings plus health (`未通过` / `可用` / `回退`).

- [ ] **Step 5: Implement the form and the helpers**

- `Edit.tsx`: `Form.List name="routes"` with route select, optional probe command/interval/timeout, add/remove/up-down; save maps to `routes`; `AddEntrypointModal` starts with one optional binding.
- `entityParams.ts`: show the binding list.
- `drafts.ts`: `connectEntrypointRoute`, `disconnectEntrypointBinding`, `disconnectEntrypointRoute` = clear all.
- `en-US.ts`: every new source string.

- [ ] **Step 6: Run the web gate**

Run: `cd web && npm test && npx tsc --noEmit`
Expected: PASS.

- [ ] **Step 7: Rebuild the bundle and the Go gate**

Run: `cd web && npm run build && cd .. && GOWORK=off go build ./... && GOWORK=off go test ./internal/admin ./internal/gateway -count=1`
Expected: PASS (`internal/admin/dist` must be fresh before the Go build).

- [ ] **Step 8: Commit**

```bash
git add web
git commit -m "feat(web): edit and show entrypoint route bindings"
```

**Implementation record (fill in after the task is complete)**
- Date/status:
- Repository + commit:
- Files changed:
- Tests/commands and results:
- Review findings and fixes:
- Deferred items/limitations:

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

- [ ] **Step 1: Write the failing tests**

- `Topology.test.tsx`: an ineligible non-active binding edge is dashed amber labelled `未通过`; the active binding edge keeps its normal style; a fallback-active edge is labelled `回退`; node `dead` decoration is unchanged.
- `edges.test.tsx`: `bindingEdgeHealth` maps health rows to edge ids, keeps the first binding on duplicates, and the decoration preserves `selected`.
- `Edit.test.tsx`: dragging entrypoint → route appends a binding, a duplicate drag is a no-op, deleting an `entrypoint:<id>->route:<id>` edge removes only that binding, and deleting the last binding makes a draft.
- `entityParams.test.ts`: the monitor panel lists each binding with its state (`可用` / `未通过` / `回退`) and the last probe's exit code and latency.

- [ ] **Step 2: Run them to verify they fail**

Run: `cd web && npx vitest run src/pages/Topology.test.tsx src/pages/edges.test.tsx src/pages/Edit.test.tsx src/pages/entityParams.test.ts`
Expected: FAIL.

- [ ] **Step 3: Implement**

- `edges.tsx`: `bindingEdgeHealth`; extend `decorateEdge` (or a sibling) so an ineligible binding edge is dashed amber with label `未通过`, a fallback-active edge is labelled `回退`, and the existing `selected` flag is preserved by re-decorating the previous edge objects.
- `Topology`: accept `entrypointHealth` and apply the decoration in a memo that does not depend on `structure.edges`.
- `Edit.connect`: route → `connectEntrypointRoute`; edge form → `disconnectEntrypointBinding`; removing the last binding makes the entrypoint a draft.
- `App.tsx`: pass the state to the monitor; `entityParams` renders binding health.

- [ ] **Step 4: Run the web gate and rebuild**

Run: `cd web && npm test && npx tsc --noEmit && npm run build`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web
git commit -m "feat(web): show binding eligibility on the topology"
```

**Implementation record (fill in after the task is complete)**
- Date/status:
- Repository + commit:
- Files changed:
- Tests/commands and results:
- Review findings and fixes:
- Deferred items/limitations:

---

### Task 6: Docs and end-to-end verification

**Files:**
- Modify: `README.md`, `AGENTS.md`, `docs/superpowers/specs/2026-11-12-entrypoint-multi-route-design.md` (status line), this plan
- Verify only: the running gateway

- [ ] **Step 1: Finish the docs**

- README: routing section with a two-binding example and a probe script example; probe contract (`sh -c`, exit 0 = eligible, env vars, timeout, no upstream calls); ordering rule and the probe-less-last rule; all-ineligible fallback; `GET /api/entrypoints/health`; upgrade note.
- AGENTS.md: keep the invariants written in Task 1 consistent with the final implementation (probe runner ownership, outer group without filters, binding marker isolation).
- Design doc: set `Status: implemented` and record any deviation from the design.

- [ ] **Step 2: Run the complete Go and web gates**

Run: `GOWORK=off go build ./... && GOWORK=off go vet ./... && GOWORK=off go test ./... -count=1`
Run: `cd web && npm test && npx tsc --noEmit && npm run build`
Expected: all PASS.

- [ ] **Step 3: Run the manual smoke check**

1. Build `GOWORK=off go build -o /tmp/cuttle-smoke .`.
2. Start with a two-binding config over two local `httptest`-like upstreams (or `python3 -m http.server`-style stubs), probe command `test -f /tmp/cuttle-flag`.
3. With no flag: request served by binding 1 (check the recorded route in `GET /api/requests`).
4. `touch /tmp/cuttle-flag`; within one interval the next request is recorded under binding 2's route, and `GET /api/entrypoints/health` shows `eligible:false`, `active.routeId` = binding 2.
5. Remove the flag; traffic returns to binding 1.
6. Remove every binding's eligibility (make the command always fail, no probe-less binding): requests still serve binding 1 with `active.fallback:true`, and node `dead` stays false.
7. SIGHUP; confirm probe goroutines are replaced (`health.last.at` advances) and no stale entrypoint rows remain.

Record the observed route ids and health payloads in this record.

- [ ] **Step 4: Commit**

```bash
git add README.md AGENTS.md docs
git commit -m "docs: document entrypoint route bindings and probe switching"
```

**Implementation record (fill in after the task is complete)**
- Date/status:
- Repository + commit:
- Files changed:
- Tests/commands and results:
- Review findings and fixes:
- Deferred items/limitations:
