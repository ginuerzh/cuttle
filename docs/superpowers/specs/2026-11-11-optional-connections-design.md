# Optional entrypoint→route and node→provider connections (draft entities)

Date: 2026-11-11
Status: design, pending approval

## Intent

Today the console can only represent a complete routing chain: an entrypoint
must name a route, and a node must name a provider (and a model). The topology
canvas therefore cannot be used to sketch the chain first and wire it up
afterwards — every card that exists is already connected.

This change makes the two associations optional so the console can build a
topology incrementally:

- `entrypoint.route` may be empty (entrypoint has no route yet),
- `node.provider` may be empty (node has no upstream yet).

Such an entity is a **draft**. Drafts are ordinary entities with an empty
reference: they carry an id, a name and validate like any other entity, but
they serve no traffic until the reference is filled in.

The user picked the "empty reference is a draft" semantics over an explicit
`enabled` flag (keeps the existing config surface, no new state to explain) and
over front-end-only drafts (positions and config are shared server-side; local
drafts would vanish for other admins).

## Goals

- `config.Validate` accepts an empty `entrypoint.route` and an empty
  `node.provider`; non-empty references must still resolve to an existing id.
- The data plane compiles a draft config successfully (no rejected reload) and
  keeps **entrypoint order as the only precedence rule**: a request that
  resolves to a draft entrypoint fails immediately and is recorded as
  `unrouted`. It never falls through to a later entrypoint, and never reaches
  an upstream.
- A providerless node is skipped while compiling its route. A route whose every
  node is skipped still compiles; selecting from it fails the same way.
- The console can create, save and display drafts, and can connect them later:
  `node→provider` becomes a draggable direction, and both draft-capable edges
  can be disconnected from the edge panel.

## Non-goals

- No fallthrough, implicit default route, implicit provider or client-model
  pass-through when nothing is connected.
- No new `enabled` field on entrypoints; disabled nodes/providers keep their
  current meaning (skip at compile time, no error) and are not drafts.
- No config migration: existing configs are unaffected, and no new required
  field is added.
- No stats schema change. `unrouted` rows and the `unrouted` graph key already
  exist.

## Constraints

- `config.Load` keeps `yaml.KnownFields(true)` and the
  decode → migrate → expand → defaults → validate order.
- `buildHopGroup` (`internal/gateway/build.go`) compiles exactly one entry per
  entrypoint and inbound protocol group, in declaration order, plus the
  entrypoint's fallback entry; x's FIFO group selector is what makes order
  semantic.
- x `hop_parser.ParseHop` accepts a hop with no nodes and its `Select` returns
  `nil`; x's local forward handler then returns `errNodeNotAvailable`, which the
  gateway answers with a 5xx (`502` for an empty hop) and records an error row with no route (the row
  therefore lands in `unrouted`). This is the mechanism the draft data plane
  relies on; a gateway test must pin it rather than assume it.

## Approaches considered

1. **Empty reference is a draft** (chosen). Relax validation, skip
   providerless nodes, compile an empty hop for an unconnected entrypoint so
   the matcher still claims the model in declaration order. Smallest new
   surface; the topology already draws a defensive `unrouted` edge for a
   missing route.
2. **Explicit `draft`/`enabled` flag on entrypoints.** Safer to reason about
   (nothing is silently unreachable), but it cannot express "a node exists
   before it has a provider": the provider field would still be required, so
   the canvas still cannot be built incrementally. It also adds a second way to
   say "does nothing", overlapping with the existing `enabled` flags.
3. **Front-end-only drafts.** No backend change, but positions are persisted
   server-side in `topology_positions` and shared by every admin; local draft
   cards would disappear on another browser and contradict "config is the
   topology".

## Design

### Configuration semantics

- **Entrypoint.** `match` stays required. `route` may be empty; when non-empty
  it must be a configured route id. An entrypoint with an empty route is a
  draft.
- **Node.** `provider` may be empty. When non-empty it must be a configured
  provider id, and:
  - `model` is required (unchanged),
  - `key`, if set, must exist on that provider (unchanged).
  When `provider` is empty:
  - `key` must be empty (a pinned key needs an owner) — validation error
    naming the node,
  - `model` may be empty; a non-empty model is kept as a hint for reconnecting
    but is not validated against a catalog and is never compiled,
  - `api` must be empty or `auto`; anything else is a validation error because
    there is no provider to resolve it against.
- Struct comments on `Entrypoint` and `Node` say the reference is optional and
  that an empty reference is a draft.

### Data plane compilation (`internal/gateway/build.go`)

- `buildRoutePlan` skips a node whose `provider` is empty, logging one warning
  per node (`route X node Y: no provider, skipped`). This is the same treatment
  as a disabled provider. Because `buildVariantHop` already tolerates an empty
  `plans` slice, the route compiles to a zero-node hop; x returns
  `errNodeNotAvailable` at select time.
- `buildHopGroup` compiles an entrypoint with an empty route against one shared
  synthetic plan (keyed by a sentinel that cannot collide with a route id),
  whose only hop is a zero-node fallback. Only the entrypoint's fallback entry
  is appended, so:
  - the entrypoint's model globs still claim their models in declaration order
    (no fallthrough to a later entrypoint),
  - a matching request reaches a hop with no nodes and fails,
  - no protocol variant entries are registered, because no upstream exists.
- `buildCountTokensPolicy` gives the synthetic plan `estimate: false`, so a
  `count_tokens` request owned by a draft entrypoint is passed through to the
  empty hop and fails like any other request instead of being answered by a
  local estimate.
- Reloads that contain drafts must succeed; a build with drafts logs warnings
  and serves a 5xx (502) for the affected models.

### Admin API

No route or payload change. `/api/nodes`, `/api/routes`, `/api/entrypoints` and
`PUT /api/config` all run the same `Validate`, so they accept drafts once
validation relaxes. The existing "cannot delete a route referenced by an
entrypoint" guard is unchanged: an empty route is not a reference. Deleting a
provider that is still named by a node stays blocked, as today.

### Web console

- **Graph** (`web/src/graph/build.ts`): an entrypoint with an empty route keeps
  its dashed edge to the `unrouted` node, coloured amber and labelled
  `未连接`; a node with an empty provider gets sub-label `未连接`, no
  node→provider edge, and `data.unconnected = true`. Both card kinds render an
  amber `未连接` tag when that flag is set.
- **Edit canvas** (`web/src/pages/Edit.tsx`):
  - `connect` gains `node→provider`: sets the node's `provider` (clearing a
    pinned `key` that belonged to the previous provider), keeping the existing
    `entrypoint→route` and `route→node` directions; other directions still
    report `不支持的连接方向`.
  - The edge panel keeps the `route→node` disconnect and adds `断开` for
    `entrypoint→route` (clear the entrypoint's route) and `node→provider`
    (clear the node's provider and key).
  - `NodeForm` and the node create modal: the provider select is clearable with
    placeholder `未连接（草稿）`; clearing it clears `model`, `api` and `key`
    in the submitted payload, and the model field is only required once a
    provider is chosen.
  - `EntrypointForm` and the entrypoint create modal: the route select is
    clearable and no longer required.
- **List pages**: an entrypoint row shows `未连接` instead of an empty route
  name; a node row shows `未连接` instead of an empty provider.
- New source strings are Chinese and mirrored in `web/src/locales/en-US.ts`.

### Documentation

`README.md` (routing section), the console page table and `AGENTS.md` routing
invariants change from "exactly one route / exactly one provider" to "optional;
an empty reference is a draft that never serves traffic and never falls
through". `example.yaml` stays fully connected (it must keep loading as is).

## Acceptance tests

- Config: drafts validate; a dangling non-empty id still fails with a message
  naming the entity; `node.key` without a provider fails; a non-empty `api`
  without a provider fails; `example.yaml` still loads unchanged.
- Gateway: an entrypoint with no route answers `502` and records one request
  row with no route (unrouted); a *later* entrypoint matching the same model is
  not used; a route whose only node has no provider behaves the same; a
  `count_tokens` request to a draft entrypoint fails instead of being answered
  locally; a reload containing drafts succeeds.
- Admin: creating a draft node and a draft entrypoint through the API works,
  and `GET /api/config` → `PUT /api/config` keeps them.
- Web: `buildGraph` marks drafts and omits the provider edge; `connect`
  handles `node→provider`; the edge panel clears the two new edge kinds; form
  payloads drop `model`/`api`/`key` when the provider is cleared.

## Risks

- **A `*` draft entrypoint blackholes every model.** This is the accepted
  consequence of "no fallthrough". The canvas tags it `未连接`, the README
  states it, and the build logs a warning per draft entrypoint.
- A config with a connected entrypoint whose route has only providerless nodes
  serves 502 with no upstream attempt; route health shows no nodes for it,
  which can look like an empty route rather than a draft. The node cards are
  tagged `未连接`, which is the operator-facing signal.
- Drafts are compiled into the data plane as failing hops; the cost is one
  matcher plus one empty hop per draft entrypoint, which is negligible.
- A draft entrypoint's failures carry no client model, because the client model
  is recorded by the node rewriter and a draft hop has no node. Entrypoint
  attribution resolves the empty model at query time, so such a row is drawn
  against whichever entrypoint matches an empty model (typically the catch-all)
  and the Sessions `?entrypoint=` filter lists nothing for the draft. The row is
  still unrouted and still 502; only its entrypoint attribution is misleading.
  Accepted: the console marks the draft `未连接`, and recovering the client model
  would require parsing every request body outside the node path.

## Open decisions settled by the builder

- Model text on a providerless node is kept (not an error) so reconnecting a
  node does not lose the model the operator typed.
- `api` is rejected rather than ignored without a provider, because an explicit
  protocol with nothing to resolve it against is a configuration mistake.
- The `unrouted` target node stays in the config-derived graph; drafts are
  distinguished by the amber tag and dashed amber edge, not by a new node kind.
