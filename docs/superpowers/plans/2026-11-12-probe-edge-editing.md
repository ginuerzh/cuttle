# Probe Edge Editing and Binding Priority Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move probe editing to the `entrypoint → route` edge in the console, allow any number of probe-less bindings in any order, and make probed bindings outrank probe-less ones (probe-less only serves when every probe fails).

**Architecture:** The config schema is unchanged (the probe stays inline on `entrypoint.routes[]`). The only data-plane change is the order of entries fed to the existing inner hop group: probed bindings first, then probe-less, each in configuration order. FIFO + `FailFilter` then yields the required priority, and the health snapshot walks the same order. The console moves the probe form fields from the entrypoint drawer to the edge panel.

**Tech Stack:** Go 1.27, `github.com/go-gost/x v0.32.0`, React 18 + Ant Design + React Flow console, vitest.

**Spec:** `docs/superpowers/specs/2026-11-12-probe-edge-editing-design.md`

## Global Constraints

- `GOWORK=off` for every Go command; no `go.mod`/`go.sum` change, no `replace`, no sibling-repo edits.
- No schema change: `entrypoint.routes[]` keeps `{route, probe?}`; no new entity ids.
- The probe stays command-only (`sh -c`, exit 0 = eligible) and never calls a provider.
- The outer hop group (entrypoints) stays a bare FIFO with no filters; only the inner binding group is reordered.
- All probes failing with no probe-less binding falls back to the first probed binding (never 502).
- UI source strings are Chinese with `en-US` keys; code/comments/docs/commits are English.
- Commit after each task, including that task's Implementation record. Never push.

## Review Focus

- A probe-less binding placed before probed bindings must not shadow them (the point of the category priority).
- All probes failed with a probe-less binding present must serve the **first probe-less** binding, not the first probed one.
- All probes failed with no probe-less binding must still serve something (first probed), never 502.
- Saving the entrypoint drawer (which no longer shows probes) must never clear an existing probe.
- `fallback` must stay false for an entrypoint that has no probes at all.

---

### Task 1: Priority selection, relaxed validation, health and docs

**Files:**
- Modify: `internal/config/config.go`, `internal/config/config_test.go`
- Modify: `internal/gateway/build.go`, `internal/gateway/bindings_test.go`
- Modify: `internal/gateway/bindingprobe.go`, `internal/gateway/bindingprobe_test.go`
- Modify: `internal/gateway/probe_e2e_test.go`
- Modify: `README.md`, `AGENTS.md`, `docs/superpowers/specs/2026-11-12-entrypoint-multi-route-design.md`
- Create: `docs/superpowers/specs/2026-11-12-probe-edge-editing-design.md`, this plan

**Interfaces:**
- Produces: `bindingOrder(bindings []gwconfig.RouteBinding) (order []int, unreachable []int)` in `internal/gateway/build.go` — `order` lists probed bindings first then probe-less, each in configuration order; `unreachable` lists the configuration indexes of probe-less bindings after the first.
- Keeps: `buildBindingsHop(entrypointID string, bindings []gwconfig.RouteBinding, plans map[string]*routePlan, api provider.API, failTimeout time.Duration, log logger.Logger) (hop.Hop, []*xhop.HopEntry, error)` returns entries index-aligned with `bindings`; the group receives them in `bindingOrder` order.
- Keeps: `health.Binding.Index` = configuration index; `bindings[]` is emitted in priority order.

- [x] **Step 1: Write the failing config tests**

```go
func TestValidateAllowsMultipleProbeLessBindings(t *testing.T)
// five probe-less bindings, and a probe-less binding followed by a probed one:
// Load succeeds with no error.

func TestValidateStillRejectsBadBindings(t *testing.T)
// duplicate route, blank route, unknown route, blank command,
// interval < 1s, timeout >= interval still fail with `routes[%d]` scoped text.
```

- [x] **Step 2: Run them to verify they fail**

Run: `GOWORK=off go test ./internal/config -run 'TestValidateAllowsMultipleProbeLessBindings|TestValidateStillRejectsBadBindings' -count=1`
Expected: FAIL on the multiple-probe-less case ("must be last").

- [x] **Step 3: Remove the placement rule**

Delete the probe-less-last branch in `Validate`; keep every other binding rule. Update the existing table test that asserted the rule (convert its cases into `TestValidateAllowsMultipleProbeLessBindings`).

- [x] **Step 4: Write the failing selection and health tests**

`internal/gateway/bindings_test.go`:

```go
func TestProbedBindingsOutrankProbeLess(t *testing.T)
// bindings [probe-less r_1, probed r_2]; mark nothing: Select returns r_2.
func TestFirstEligibleProbedWins(t *testing.T)
// three probed bindings; mark the first: Select returns the second.
func TestAllProbesFailServesFirstProbeLess(t *testing.T)
// [probed, probe-less, probed, probe-less]; mark both probed: Select returns
// the first probe-less.
func TestNoProbeLessFallsBackToFirstProbe(t *testing.T)
// two probed, both marked: Select returns the first probed (no nil).
func TestOnlyProbeLessServesFirst(t *testing.T)
// three probe-less: Select returns the first.
func TestBindingOrderHelper(t *testing.T)
// [probe-less, probed, probe-less, probed] -> order [1,3,0,2],
// unreachable [2].
```

`internal/gateway/bindingprobe_test.go`:

```go
func TestSnapshotPriorityOrderAndFallback(t *testing.T)
// payload order is priority order with Index = configuration index;
// fallback true only for the all-probes-failed + probe-less case;
// false for an entrypoint with no probes.
```

- [x] **Step 5: Run them to verify they fail**

Run: `GOWORK=off go test ./internal/gateway -run 'TestProbedBindingsOutrankProbeLess|TestFirstEligibleProbedWins|TestAllProbesFailServesFirstProbeLess|TestNoProbeLessFallsBackToFirstProbe|TestOnlyProbeLessServesFirst|TestBindingOrderHelper|TestSnapshotPriorityOrderAndFallback' -count=1`
Expected: FAIL (undefined `bindingOrder`, wrong selection).

- [x] **Step 6: Implement `bindingOrder`, the group order and the snapshot order**

- `bindingOrder` returns the two slices described in Interfaces.
- `buildBindingsHop` builds the index-aligned entries as today, then passes `order` to `xhop.WithEntriesOption`; keep the returned slice index-aligned. Log one `Warnf` per unreachable binding: `entrypoint %s: routes[%d] is a probe-less binding after another probe-less binding; it can never serve`.
- `Snapshot` emits bindings in `bindingOrder` order (skip the helper when there are no probes) and picks `active` by walking that order. `fallback` is true only when at least one binding has a probe and none of the probed bindings is eligible.
- Keep the single marker read per binding.

- [x] **Step 7: Add the mixed end-to-end test**

`internal/gateway/probe_e2e_test.go`: three bindings over three capture upstreams — probe A, probe B, probe-less C. With both flags allowing A and B, traffic hits A; make A fail → B; make B fail → C; restore A → A. Assert both probes stay off the upstream hit counters and no node reports `dead`.

- [x] **Step 8: Run the gateway tests and the full gate**

Run: `GOWORK=off go test ./internal/gateway -run 'TestBinding|TestProbe|TestSnapshot|TestEntrypointSwitches|TestEntrypointPriority' -count=1 -race`
Run: `GOWORK=off go build ./... && GOWORK=off go vet ./... && GOWORK=off go test ./... -count=1`
Expected: PASS.

- [x] **Step 9: Update the docs**

- `README.md`: probe editing happens on the edge; priority rules (probed first in config order, then the first probe-less); multiple probe-less bindings allowed; unreachable extras warning; the three fallback cases; the entrypoint `routes[]` table keeps `probe` (config still supports it).
- `AGENTS.md`: replace "a probe-less binding may appear at most once and only last" with the category-priority invariant and the warning rule.
- Original design doc: update §1 (no placement rule), §3 (priority algorithm, fallback cases), §5 (health order/fallback, console editing on the edge), and add a one-line amendment note pointing at the new spec.

- [x] **Step 10: Commit**

```bash
git add internal/config internal/gateway README.md AGENTS.md docs/superpowers
git commit -m "feat(gateway): probed bindings outrank probe-less bindings"
```

**Implementation record (fill in after the task is complete)**
- Date/status: 2026-11-12 / DONE (code, tests, docs).
- Repository + commit: `cuttle`, branch `feat/probe-edge-editing`. Code commit
  `e753beb1` — `feat(gateway): probed bindings outrank probe-less bindings`.
  This SHA is recorded by the follow-up docs commit (`docs: record task 1
  commit`), because a commit cannot contain its own hash.
- Files changed:
  - `internal/config/config.go` — removed the probe-less-last validation branch;
    every other binding rule is unchanged.
  - `internal/config/config_test.go` — replaced `TestValidateEntrypointRouteBindings`
    with `TestValidateAllowsMultipleProbeLessBindings` (valid cases incl. five
    probe-less bindings, a probe-less binding before a probed one, and a `Load`
    from YAML) and `TestValidateStillRejectsBadBindings` (blank/duplicate/unknown
    route, blank command, `interval < 1s`, `timeout < 1s`, `timeout >= interval`).
  - `internal/gateway/build.go` — new `bindingOrder` / `bindingHasProbe`;
    `buildBindingsHop` feeds the group entries in priority order while returning
    the index-aligned per-binding entries; `buildHopGroup` logs one `Warnf` per
    unreachable probe-less binding.
  - `internal/gateway/bindings_test.go` — new selection tests
    (`TestProbedBindingsOutrankProbeLess`, `TestFirstEligibleProbedWins`,
    `TestAllProbesFailServesFirstProbeLess`, `TestNoProbeLessFallsBackToFirstProbe`,
    `TestOnlyProbeLessServesFirst`, `TestBindingOrderHelper`,
    `TestUnreachableProbeLessBindingWarns`), a `bindingPriorityFixture` /
    `selectBindingNode` helper pair, and updated fixture comments.
  - `internal/gateway/bindingprobe.go` — `Snapshot` emits bindings in priority
    order, reads each binding's marker once, and computes `fallback` as "a probe
    exists and no probed binding is eligible"; new `probePriorityOrder`.
  - `internal/gateway/bindingprobe_test.go` — new
    `TestSnapshotPriorityOrderAndFallback`; `assertSnapshotConsistent` now takes
    the probed configuration indexes, because a probe-less binding may serve
    while `fallback` is true.
  - `internal/gateway/probe_e2e_test.go` — new
    `TestEntrypointPriorityAcrossProbedAndProbeLess` (two probed bindings + one
    probe-less, traffic A → B → C → A); `waitBindingEligibility` looks a binding
    up by `Index` instead of slice position; new `assertActive` helper.
  - `internal/health/health.go` — comments only: `bindings[]` priority order and
    the `fallback` rule.
  - `README.md`, `AGENTS.md`, `example.yaml`,
    `docs/superpowers/specs/2026-11-12-entrypoint-multi-route-design.md` — the
    new priority/validation/health semantics and the edge-editing note.
  - `docs/superpowers/specs/2026-11-12-probe-edge-editing-design.md` and this
    plan (previously untracked) are included in the commit.
- Tests/commands and results:
  - `GOWORK=off go test ./internal/config -run
    'TestValidateAllowsMultipleProbeLessBindings|TestValidateStillRejectsBadBindings'
    -count=1` — RED then PASS.
  - `GOWORK=off go test ./internal/gateway -run
    'TestProbedBindingsOutrankProbeLess|TestFirstEligibleProbedWins|TestAllProbesFailServesFirstProbeLess|TestNoProbeLessFallsBackToFirstProbe|TestOnlyProbeLessServesFirst|TestBindingOrderHelper|TestSnapshotPriorityOrderAndFallback|TestUnreachableProbeLessBindingWarns|TestEntrypointPriorityAcrossProbedAndProbeLess'
    -count=1` — RED then PASS.
  - `GOWORK=off go test ./internal/gateway -run
    'TestBinding|TestProbe|TestSnapshot|TestEntrypointSwitches|TestEntrypointPriority'
    -count=1 -race` — PASS.
  - `GOWORK=off go build ./... && GOWORK=off go vet ./... && GOWORK=off go test
    ./... -count=1` — PASS (all packages).
- Review findings and fixes (self-review):
  - The unreachable warning is logged in `buildHopGroup` once per entrypoint, not
    in `buildBindingsHop`, which runs once per inbound variant; otherwise an
    entrypoint would warn up to five times.
  - `bindingOrder` and `Snapshot` classify a probe with a blank command as
    probe-less (`bindingHasProbe`), so a build that bypasses `config.Validate`
    keeps the group order and the health order aligned.
  - `assertSnapshotConsistent` previously asserted that `fallback` implies no
    eligible binding at all. Under the amended semantics a probe-less binding is
    eligible while serving as the fallback, so the assertion now checks the probed
    indexes only.
  - `TestBindingFixtureIsSchemaValid` was kept (with an updated comment) instead
    of deleted: the fixture must still load, and the relaxed rule is covered by
    the new config tests.
- Deferred items/limitations:
  - `web/src/locales/en-US.ts` still carries the old hint ("... must be last");
    Task 2 updates the console hint and the edge editor.
  - The health payload does not expose whether a binding has a probe (no schema
    change allowed); the console derives it from `entrypoints[].routes[].probe`.

---

### Task 2: Console — probe editing on the edge, drawer without probes

**Files:**
- Modify: `web/src/pages/Edit.tsx` (`EntrypointForm`, `EdgeForm`), `web/src/pages/Edit.test.tsx`, `web/src/pages/Edit.dom.test.tsx`
- Modify: `web/src/pages/Entrypoints.tsx` (hint text), `web/src/pages/Entrypoints.test.tsx` if the hint is asserted
- Modify: `web/src/locales/en-US.ts`
- Modify: `README.md` (console section, only if Task 1 left it incomplete)

**Interfaces:**
- Consumes: `Entrypoint.routes: EntrypointRoute[]` with `probe?: RouteBindingProbe`; `putEntrypoints(list)`; `entrypointEdgeRoute(id)` and `disconnectEntrypointEdge(list, id)` already exist.
- Produces: `bindingEdgeHealth(health?, entrypoints?) : Map<string, BindingEdgeState>` where `BindingEdgeState = {eligible: boolean; active: boolean; fallback: boolean; hasProbe: boolean}` — `hasProbe` comes from the config binding (`entrypoints[].routes[].route === routeId && !!probe`), so the health payload is unchanged.
- Produces: `bindingEdgeBadge(state?) : BindingEdgeBadge` where `BindingEdgeBadge = {mark?: '✕'; text?: string; invalid: boolean; dashed: boolean}`. Rules: no state → all false/absent; `hasProbe && !eligible` → `mark: '✕'`, `invalid: true`, solid; `dashed = !hasProbe`; `fallback` adds `text: translate('回退')`; eligible active → all false/absent.
- Produces: pure helpers `probeFromForm(values): RouteBindingProbe | undefined` and `withBindingProbe(entrypoints, entrypointID, routeID, probe)` in `Edit.tsx` (or `drafts.ts` if that keeps the file smaller) — the tests target the helpers plus the save payloads.

- [x] **Step 1: Write the failing tests**

- `Edit.test.tsx`: `withBindingProbe` sets/replaces/clears (`undefined` → field removed) only the named binding and leaves every other binding and probe untouched.
- `Edit.dom.test.tsx`: saving the entrypoint drawer after a reorder/removal keeps each remaining binding's existing probe (payload has the same `probe` objects), and a newly added row has none.
- `Edit.dom.test.tsx`: the edge form saves `command`/`interval`/`timeout` into the correct binding through `PUT /api/entrypoints`, an empty command removes the probe, and the command control is a multiline textarea.
- `Topology.test.tsx`: a binding without a probe is dashed and unlabelled; a
  failing probe binding is solid amber with `✕`; the fallback-active binding
  reads `✕ 回退` (probed) or `回退` (probe-less); an eligible active binding is
  solid and unlabelled; node `dead` decoration is unchanged.
- `Edit.test.tsx`: the drawer no longer renders probe inputs (query by the old placeholder/label yields nothing).

- [x] **Step 2: Run them to verify they fail**

Run: `cd web && npx vitest run src/pages/Edit.test.tsx src/pages/Edit.dom.test.tsx`
Expected: FAIL.

- [x] **Step 3: Implement the drawer change (probe-preserving save)**

- `EntrypointForm`: binding rows become route select + up/down + delete. On save, map each row's route id back to the existing binding and carry its `probe`; the row order comes from the form. Update the hint: order decides among probed bindings; probes are edited on the topology edge; probe-less bindings only serve when every probe fails.
- `AddEntrypointModal` unchanged (it creates no bindings).

- [x] **Step 4: Implement the edge form**

- `EdgeForm` entrypoint branch: route label, `command` as an auto-sized multiline `Input.TextArea` (monospace), `interval`/`timeout` (Input, default shown), the same duration validation the drawer used (`probeDurationMs`, `probeIntervalError`, `probeTimeoutError`), Save + Disconnect.
- Save uses `withBindingProbe` and `putEntrypoints`, then `onSaved`.
- `edges.tsx`: `bindingEdgeHealth(health, entrypoints)` also records whether the
  config binding has a probe; the badge helper returns `✕` for a failing probe
  binding, `✕ 回退` for the probed fallback, `回退` for a probe-less fallback,
  and nothing otherwise. A probe-less edge draws dashed; a failing-probe edge
  draws solid amber; the eligible active edge stays solid. Keep node `dead`
  precedence and the `selected` flag, and keep the traffic label combination.
- `en-US.ts`: all new strings; remove the now-unused `未通过` edge key if nothing else uses it.

- [x] **Step 5: Run the web gate and rebuild**

Run: `cd web && npm test && npx tsc --noEmit && npm run build && cd .. && GOWORK=off go build ./...`
Expected: PASS.

- [x] **Step 6: Commit**

```bash
git add web README.md
git commit -m "feat(web): edit binding probes on the topology edge"
```

**Implementation record**
- Date/status: 2026-11-12 / DONE (drawer, edge panel, edge styling, locale,
  carried cleanups; web + Go gates green).
- Repository + commit: `cuttle`, branch `feat/probe-edge-editing`, work done on
  top of `4728975d`. Code commit `feat(web): edit binding probes on the
  topology edge`; its SHA is recorded by the follow-up commit
  `docs: record task 2 commit`, because a commit cannot contain its own hash.
  The code, tests, docs and this record share the code commit.
- Files changed:
  - `web/src/pages/Edit.tsx` — `EntrypointBindingRow` keeps only `route`, so
    the drawer row is route select + up/down + delete; `entrypointBindingInputs
    (rows, existing)` re-attaches each route's existing probe by route id (the
    same object, never rebuilt) and gives a newly added row none; new pure
    helpers `probeFromForm(values)` and `withBindingProbe(entrypoints,
    entrypointID, routeID, probe)` (clearing drops the field instead of writing
    `probe: undefined`); the drawer hint says probes are edited on the topology
    edge and that order only decides between probed bindings; the
    `entrypoint:` branch became its own `EntrypointEdgeForm` (entrypoint → route
    label, route id, auto-sized monospace `Input.TextArea` command,
    interval/timeout inputs that reuse `probeDurationMs` /
    `probeIntervalError` / `probeTimeoutError`, Save through
    `withBindingProbe` + `putEntrypoints`, Disconnect unchanged); `Edit` mounts
    `EdgeForm` with `key={selection.id}` so a new selection remounts the form
    instead of reusing one seeded from another binding.
  - `web/src/pages/edges.tsx` — `BindingEdgeState` gains `hasProbe`, derived
    from `entrypoints[].routes[].probe` through a `configuredProbes` helper
    (no API field); `bindingEdgeHealth(health?, entrypoints?)` takes the config;
    `BindingEdgeBadge` gains `mark: '✕'` and `dashed`, drops the `未通过` text,
    and keeps `invalid` for the amber styling.
  - `web/src/pages/Topology.tsx` — `bindingEdgeHealth(entrypointHealth,
    config.entrypoints)`; the label is `mark` + `text` (`✕`, `✕ 回退`, `回退`);
    the dash follows `badge.dashed` and the amber follows `badge.invalid`; node
    `dead` precedence, the traffic-label combination and the
    re-decorate-in-place `selected` preservation are unchanged. Binding state
    is a monitor-mode decoration: `App.handleSnapshot` stops pushing graph
    state in edit mode and `Edit` mounts `Topology` with `metrics={null}` and
    no `entrypointHealth`.
  - `web/src/api/types.ts` — comments only: bindings are in priority order,
    `index` is the configuration index, a probe-less binding need not be last.
  - `web/src/locales/en-US.ts` — added the new drawer hint, the edge-panel
    hint, the command hint and “This binding no longer exists.”; removed the
    stale “must be last” hint, the probe toggle keys and the empty-command key.
    `未通过` stays: it is still the entrypoint list and detail-panel state word
    (`Entrypoints.tsx`, `entityParams.ts`), only the edge usage went away.
  - `web/src/pages/Edit.test.tsx` — `entrypointBindingInputs` carry-over and
    object identity, `withBindingProbe` set/replace/clear/unknown-target,
    `probeFromForm`, the drawer renders no probe input, the edge panel shows the
    route name and a `<textarea>` command.
  - `web/src/pages/Edit.dom.test.tsx` — the drawer keeps every remaining probe
    across reorder and removal, a new row gets none, the edge form saves
    command/interval/timeout into the named binding, keeps every other binding
    probe, removes the probe on an empty command (even with stale invalid
    schedule text) and refuses an interval below 1s.
  - `web/src/pages/Topology.test.tsx`, `web/src/pages/edges.test.tsx` — the five
    edge shapes (probe-less dashed/neutral/unlabelled, failing probe solid amber
    `✕`, probed fallback `✕ 回退`, probe-less fallback `回退`, eligible solid and
    unlabelled), `dead` precedence, the traffic+badge label, `hasProbe` from
    config, and the `selected` flag on re-decoration.
  - `internal/gateway/bindingprobe.go` — the `add` doc comment now says the
    binding appears in selection priority order (probed first), not
    configuration order.
  - `README.md` — the drag-connect sentence matches `drafts.ts` (a dragged
    route is added without a probe and inserted before a trailing probe-less
    binding, not appended); the monitor-mode sentence now describes the shapes
    the console draws; the console table says the probe is edited on the edge.
  - `AGENTS.md` — the health invariant no longer opens with “in configuration
    order” before the priority order.
- Tests/commands and results:
  - RED against the previous implementation (implementation files stashed, new
    tests kept): `cd web && npx vitest run src/pages/Edit.test.tsx
    src/pages/Edit.dom.test.tsx src/pages/Topology.test.tsx
    src/pages/edges.test.tsx` — 4 files failed, 27 tests failed / 79 passed
    (missing helpers, probe inputs still in the drawer, no `hasProbe`, old
    `未通过`/dashed badge rules).
  - GREEN, same command: 4 files, 106 tests passed.
  - Mutation checks that the new tests bite: dropping the probes of the other
    bindings in `withBindingProbe` fails “keeps every other binding probe when
    saving one”; ignoring `existing` in `entrypointBindingInputs` fails the
    drawer round-trip, reorder, removal and new-row tests.
  - `cd web && npm test` — 51 files, 476 tests passed.
  - `cd web && npx tsc --noEmit` — clean.
  - `cd web && npm run build` — built into `internal/admin/dist` (git-ignored,
    not committed).
  - `GOWORK=off go build ./...` — OK. `GOWORK=off go vet ./internal/gateway` —
    OK. `GOWORK=off go test ./internal/gateway -count=1` — ok (11.1s).
- Review findings and fixes (self-review of the unverified draft):
  - The draft was re-read against the brief line by line; the helper split, the
    `EntrypointEdgeForm` extraction, the badge rules and the test set all match,
    so they were kept. Rewritten: the README paragraph (the new drag sentence
    was garbled at the line wrap and the monitor sentence claimed an
    “emphasised” binding the console does not draw), the
    `EntrypointEdgeForm.save` validation (a nested
    `.then/.catch` inside an `if` became an explicit `if (probe)` block), the
    `entrypointBindingInputs` probe map (a `filter/map` over a possibly
    undefined probe became a typed loop) and the `configuredProbes` doc
    comment.
  - Added the missing review-focus test: saving one edge probe leaves another
    binding's probe untouched (the draft only proved a probe-less neighbour is
    untouched).
  - Probe objects are carried by reference on both surfaces, so an untouched
    probe is byte-for-byte what the config held (`toBe` in the test) and a
    probe-less binding never gains a `probe: undefined` key.
  - `未通过` was kept in the locale after verifying it is still rendered by the
    entrypoint list and the detail panel; deleting the key would have shown
    Chinese on the English UI. The brief's Step 4 states that condition.
  - The `entrypoint:<id>->unrouted` draft edge has no `->route:` marker, so the
    panel shows `未连接` + “this edge cannot be edited directly” instead of a
    fake missing-binding warning, and Disconnect stays disabled.
  - The edge form drops stale schedule text when the command is blank, so
    removing a probe cannot be blocked by an invalid interval left in the form.
- Deferred items/limitations:
  - `hasProbe` comes from the console config, so a binding the server no longer
    builds shows as probe-less; the health payload deliberately stays unchanged.
  - Binding-edge probe styling is monitor mode only (`Edit` renders `Topology`
    with `metrics={null}` and no `entrypointHealth`, and `handleSnapshot`
    freezes graph state in edit mode). Passing health through `Edit` would show
    the badges while editing; not done because the design and README frame the
    probe decoration as monitor state.
  - `EntityParams` still prints the probe command read-only in the entrypoint
    detail panel; editing it is only on the edge.
  - No browser visual check: the edge styling is covered by Vitest and
    `renderToStaticMarkup` only.
