# Subagent observability and subagent route bindings

Date: 2026-11-19
Status: **Phase 1** ready for a plan. **Phase 2** designed, not scheduled.
Phasing decided 2026-11-19 (§1); see Decisions 1-14.

> **Amends the 2026-11-12 entrypoint multi-route design.** That document listed
> "per-request route selection from request attributes (headers, query, client
> IP)" as a non-goal (approach A, not chosen). Phase 2 deliberately opens one
> narrow slice of it: a binding may declare that it serves subagent traffic,
> decided from a fixed, cuttle-owned table of subagent marker headers. Arbitrary
> request attributes still never select a route.

## Intent

Antigravity's Orchestrator made the industry pattern explicit: a long agent task
needs a frontier model for planning and review and a cheap fast model for
execution, and the split has to happen inside one session (see
`.memory/notes/cuttle-orchestrator-borrow-eval.md`). A transparent gateway cannot
own that control loop — it does not see tool execution or phase boundaries — but
it can see one thing the harness tells it on the wire: **whether a request comes
from a subagent**.

Two questions follow, and they are deliberately separated into two phases:

1. **How much subagent traffic is there?** (Phase 1, observability.) Today the
   answer cannot be recovered: the three marker headers are neither stored on
   the request row nor on the capture allowlist, and `user_agent` is identical
   for a subagent and its lead because the same process sends both. Nothing in
   existing data stands in for the marker.
2. **Should that traffic take a cheaper route?** (Phase 2, dispatch.) Only
   worth the machinery if the measured share is significant, which is why
   Phase 1 ships first and Phase 2 is designed but unscheduled.

Phase 1 additionally supplies the diagnosis surface Phase 2 will need: per
request, which class it was and which client marker said so.

## Goals

**Phase 1**

- Every request row records `subagent` (0/1) and `subagent_source`
  (`claude_code` / `opencode` / `codex` / empty), derived at record time from the
  client-sent headers. No raw header value is stored on the row.
- `GET /api/requests?subagent=1|0` filters on it; the request detail shows both
  fields; the list does not.
- The three marker headers join the capture header allowlist, so an on-demand
  capture shows the marker values that produced the classification.
- One cuttle-owned table in a new leaf package `internal/agent` defines the
  markers and the classifier. This is the only place that knowledge lives, and
  Phase 2's dispatch rules read the same table.
- Console: a Sessions filter (全部 / 是 / 否) and a detail row
  `标记来源 · <kind>`.

**Phase 2**

- A route binding may carry `when: subagent`; absent means any request. Unknown
  values are rejected at load, naming the entrypoint and binding index.
- Selection happens inside the existing x hop group (`routing.Matcher` on
  `xhop.HopEntry`), per request, with no new selector, dispatcher, goroutine or
  reload. A conditioned binding is preferred for subagent traffic and the
  unconditional binding serves everything else and absorbs failures.
- `GET /api/entrypoints/health` rows carry `when`; the console lets the operator
  pick the class on both binding editors.
- Docs: README gains the schema, the client/version matrix and the
  measure-then-enable workflow; `AGENTS.md` amends its "only the body model
  matches" invariant.

## Non-goals

- Inferring phase from the body (turn position, tool-result density, error
  strings) or escalating on friction. Later tiers of the Orchestrator
  evaluation, deferred wholesale.
- Matching a specific agent (`review` vs `explore` vs `compact`). Class only;
  the marker value is recorded as a source label, never interpreted.
- Routing on any other request attribute. Headers cannot select a route except
  through the fixed marker table; paths still only pick the upstream wire API.
- Per-class rollups, charts, alerts or graph edges (Phase 1 stores two columns
  and nothing else).
- A "configured but not effective" health hint in Phase 2. The Sessions filter
  answers it in one click; the automated hint is deferred.
- Changing entrypoint specificity, sticky identity, retry policy, count_tokens,
  capture semantics or the topology structure rules.
- Stripping or normalizing the marker headers toward upstreams. They pass
  through untouched, as today.

## Anti-goals (what would make this wrong even if it works)

- Any behavior change for a config that does not use `when` (Phase 2) or any
  schema change that is not additive (Phase 1).
- Persisting raw session or agent header values on the **request row**. Capture
  is the deliberate, on-demand exception (as `x-opencode-session` already is).
- A `when: subagent` binding serving lead traffic. (The reverse — lead traffic
  falling back into an unconditional binding — is the intended degradation.
  There is no lead class to misclassify after Decision 2.)
- 502 for ordinary lead traffic because every binding was conditioned on
  `subagent` (must be a validation error).
- Reintroducing an arbitrary matcher expression DSL in config (removed on
  2026-11-05 by design).
- A cuttle-owned dispatcher in front of the x hop group.
- Treating the marker headers as authenticated. They are client-controlled
  (Decision 4): any holder of a gateway key can claim `subagent` and reach the
  cheap route. Accepted for a self-hosted gateway; documented in README.

## Constraints

- `github.com/go-gost/x v0.32.0`, no `replace`, **no x change**. What the pinned
  version gives Phase 2:
  - `xhop.NewHopEntry(hop, matcher, probe)` already takes a `routing.Matcher`;
    `buildBindingsHop` passes `nil` today (`internal/gateway/build.go:299`).
  - `hopGroup.Select` stage 1 filters entries by matcher per request from
    `routing.Request`, which carries `Header http.Header`; the forwarder passes
    the client headers (`x/internal/util/forwarder/sniffer_http.go:422-431`).
  - `xrouting.NewMatcher` supports `Header(name[, value])`, `HeaderRegexp` and
    `&&`/`||`; one argument is a presence check. Presence tests the map key, so
    an empty-valued marker still matches.
  - `hopGroup.Select` returns `nil` when nothing is eligible, which the
    forwarder turns into a 502. Validation must make that unreachable for
    ordinary traffic.
  - `FailFilter` is a no-op when exactly one entry is eligible (documented in
    the 2026-11-12 design and accepted again here).
- `internal/gateway` already imports `internal/stats`, so the shared classifier
  cannot live in `gateway`; it goes in a new leaf package `internal/agent`
  (Decision 9).
- Existing capture facts: the allowlist is 8 exact headers plus two prefixes
  (`internal/stats/headers.go:10-24`), `x-opencode-session` is already there, and
  captured request headers are persisted in `request_captures.request_headers`.
  Capture defaults to `off`, is bounded by rules (route + client model only) and
  by 24h retention.
- Client wire facts this design relies on, with their verification status:
  - **Claude Code**: `x-claude-code-agent-id` on subagent requests. Already in
    the sticky key (`internal/gateway/session.go:25`), which separates a
    subagent's stick from its lead's in production — production-verified.
  - **OpenCode**: the V2 runner sends `x-parent-session-id` on child sessions
    (subagents) since #43188 (2026-08-18); releases v1.18.16–v1.18.18 dropped
    all session headers (#42694), and forks such as Kilo predate the restore.
    Child sessions carry their own `X-Session-Id`/`x-session-affinity`, so the
    parent header is a marker only, never an identity. Verified end-to-end
    locally in Phase 1 (opencode v2.3.0 is installed in the build environment).
  - **Codex**: `x-openai-subagent` with `review`, `compact`,
    `memory_consolidation`, `collab_spawn` or a custom label
    (`codex-rs/core/src/client.rs`, `build_subagent_headers`); session identity
    is `session-id`/`thread-id`, already listed in `sessionHeaders`.
    Source-confirmed only: no Codex binary exists in the build environment and
    whether the header is sent to third-party base URLs is unverified.
- **Verification policy (Decision 13 of the grilling round, 2026-11-19).** All
  three markers ship. OpenCode is verified end-to-end during Phase 1. Codex
  carries a "source-verified, wire-unverified" note in the implementation record
  and a manual smoke item the owner runs **before relying on `subagent=codex`
  or enabling `when: subagent` for Codex traffic** — a use-condition, not a
  merge gate. A client whose marker never arrives simply never matches.

## Approaches considered

**A. Generic per-binding matcher string** (`match: 'Header("x-parent-session-id")'`).
Rejected: cuttle removed arbitrary route matching on 2026-11-05 and
`yaml.KnownFields(true)` now rejects it on purpose; it would resurrect the DSL
for one use case and push three clients' header knowledge onto every operator.

**B. Fixed `when` enum (chosen for Phase 2).** One value, `subagent`, compiled
from a cuttle-owned table. Strict schema, self-documenting, one place to
maintain, and the operator never types a header name. Costs flexibility: a new
client or a per-agent distinction needs a code change — acceptable for markers
that are stable, documented wire contracts.

**C. Measure first, then decide (chosen sequencing).** Store the class, look at
real traffic, then build dispatch. Chosen after the grill: the maintenance cost
of `when` (three-client table, class tier, health/admin/web) is only justified
by a measured share, and the share cannot be recovered from existing data.

**D. Two entrypoints per client model (one per class), selected by
specificity.** Rejected: entrypoint match is the body `model` glob only; two
entries with the same glob would be decided by declaration order, not by
headers, so this cannot express the split.

**E. Rewriter-level model swap per request (one node, two target models).**
Rejected: it breaks the "a node binds one provider model" invariant and with it
target-model attribution, catalog limits and per-node stats and health.

## Design

### 1. Phasing

| | Phase 1 — observability | Phase 2 — dispatch |
| --- | --- | --- |
| Delivers | `subagent` + `subagent_source` columns, `?subagent=` filter, detail fields, capture allowlist, `internal/agent` table + classifier, Sessions filter | `when: subagent` on bindings, matcher compilation, class tier in the binding order, health `when`, console selects, README/AGENTS updates |
| Gate to start | none | measured share is significant (judgement, no preset threshold — Decision 13) |
| Behavior change | none for any existing config | only for configs that set `when` |
| Depends on | nothing | Phase 1's table and classifier |

Phase 1 ships first because it is the only way to learn whether Phase 2 is worth
it (Decision 1). Phase 2's design is kept here, unscheduled, so the axis,
rationale and table stay in one place (Decision 10).

### 2. Marker table and classifier (Phase 1)

New leaf package `internal/agent`:

```go
type marker struct {
    source string // stored as subagent_source
    header string // presence marks a subagent request
}

// Keep in sync with the clients cuttle serves. See README for versions.
var markers = []marker{
    {"claude_code", "x-claude-code-agent-id"},
    {"opencode", "x-parent-session-id"},
    {"codex", "x-openai-subagent"},
}

// Classify reports whether the request carries a subagent marker and which
// client's it is; the first match in table order wins.
func Classify(h http.Header) (source string, subagent bool)
```

- `Classify` tests **map-key presence** (`_, ok := h[canonical]`), never
  `Get(...) != ""`, because x's `Header(name)` matcher does the same: an
  empty-valued marker must classify identically to a present one, or the
  recorded class and Phase 2's dispatch would disagree.
- `internal/gateway` and `internal/stats` both import this package; it imports
  only the standard library.
- Phase 1 adds no rule builders and no `xrouting` dependency. The matcher-rule
  generator lands in Phase 2, together with the only code that consumes it
  (Decision 11); until then there is nothing to drift.
- `sessionHeaders` in `session.go` is untouched: identity and class are separate
  concepts, and subagent child sessions already produce distinct sticky keys
  (OpenCode and Codex via their own session ids, Claude Code via the agent id
  appended today).

### 3. Observability (Phase 1)

- `requests` gains two additive columns via `ensureColumns`, like `retries` and
  `gateway_key`:
  - `subagent INTEGER NOT NULL DEFAULT 0`
  - `subagent_source TEXT NOT NULL DEFAULT ''`
  Existing rows read `0` / `''`; no backfill, no destructive migration.
- Classification runs at record time from the client headers the recorder
  already holds (`clientRequestHeaders`, `internal/stats/requestcontext.go:52`),
  through `agent.Classify`. Raw header values never reach the database or the
  log.
- Deliberately **not** through `stats.Facts`: `Facts` is populated by the
  rewriter, which only runs after a node was chosen. Rows that fail before a
  node — a draft-entrypoint 502, an all-nodes-skipped failure — would then
  silently read `subagent=0`, which is exactly the traffic an operator may be
  measuring.
- `stats.Filter` gains `Subagent *bool`, mapped from `GET /api/requests?subagent=1`
  and `?subagent=0` (boolean only; a source filter is deferred, Decision 14).
  The request detail carries both fields; the list does not.
- Capture: the three marker headers join `headerExact`
  (`internal/stats/headers.go`), so `request_captures.request_headers` shows the
  marker values for captured requests. This is deliberate (Decision 9): capture
  is on-demand, rule-bounded and 24h-retained, and it is the only direct way to
  answer "why was this request classified this way". The "no raw values"
  discipline therefore applies to the request row, not to capture.
- Rollups, key rollups, attempts, the graph and `/api/stats/series` are
  unchanged (Decision 8).

### 4. Config schema (Phase 2)

```yaml
entrypoints:
  - id: e_7h2kq9v3xa
    name: claude
    match: [claude-*]
    routes:
      - route: r_worker          # subagents: cheap and fast
        when: subagent
        probe:
          command: /usr/local/bin/cuttle-quota cheap
          interval: 30s
      - route: r_lead            # no when: everything else, and the fallback
```

- `RouteBinding` gains `When string` (`yaml:"when,omitempty"`). The only allowed
  value is `"subagent"`; `""` (absent) means any request. Anything else is a
  validation error naming the entrypoint and binding index.
- There is no `lead` value (Decision 2). A conditioned binding ordered before
  the unconditional one produces exactly the same outcome as an explicit
  `lead` binding would, without a negation rule, without a lead class, and
  without the "a false-positive marker silently downgrades the main session"
  failure mode. The unconditional binding is both the lead path and the fallback.
- **Servability validation.** Subagent traffic must never be left with zero
  eligible bindings (x returns nil and the forwarder answers 502). Validation
  requires at least one binding without `when`. Draft entrypoints (no bindings
  at all) are exempt: they already claim their models and answer 5xx by design.
  Lead traffic is served by the unconditional binding by construction, so no
  second rule is needed.
- Duplicate `when: subagent` bindings inside one entrypoint are legal with the
  same semantics as duplicate probe-less bindings: only the first can serve and
  the build logs one warning for each further one.
- Bindings remain inline config with no ids; `expandEnv` still touches only the
  route reference; the admin API round-trips `when`.

### 5. Build and selection (Phase 2)

`buildBindingsHop` compiles each binding's matcher from `when` and passes it to
`xhop.NewHopEntry(h, matcher, nil)`. The rule, generated from the same table:

```text
Header("x-claude-code-agent-id") || Header("x-parent-session-id") || Header("x-openai-subagent")
```

`bindingOrder` gains a class tier as its primary sort key:

```text
1. bindings with when != ""   (config order, probed before probe-less)
2. bindings with when == ""   (config order, probed before probe-less)
```

Selection stays entirely inside x: stage 1 matcher filter (class), stage 2
`FailFilter` (probe marker) + FIFO over the survivors. Consequences, each
intentional:

- Subagent traffic sees the conditioned and the unconditional bindings, with the
  conditioned one first, so it wins whenever it is eligible. Everything else
  sees only the unconditional bindings.
- The class tier sits **above** the probe tier (Decision 3): a probe-less
  `when: subagent` binding outranks a probed unconditional one for subagent
  traffic. A conditioned binding is a separate policy lane; an operator who
  wants its availability managed puts a probe on it.
- **Degradation is probe-driven only.** A conditioned binding whose probe is
  ineligible is filtered out and the unconditional binding serves. A binding
  whose *route* builds to zero nodes (provider disabled, every node skipped) is
  not filtered: its hop returns nil and the request 502s, exactly as a probed
  binding behaves today. This is accepted x semantics, not a regression; the
  README states it plainly (Decision 5).
- Only-one-eligible short-circuits `FailFilter` (x quirk): an entrypoint whose
  only suitable binding is probe-failed still uses it.
- Unconditional-only configs compile to `nil` matchers: stage 1 admits
  everything and today's order is preserved.

### 6. Health, admin API and config pipeline (Phase 2)

- `health.Binding` gains `When string` (`json:"when,omitempty"`).
- `bindings[]` stays **one flat list in the data-plane priority order**, which
  now carries the class tier first: conditioned bindings (probed before
  probe-less, config order), then unconditional ones (probed before
  probe-less, config order). `index` remains the configuration index, so a
  `subagent` row can precede the active one.
- `probePriorityOrder` in `bindingprobe.go` mirrors `bindingOrder` by
  definition; the class tier is added to both in the same change, with a test
  asserting they agree.
- `active` keeps its single meaning — the binding that will serve the next
  request **without a subagent marker** — which after Decision 2 is simply the
  first eligible unconditional binding, computed with the same marker read and
  order the data plane uses. `fallback` keeps its current meaning over that same
  subset. Both are documented on the type.
- No class-scoped `active`: the operator reads which binding serves subagent
  traffic from the rows (first `eligible` row whose `when` is `subagent`, else
  the first eligible unconditional one). Deferred until someone needs a second
  server verdict.
- `GET /api/entrypoints`, `PUT /api/entrypoints`, `PUT /api/config` and the
  `entrypointHealth` part of the `/api/events` snapshot carry `when`. Cadence
  unchanged.
- Validation lives in `config.Validate` next to the existing binding rules, all
  messages naming the entrypoint and binding index.

### 7. Web console

**Phase 1**: Sessions page gains a `子代理` filter (全部 / 是 / 否) and the
request detail shows `标记来源 · <kind>` when the request carries a marker.

**Phase 2**: the entrypoint form and the topology `entrypoint → route` edge
panel gain a class select (不限 / 子代理); monitor mode labels a conditioned
binding. Source strings stay Chinese; both locales get every new string.

### 8. Files

| File | Phase | Change |
| --- | --- | --- |
| `internal/agent/agent.go` | 1 | marker table + `Classify` (new leaf package) |
| `internal/agent/agent_test.go` | 1 | table-driven classification, presence semantics |
| `internal/stats/store.go` | 1 | `ensureColumns`, `Filter.Subagent`, row scan/insert, detail select |
| `internal/stats/recorder.go` | 1 | classify and write both columns |
| `internal/stats/headers.go` | 1 | three marker headers on the capture allowlist |
| `internal/admin/handlers_stats.go`, `labels.go` | 1 | `?subagent=` parameter, detail fields |
| `internal/stats/*_test.go`, `internal/admin/*_test.go` | 1 | columns, filter, allowlist, detail |
| `web/src/api/types.ts`, `pages/Sessions.tsx`, `locales/en-US.ts` | 1 | filter, detail row, strings |
| `internal/gateway/build.go` | 2 | per-binding matcher from `when`; class tier in `bindingOrder` |
| `internal/gateway/bindingprobe.go` | 2 | class tier in `probePriorityOrder` (mirrors `bindingOrder`) |
| `internal/agent/agent.go` | 2 | matcher-rule generator from the same table |
| `internal/config/config.go`, `config_test.go` | 2 | `RouteBinding.When`, validation, servability |
| `internal/gateway/health` | 2 | `Binding.When`; `active` over unconditional bindings |
| `internal/gateway/build_test.go` | 2 | matcher wiring, feed order, nil-matcher no-op, order agreement |
| `internal/admin/handlers.go`, `server.go`, tests | 2 | `when` in payloads, health rows |
| `web/src/pages/Entrypoints.tsx`, `Topology.tsx`, `types.ts`, `locales` | 2 | class selects, labels |
| `example.yaml`, `README.md` | 1, 2 | Phase 1 columns and capture note; Phase 2 schema, matrix, workflow |
| `AGENTS.md` | 2 | amend the "only the body model matches" invariant |
| this document | 1 | design record |

### 9. Documentation changes

- README (Phase 1): what the two columns mean, that they are measured and not
  routed on, the client/version matrix, and the measurement workflow (filter
  Sessions by `subagent=1`, compare request and token shares).
- README (Phase 2): the `when` field, the class-tier semantics, the
  probe-only degradation rule, the marker-trust caveat, and the
  measure-then-enable workflow.
- `AGENTS.md` (Phase 2) routing invariants amended to:
  - entrypoint precedence and binding choice are separate: the body `model` glob
    still decides entrypoint precedence and never selects a binding;
  - binding choice now consults the subagent marker table (fixed, cuttle-owned)
    in addition to probe eligibility;
  - paths still only select the upstream wire API.

## Testing

**Phase 1**

- `agent.Classify`: table-driven, one case per client, precedence order, an
  empty-valued marker counts as present, unrelated headers absent, and the
  returned source is a fixed label (never the header value).
- Stats: both columns written for all three sources and for a marker-less
  request; `subagent=1|0` filter returns the right rows; detail carries both
  fields, list omits them; pre-existing rows read `0`/`''`.
- Capture: the three headers survive `captureHeaders`, every previously
  allowlisted header still does, and a non-allowlisted header still does not.
- Admin: `?subagent=` parsing (absent/1/0/invalid), detail payload.
- Web: filter wiring, detail row, i18n keys in both locales (no browser
  regression suite exists; the record says so).
- Manual: drive opencode v2.3.0 against a local header-echo upstream, open a
  child session, and confirm `subagent=1` / `subagent_source=opencode` on the
  row and the marker header in a capture. Record the result, and record Claude
  Code as production-verified and Codex as source-verified only.

**Phase 2**

- Config: unknown `when` rejected naming entrypoint and index; an entrypoint
  with no unconditional binding rejected; draft exempt; duplicate
  `when: subagent` warns rather than fails; round-trip through `PUT /api/config`.
- Build: matcher generated from the table (assert against a synthetic
  `routing.Request`); feed order places conditioned bindings first within the
  probe tier; `bindingOrder` and `probePriorityOrder` agree; unconditional-only
  entrypoints pass `nil` matchers.
- Selection (httptest upstreams): each of the three markers reaches the
  conditioned upstream; a marker-less request reaches the unconditional one; a
  dead probe on the conditioned binding falls back; a route that builds to zero
  nodes still 502s (documented behavior, asserted so it cannot change silently).
- Admin/health: `when` in payloads and rows; `active` refers to the first
  eligible unconditional binding.
- Web: class select round-trip, labels, i18n.

## Decisions (settled 2026-11-19)

1. Split the work: Phase 1 observability ships first, Phase 2 dispatch only
   after the measured share justifies it. Existing data cannot answer the
   question (verified: the markers are neither on the request row nor on the
   capture allowlist, and UA cannot distinguish a subagent from its lead).
2. No `when: lead`. The class-first ordering already produces identical behavior
   without a negation rule, a lead class or a silent-downgrade failure mode.
3. Class tier above the probe tier: a conditioned binding outranks a probed
   unconditional one for its class; availability management for a conditioned
   binding is its own probe.
4. Marker headers are treated as unauthenticated client input. Accepted and
   documented; any holder of a gateway key can claim the subagent class.
5. "No 502" applies to probe unavailability only. A binding whose route builds
   to zero nodes still shadows the fallback and 502s, as it does today.
6. No automated "configured but not effective" hint in Phase 2; README supplies
   the one-click manual check.
7. The stats column is `subagent_source` (which client's marker matched), not
   `subagent_kind`; UI says 标记来源.
8. Phase 1 observability is two columns plus the Sessions filter and detail.
   No rollups, charts, alerts or graph edges.
9. The three marker headers join the capture allowlist. The "never store raw
   header values" rule applies to the request row; capture is the on-demand,
   bounded exception, as `x-opencode-session` already is.
10. One design document with two phases; Phase 2 is designed but unscheduled.
11. Phase 1's `internal/agent` holds the table and `Classify` only; the matcher
    rule generator lands with Phase 2 so no dead code and no `xrouting`
    dependency appear early.
12. The config field is `when: subagent`, not a boolean: it can grow into
    value-level matching without a schema break.
13. No preset threshold for starting Phase 2. The document records the two
    metrics to inspect — share of requests and share of tokens — and the owner
    judges.
14. The Phase 1 filter is boolean only; a source filter is deferred.

## Deferred

- Per-agent matching (`review`, `compact`, `explore`, custom labels).
- Phase inference from body signals and friction-driven escalation (the later
  Orchestrator tiers).
- Per-class rollups, charts, alerts and graph edges.
- The automated "configured but not effective" health hint.
- A class-scoped `active` in the health payload.
- Filtering Sessions by `subagent_source`.
- Compensating for clients that send no marker (pre-#43188 OpenCode, Kilo).
- Any authentication of the marker headers.