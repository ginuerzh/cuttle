# Probe editing on the entrypoint → route edge, and binding priority

Date: 2026-11-12
Status: design; pending owner approval

Amends `2026-11-12-entrypoint-multi-route-design.md` §1, §3 and §5. The binding
list, the probe runner and the health payload keep their shape; this change
moves where the probe is edited, removes the probe-less placement rule, and
makes probed bindings outrank probe-less ones.

## Intent

Two operator complaints about the shipped feature:

1. The entrypoint form and `entrypoints[].routes[]` are cluttered: a binding
   carries three probe fields even though the console already shows binding
   status on the `entrypoint → route` edge. The probe should be edited on the
   edge, and the entrypoint block should only list routes.
2. The rule "a binding without a probe may appear at most once and only last"
   is too restrictive. Multiple probe-less bindings should be allowed, and
   probed bindings must have priority: traffic uses a probed binding when any
   probe passes, and only falls back to a probe-less binding when every probe
   fails.

## Goals

- Console: the entrypoint drawer shows route order only (no probe fields); the
  `entrypoint → route` edge panel edits `command` / `interval` / `timeout`
  (empty command = no probe) and still disconnects one binding.
- Selection priority: probed bindings first (configuration order), then
  probe-less bindings (configuration order).
- Configuration: multiple probe-less bindings and any ordering are valid.
- The data plane keeps using the x hop group; no new dispatcher.
- Docs and the health payload describe the new priority.

## Non-goals

- No schema change: the probe stays inline on `entrypoint.routes[]`.
- No named probe entities, no top-level binding/edge list.
- No change to the probe runner, its interval/timeout contract, or the
  all-probes-failed → first probed fallback.
- No per-request or per-session route selection.

## Semantics

Selection inside one entrypoint, for every inbound variant:

1. **Probed bindings** are tried first, in configuration order. The first one
   whose probe verdict is eligible serves the request.
2. If **every probed binding is ineligible**, the **first probe-less binding**
   in configuration order serves. Additional probe-less bindings are legal but
   unreachable; the build logs one warning per unreachable binding.
3. If there is **no probe-less binding** and every probe failed, the **first
   probed binding** serves (the existing "do not take the entrypoint down"
   fallback; never a 502 for this case).
4. An entrypoint with **only probe-less bindings** has no probe state at all:
   the first binding serves and nothing is reported as a fallback.

Health mapping:

- `bindings[]` is ordered by selection priority (probed in configuration
  order, then probe-less in configuration order); `index` stays the
  configuration index.
- `active` is the binding that the next request would use.
- `fallback` is true exactly when at least one probed binding exists and none
  is eligible. A probe-less binding serving because every probe failed is a
  fallback; an entrypoint with no probes never reports one.

## Implementation

### Config

- Delete the probe-less-last rule in `config.Validate`; keep duplicate route,
  blank route, unknown route, non-blank command, `interval >= 1s`, `timeout >=
  1s`, `timeout < interval`.
- No schema, migration or API shape change.

### Build and selection

- `buildBindingsHop` builds the per-binding entries as today (index-aligned),
  then feeds the hop group an **ordered** entry slice: probed bindings in
  configuration order, then probe-less bindings in configuration order. The
  inner group stays `FIFO + FailFilter(1, failTimeout)`; the reordering is what
  implements priority, and x's last-resort still returns the first probed
  binding when no probe-less entry exists.
- Log one warning per probe-less binding after the first: it can never serve.
  This is a warning, not a validation error.
- The probe runner keeps marking the entries of its own binding; reordering the
  group's slice must not break the per-binding entry list (they are separate
  slices).

### Health

- `probeSet.Snapshot` emits bindings in the same priority order and computes
  `active` by walking that order; `fallback` follows the rule above.
- No field is added or renamed.

### Console

- `EntrypointForm`: the binding rows become route select + up/down + delete.
  Saving must **preserve the existing probe of every binding by route id**,
  including through reorders and removals; a newly added binding has no probe.
  The hint says probes are edited on the topology edge.
- `EdgeForm` for `entrypoint:<id>->route:<id>`: show the route name and the
  three probe fields, with the same duration/`timeout < interval` validation
  the drawer used; an empty command clears the probe. The command field is a
  multiline input (auto-sized textarea, monospace) because probe commands can
  be long shell pipelines. Save replaces the whole entrypoint list through
  `PUT /api/entrypoints` and preserves every other binding's probe.
- Topology: binding edges carry state through shape, not verbose text.
  - A binding **without a probe** is drawn **dashed** (neutral), because nothing
    checks it; it gets no state label.
  - A binding **whose probe fails and is not serving** is drawn solid amber
    with a `✕` label.
  - The **fallback-active** binding (serving because every probe failed) keeps
    its failure mark and adds `回退`: `✕ 回退` on a probed binding, `回退` on a
    probe-less one.
  - An eligible active binding stays solid and unlabelled. Node `dead` edges are
    unchanged (red dashed, no text) and belong to a different edge family.
  - `hasProbe` is derived from the console's own config binding
    (`entrypoint.routes[]`), not from the health payload, so no API field is
    added. Binding status text stays in the entrypoint list and the detail
    panel.
- Text that explains ordering changes: within probed bindings, order decides;
  probe-less bindings only serve when every probe fails, and only the first
  one.
- UI strings remain Chinese with `en-US` keys.

### Docs

- README: probe edited on the edge, priority rules, multiple probe-less
  bindings, unreachable extras warning, fallback cases.
- AGENTS: replace the probe-less-last invariant with the priority invariant.
- `2026-11-12-entrypoint-multi-route-design.md`: update §1 (validation),
  §3 (selection), §5 (console), and add a short amendment note.

## Files

| File | Change |
| --- | --- |
| `internal/config/config.go`, `config_test.go` | drop the placement rule; tests for multiple probe-less bindings and free ordering |
| `internal/gateway/build.go`, `bindings_test.go` | priority ordering + unreachable warning; selection tests |
| `internal/gateway/bindingprobe.go`, `bindingprobe_test.go` | snapshot order and `fallback` rule |
| `internal/gateway/probe_e2e_test.go` | mixed probe/probe-less switching end to end |
| `web/src/pages/Edit.tsx`, `Edit.test.tsx`, `Edit.dom.test.tsx` | drawer without probe fields (probe-preserving save), edge form probe editor |
| `web/src/pages/Entrypoints.tsx` (hint text), `web/src/locales/en-US.ts` | wording |
| `README.md`, `AGENTS.md`, the original design doc | semantics |

## Testing

- Config: five probe-less bindings in any order load; the removed rule produces
  no error; the remaining rules still reject.
- Selection: probed beats probe-less regardless of position; the first eligible
  probed wins among several; all probes fail → first probe-less (not the first
  binding); no probe-less + all fail → first probed; only probe-less → first
  serves, no fallback.
- Health: payload order, `index` = config index, `fallback` true only when a
  probed binding exists and none is eligible.
- Warning: one log line per unreachable probe-less binding, and none when there
  is only one.
- Console: drawer save preserves probes across reorder/remove; edge form saves
  and clears a probe; interval/timeout validation; disconnect still removes one
  binding.
- End to end: two probed + one probe-less binding; probing flips traffic among
  the probed ones, then to the probe-less one when both fail, then back.

## Decisions

1. Priority is by category, order only inside a category; the entrypoint drawer
   therefore no longer expresses the full selection order, and its hint says so.
2. Unreachable extra probe-less bindings are a build warning, not a validation
   error.
3. No probe-less + all probes failed keeps the existing fallback to the first
   probed binding.
4. The probe stays inline in the config; only the console editing surface moves
   to the edge.
