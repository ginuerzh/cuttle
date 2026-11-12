# Entrypoint → multiple route bindings, switched by a command probe

Date: 2026-11-12
Status: implemented 2026-11-12; plan at
`docs/superpowers/plans/2026-11-12-entrypoint-multi-route-bindings.md`

## Intent

An entrypoint currently names exactly one route. The operator wants to choose
the route at runtime from external logic that is not upstream failure: provider
quota, time of day, canary/gradual rollout, cost budget, a maintenance flag.
The switch has to happen without editing config and without client changes.

The chosen mechanism is a **command probe on every entrypoint → route binding**:

```text
Entrypoint (model globs)
  -> route bindings, ordered        (first eligible binding wins)
       binding = { route, probe? }
       probe   = `sh -c <command>`, exit 0 = eligible, non-zero = ineligible
  -> Route (unchanged: nodes + strategy + retry policy)
  -> Node -> Provider model
```

Exactly one binding is normally eligible at a time: its command exits 0 while
the others exit non-zero. The command is the external logic. If several are
eligible, binding order breaks the tie. If none is eligible, the first binding
is still used (accepted fallback, see Decisions).

## Goals

- `entrypoint.routes` is an ordered list of bindings; each binding may carry a
  command probe. An entrypoint with no bindings is a draft, exactly like an
  entrypoint without a route today.
- Probe results drive route selection inside one entrypoint, at request
  granularity, with no config write and no reload.
- The switch is visible: per-binding eligible/ineligible state, exit code,
  latency and error are exposed through the admin API and the console.
- Everything else keeps working unchanged: stats attribution, discarded
  attempts, retry policy, sticky node selection inside a route, node `dead`
  markers, topology positions, gateway keys, count_tokens.
- Break the old `entrypoint.route` schema **loudly** with no compatibility
  alias (same policy as the 2026-11-05 routing change).

## Non-goals

- Per-request route selection from request attributes (headers, query, client
  IP). That was approach A and was not chosen; the route must not depend on the
  request.
- An admin-API "active route" pointer or console switch (approach C).
- Weighted or round-robin spreading across bindings. Order is the only rule.
- Health-driven failover **across entrypoints**. Entrypoint declaration order
  stays the only precedence rule and a request never falls through to a later
  entrypoint.
- Probe history in SQLite, probe-driven alerts, probe metrics in graph rollups.
- Node-level `probe` and x's `strategy: lowestlatency` / `maxLatency`. Those
  remain unused by cuttle.

## Anti-goals (what would make this wrong even if it works)

- Route order becoming semantic across entrypoints (an ineligible binding must
  never hand the request to another entrypoint).
- The command probe sharing a failure marker with node `failCodes` (429/5xx)
  counting. The binding marker is probe-only.
- Replacing the x hop group / selector with a cuttle dispatcher.
- One upstream call per probe. The probe must not spend provider quota.

## Constraints

- `github.com/go-gost/x v0.32.0`, no `replace`; **no x change is allowed** for
  this work. Consequences that are accepted and must be documented:
  - `hopGroup.Select` returns the first eligible entry when every entry is
    filtered out, so "all ineligible" falls back to the first binding.
  - `FailFilter` is a no-op when exactly one entry is eligible, so a
    single-binding entrypoint always uses its binding.
  - `FailFilter` per-item overrides read `metadata.Metadata`, which
    `x/hop.HopEntry` does not implement; the filter is configured per group.
- `IsFailed` releases a marked item once `failTimeout` elapses since the last
  mark (`x/selector/filter.go`). A probe that keeps failing re-marks every
  interval, so the group `failTimeout` must be greater than the probe interval
  or eligibility flaps.
- cuttle compiles one x hop per (route × inbound protocol variant), each with
  its own rewriters. A binding therefore has up to four entries (chat,
  responses, anthropic, gemini) plus the fallback variant. Using x's per-entry
  probe would run the same command four times per interval.
- The probe command runs as the cuttle process. The admin API can already
  change provider URLs, so this adds no new privilege boundary; commands that
  embed tokens are secrets anyway and must not be logged.
- Existing invariants in `cuttle/AGENTS.md`: x hop group and selector stay; one
  stats row per client request; entity ids, not names, in stats and API paths.

## Approaches considered

**A. Per-request matcher on each binding.** `routes: [{route, match:
"Header('x-tier') == 'cheap'"}]`, compiled into the existing x routing DSL.
Trades: deterministic per request, no new goroutines, but the decision moves to
whoever sends the request (or a front process) and cannot express quota or time
without client cooperation. Not chosen.

**B. Command probe per binding (chosen).** The external logic is a script; exit
0 means "use this route". Switch latency is the probe interval; the decision is
global per entrypoint and needs no client or config change. Uses x's entry
markers and `FailFilter`; cuttle owns the runner so the command runs once per
binding instead of once per protocol variant, and so the result is observable.

**C. cuttle-owned active-route pointer (admin API / console).** Most direct for
an operator, but it needs either a custom selector or a dispatcher layer in
front of the hop group, against the "use the x machinery" invariant, and it
makes the console a control plane component. Not chosen.

## Design

### 1. Config schema

```yaml
entrypoints:
  - id: e_7h2kq9v3xa
    name: claude
    match: [claude-*]
    routes:
      - route: r_cheap
        probe:
          command: /usr/local/bin/cuttle-quota cheap
          interval: 30s
          timeout: 5s
      - route: r_premium          # no probe: always eligible
```

- `Entrypoint.Route string` becomes `Entrypoint.Routes []RouteBinding`.
- `RouteBinding{Route string; Probe *Probe}`; `Probe{Command string; Interval
  Duration; Timeout Duration}`. Bindings are inline config, not entities: no
  id, not addressable through the API.
- `routes: []` or absent = draft (claims its model globs, serves nothing,
  compiles to the shared zero-node hop). Binding with `route: ""` is a
  validation error, not a draft.
- Duplicate route ids inside one entrypoint are rejected. The same route in two
  entrypoints is fine and yields two independent bindings and two probes.
- A binding without `probe` is always eligible. It is how an explicit static
  default is expressed ("if no script claims the traffic, use this"). Because
  such a binding shadows every binding after it, **at most one probe-less
  binding is allowed and it must be last**; validation rejects anything else.
  A list of only probed bindings is fine, and then the all-ineligible fallback
  in §3 picks the first one.
- Defaults: `interval` 30s, `timeout` 5s. Validation: `command` must be
  non-blank, `interval >= 1s`, `timeout >= 1s`, `timeout < interval`. Errors
  name the entrypoint and the binding index.
- `probe.command` is **not** run through `ExpandEnv` at load time: `$` belongs
  to the shell and env expansion at load time would corrupt commands and break
  the "admin writes must not expand a literal `$`" rule. The command gets the
  cuttle process environment plus the variables in §4 at run time.
- Migration: `MigrateIdentities` resolves each binding's route reference
  (id or unique name → id) exactly as it does for the single route today.
- The old `route:` key is rejected by `yaml.KnownFields(true)`. This is a
  breaking change: the operator must rewrite the file before SIGHUP, and
  README gets an upgrade note. `example.yaml` is converted in the same change.

### 2. Build: one inner hop group per entrypoint and variant

`buildHopGroup` today emits one entry per (entrypoint × inbound protocol group)
plus a fallback entry, each entry wrapping a single route plan's hop, and the
outer group selector is a bare FIFO with no filters.

New shape:

```text
outer hop group        (unchanged matcher; bare FIFO, no filters)
  entry: entrypoint e, variant chat        matcher = model globs ∧ chat path
    inner hop group      FIFO + FailFilter(1, derived timeout)
      entry: binding 0 -> hop(route A, chat)
      entry: binding 1 -> hop(route B, chat)
  entry: entrypoint e, fallback            matcher = model globs
    inner hop group
      ...
```

- The inner group is `xhop.NewHopEntry(bindingHop, nil, nil)` per binding with
  `xs.NewSelector(xs.FIFOStrategy[hop.Hop](), xs.FailFilter[hop.Hop](1,
  groupFailTimeout))`. Inner entries carry no matcher: the variant was already
  chosen by the outer matcher.
- The outer group keeps `xs.NewSelector(xs.FIFOStrategy[hop.Hop]())` with **no
  filters**. Adding `FailFilter` there would let an ineligible binding fall
  through to a later entrypoint, which the anti-goals forbid.
- Inner groups are built uniformly, even for a single binding. One code path,
  and the entry that the runner marks always exists.
- `groupFailTimeout = max(interval over bindings) * 2`, floored at 10s.
  Rationale in Constraints; it is derived, not configurable.
- `routePlans` stays keyed by route id and shared by every entrypoint that
  names the route, so rewriters are still registered once per route × variant ×
  node.
- Draft: an entrypoint with no bindings uses `buildDraftPlan` exactly as today
  (`draftPlanKey` now means "no bindings").
- `buildCountTokensPolicy`: an entrypoint estimates locally when **any** of its
  bindings' routes needs an estimate. The policy is keyed by model globs, not
  by the route that ends up serving; a conservative rule is the only sound one
  without evaluating the probe on the count_tokens path.

### 3. Selection semantics

Per request, in order:

1. Outer group picks the first entry whose matcher matches, in declaration
   order (model glob + protocol path for variant entries, model glob only for
   the fallback entry). No filters, so no cross-entrypoint fallthrough.
2. Inner group builds its candidate pool = every binding (all match), applies
   `FailFilter` when there is more than one candidate, then FIFO takes the
   first survivor.
3. If every binding is ineligible, x falls back to the first candidate
   (`hopGroup.Select` last resort). Accepted: a broken script or a probe
   timeout degrades to "the default route", never to a 502, and never to
   another entrypoint. A probe-less binding is always eligible, so this path
   only applies to entrypoints whose bindings all carry probes.
4. With one candidate, `FailFilter` is a no-op by design; the only binding is
   used regardless of its probe. Documented, not worked around.

There is no hysteresis, no backoff and no session pinning across routes. A
command that flaps will flap traffic. Within the active route, `sticky` node
selection is unchanged, so a route switch cold-starts prompt cache affinity —
expected, and the reason anchors are per route.

### 4. Probe runner (cuttle-owned)

New `internal/gateway/bindingprobe.go`:

```go
type probeSet struct { ... }        // built by buildHopGroup, started by apply
func (s *probeSet) Start(ctx)       // one goroutine per binding
func (s *probeSet) Stop()
func (s *probeSet) Snapshot() []health.Entrypoint
```

- One goroutine per (entrypoint, binding), independent of protocol variants.
  It holds the `*xhop.HopEntry` of every variant of that binding and the
  registry that explains the command's outcome.
- Loop: run immediately, then every `interval`; stop on context cancellation
  (reload/shutdown).
- Each run: `exec.CommandContext` with `timeout`, `sh -c <command>` (`cmd /C`
  on Windows, mirroring x's helper). Environment: `os.Environ()` plus
  `CUTTLE_ENTRYPOINT_ID`, `CUTTLE_ENTRYPOINT_NAME`, `CUTTLE_ROUTE_ID`,
  `CUTTLE_ROUTE_NAME`, `CUTTLE_BINDING_INDEX`. One shared script can serve
  every binding.
- Exit 0 → `Reset()` on every variant entry of that binding. Non-zero, timeout
  or exec failure → `Mark()` on all of them. The group `failTimeout` derived in
  §2 keeps them ineligible until the next successful probe.
- Result recorded in memory per binding: `at`, `ok`, `exitCode`, `latencyMs`,
  `error` (stderr head, first 1 KiB, plus the exec error). Logs: debug on
  success, info on transition only (eligible ↔ ineligible), never the command
  or its full output.
- Lifecycle: `buildHopGroup` returns the unstarted `*probeSet`; `apply` starts
  it after the hop swap and stops the previous set immediately, so an old
  runner never marks a hop that no new request uses; only the old hop close and
  the rewriter unregister stay in the 30s grace window (`gateway.go`). A build
  error never starts a probe set.
- Binding eligibility is written **only** by this runner. Node 429/5xx marking
  (`failCodes`) and its marker stay untouched, so `dead` badges keep meaning
  what they mean today.

### 5. Health and console

- New `GET /api/entrypoints/health` (authenticated like the rest):

  ```json
  [{"entrypointId":"e_…","entrypointName":"claude",
    "active":{"routeId":"r_…","fallback":true},
    "bindings":[
    {"index":0,"routeId":"r_…","routeName":"cheap","eligible":false,
     "last":{"at":"…","ok":false,"exitCode":1,"latencyMs":12,"error":"quota exhausted"}},
    {"index":1,"routeId":"r_…","routeName":"premium","eligible":true}]}]
  ```

  `eligible` is the raw probe verdict; `active` is the binding that will serve
  the next request, computed with the same rule the data plane uses (first
  eligible binding, otherwise binding 0) and flagged `fallback: true` in the
  second case. The two differ in exactly the two accepted states of §3
  (all-ineligible fallback, single binding). Compute `active` in `Snapshot()`
  from the entry markers with `xs.IsFailed` and the derived `failTimeout`, so
  there is one verdict source. Bindings that have not probed yet report
  `eligible: true` and no `last`. The command string is not echoed here (the
  config API already returns it).
- `/api/events` snapshots add `entrypointHealth` to the snapshot that is
  already pushed on every tick (no change to the push cadence or the
  percentile recomputation rule).
- Console:
  - Entrypoints list card subtitle: `claude-* → cheap (未通过) / premium (可用)`,
    with the active binding marked (`●`), and a `回退` tag when the active
    binding is the all-ineligible fallback.
  - Entrypoint form: ordered binding list with up/down, route select, and probe
    command/interval/timeout fields. Empty list shows the draft hint. A
    probe-less binding is offered only in the last position (the same rule the
    API enforces).
  - Topology: one `entrypoint:<id> -> route:<id>` edge per binding (structure
    graph). In monitor mode the active binding edge is emphasised, an
    ineligible non-active binding is dashed amber labelled `未通过`, and a
    fallback-active binding is dashed amber labelled `回退`. Node `dead`
    styling is untouched.
  - Entrypoint detail panel: bindings, active/fallback, last probe time, exit
    code, latency and error.
  - Data source: monitor mode reads `entrypointHealth` from the `/api/events`
    snapshot; the Entrypoints page fetches `GET /api/entrypoints/health` on
    load and after a save, the same way it loads config.
- Topology edit mode: dragging `entrypoint → route` **appends** a binding when
  the route is not already bound (a duplicate is a no-op); deleting the
  `entrypoint:<id>->route:<id>` edge removes only that binding; removing the
  last binding turns the entrypoint back into a draft. The edge id stays
  `entrypoint:<id>->route:<id>` (no binding index), so the edge form resolves
  the binding by route id.
- `/api/routes/health` and the node marker model are unchanged.

### 6. Admin API and config pipeline

- `GET /api/entrypoints` and `PUT /api/entrypoints` carry `routes: [{route,
  probe}]`. `adoptEntityID` is unchanged: bindings have no ids.
- `PUT /api/config` round-trips the new fields; the adopt-or-mint rule for
  entrypoints is unchanged.
- `DELETE /api/routes/{id}` keeps failing while any binding references the
  route; the message lists the referencing entrypoints by label and id.
- `expandEnv` expands `binding.Route` only; not `probe.command`. `applyDefaults`
  trims and defaults interval/timeout. `Validate` implements §1 rules and the
  existing route-reference check per binding.
- `migrate.go` resolves binding references; a hand-written config that omits
  ids migrates exactly as today.

### 7. Stats, capture, retries

No change. The serving route is the one recorded on the request row, so the
Sessions list, entrypoint attribution, graph edges, attempts and captures follow
the active route automatically. The topology structure graph now draws one edge
per binding, which is the only visible stats-adjacent change.

### 8. Files

| File | Change |
| --- | --- |
| `internal/config/config.go` | `Entrypoint.Routes`, `RouteBinding`, `Probe`; expand/create/trim/validate; `Route` helper unchanged |
| `internal/config/migrate.go` | resolve route references per binding |
| `internal/config/config_test.go`, `migrate_test.go` | new schema, defaults, rejections, old `route:` rejected |
| `internal/gateway/build.go` | inner group per entrypoint × variant; probe set construction; count_tokens rule |
| `internal/gateway/bindingprobe.go` | new runner + registry |
| `internal/gateway/gateway.go` | store/start/stop probe set; `EntrypointHealth()`; grace close |
| `internal/gateway/health` (`internal/health/health.go`) | `Entrypoint` / `Binding` payload types |
| `internal/admin/server.go`, `handlers.go`, `handlers_events.go` | `/api/entrypoints/health`, delete-route check, events snapshot |
| `internal/admin/*_test.go` | API + validation + health + events |
| `web/src/api/types.ts` | `Entrypoint.routes`, `Probe`, `EntrypointHealth` |
| `web/src/graph/build.ts` | one edge per binding; `unconnected` on empty list |
| `web/src/pages/Entrypoints.tsx`, `Edit.tsx`, `entityParams.ts`, `drafts.ts`, `Topology.tsx`, `labels.ts` | binding editor, badges, edges, panel |
| `web/src/locales/en-US.ts` | new strings (source strings stay Chinese) |
| `example.yaml`, `README.md`, `AGENTS.md` | new schema, upgrade note, routing invariants |
| this document | design record |

## Testing

- Config: defaults, `timeout < interval`, blank command, duplicate binding,
  draft vs empty binding route, `route:` key rejected, migration of names to
  ids, `probe.command` not env-expanded, validation messages naming the entry.
- Build: binding count × variant entries; inner group only when >1 binding;
  outer group still has no filters; shared route plan and rewriters reused
  across entrypoints; draft entrypoint unchanged.
- Probe runner: fixture command in `t.TempDir` that reads a file to flip
  exit 0/1; assert every variant entry's marker follows; env variables are
  passed; timeout marks ineligible; `Stop` cancels a slow command; result
  registry carries exit code, latency and stderr tail.
- Selection: two bindings, first ineligible → second serves; both ineligible →
  first serves (fallback); first becomes eligible → first serves again; single
  binding ignores probe; entrypoint-level no-fallthrough (a dead binding of
  entrypoint 1 does not serve through entrypoint 2's catch-all).
- Admin: PUT/GET with bindings, delete-route rejection, health payload
  (`eligible` vs `active`/`fallback` in all three states), events snapshot
  includes `entrypointHealth`, 401 without a session.
- Config edge cases: probe-less binding not last is rejected; duplicate
  route ids rejected; single-binding entrypoint still reports `active` and
  ignores its probe for selection.
- Topology edit mode: connect appends, duplicate connect is a no-op, edge
  delete removes one binding, deleting the last one makes a draft.
- Web: graph edges per binding, entrypoint card and form rendering, i18n keys
  present in both locales, existing fixtures updated to `routes:`.
- Manual: two routes over two `httptest`-style upstreams, a script that flips a
  file, confirm the request's recorded route changes within one interval and
  that a failed probe does not mark nodes `dead`.

## Decisions (call list for the owner)

1. Breaking schema: `route` → `routes`, no alias, loud rejection, README
   upgrade note. Accepted cost: the live config must be edited before SIGHUP.
2. FIFO among eligible bindings; no weights, no round-robin.
3. cuttle owns the probe runner (one command per binding) instead of x's
   per-entry probe (one command per protocol variant), for cost and
   observability.
4. All-ineligible falls back to the first binding (x native behavior, no x
   change). The default route therefore belongs first in the list.
5. Commands are not env-expanded at load; `sh -c` plus injected `CUTTLE_*`
   variables; stderr tail is recorded for the panel.
6. Always wrap bindings in an inner group, including the single-binding case.
7. Binding health is memory-only, separate from `/api/routes/health`, and shows
   up in `/api/events` and the console.
8. `count_tokens` estimates when any binding of the entrypoint needs it.
9. Bindings are inline config with no entity ids.
10. Health reports the probe verdict per binding **and** the effective
    `active` binding (with a `fallback` flag), so the console can show "which
    route serves now" rather than only "which probes passed".
11. Topology edit mode keeps working as list edits: connect appends a binding,
    deleting the edge removes that one binding, the last removal is a draft.
12. A probe-less binding is allowed only once and only last; validation
    rejects a configuration where it shadows later bindings.
13. `/api/events` cadence is unchanged: `entrypointHealth` rides the existing
    per-tick snapshot.

## Implementation notes (deviations from this design)

Implemented on branch `feat/entrypoint-bindings` on 2026-11-12 across tasks 1-6,
with no x change and no `replace`: `github.com/go-gost/x` stays `v0.32.0`. The
design was followed except for these points:

1. **Old probes stop immediately after the swap.** `apply` stops the previous
   `probeSet` as soon as the new hop is published and starts the new set there;
   only the old hop close and the rewriter unregister stay in the 30s grace
   window. Task 2's Step 4 prose originally put the stop inside the grace block;
   an early plan commit corrected it before implementation.
2. **`buildBindingsHop` takes an `entrypointID`.** The planned signature had no
   way to name the entrypoint in the inner group's log name
   (`entrypoint/<id>/<variant>`) or in its error messages.
3. **Probe stderr is the head, not the tail.** `cappedBuffer` keeps the first
   1 KiB so a chatty command cannot grow the process; the payload shape is
   unchanged. §4 and the code comments say head.
4. **Selection health comes from one marker read per binding.** `Snapshot`
   reads `xs.IsFailed` once per binding and derives both `eligible` and the
   `active` candidate from that read, so a probe landing mid-snapshot cannot
   report the active binding as ineligible.
5. **e2e fixtures use `test ! -f <flag>`.** Task 2's Step 4b sketch said
   `test -f`, which is inverted relative to its own flow (creating the flag must
   make the first binding ineligible); the tests and the manual smoke use
   `test ! -f`.
6. **The probe environment is exactly the five documented variables**, appended
   to `os.Environ()`. A probe-less binding appears in the health payload but
   starts no goroutine, and a draft entrypoint is absent from the payload.

## Deferred

- Per-request header/attribute routing (approach A) as a complementary
  mechanism.
- Admin/console active-route pointer (approach C).
- Per-binding weights or a "spread" mode.
- Probe history, alerts and rollups.
- An x option for hard failure when every binding is ineligible.
- Node-level probe, `maxLatency` and `lowestlatency` strategy.
