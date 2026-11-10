# Per-route dead state in the topology — design

Date: 2026-11-10
Status: proposed
Repository: `ginuerzh/cuttle`

## Intent

A binding node can be referenced by several routes, and x marks failures per
compiled node instance, so its `dead` state is per route. The topology collapses
that into one card and currently shows dead only when **every** route that
references the node is dead (`web/src/pages/routeHealth.ts`). A node dead in one
route but healthy in another therefore looks completely healthy on the main
monitoring screen, while one route is already skipping it.

The goal is to surface the partial state: the route→node edge says exactly which
binding is dead, and the node card says the node is dead somewhere without
turning the whole card into a false alarm.

## Behavior

### Data

`deadNodeNames` becomes `nodeRouteHealth(health)` returning:

- `dead`: config node names dead in every route that reports them.
- `partial`: node name → dead route names, for nodes dead in at least one but
  not all reporting routes.
- `edges`: `route:<route>|node:<node>` for every route row with `dead: true`.

The denominator is the routes that report the node in `/api/routes/health`, so a
route that skips a disabled node does not count. The within-route collapse
(variants and provider keys) is unchanged.

### Node card

- All routes dead: unchanged — red border, grey icon, red dead-face badge,
  title `dead`.
- Some routes dead: keep the traffic-derived border and icon colour, and show
  the dead-face badge in amber with title `dead in {routes}`. The single badge
  slot keeps its priority: dead, then partial, then error, warn, healthy.
- No dead route: no new visual.

### Route→node edge

`decorateEdge` takes the dead edge set. A dead binding is drawn as a red dashed
stroke (`#dc2626`, dash `6 4`) regardless of traffic colour, and its label gains
a `dead` suffix (the whole label when there are no metrics). Edit mode has no
metrics and still shows the dashed edge. `entrypoint->route` edges are never
dead.

### Detail panel

The binding node panel gains a `dead 路由` row listing the dead route names. The
existing dead icon still marks the all-routes-dead case.

### Comment fix

`web/src/graph/build.ts` says the dead flag means "dead in at least one route";
it means the opposite. Correct it while touching the type.

## Approaches considered

1. **Edge only.** Precise about the binding but the overview still hides the
   problem unless the operator studies edges.
2. **Node card only.** Immediately visible but cannot say which route is dead.
3. **Both (chosen).** Edge marks the binding, card marks the node, panel names
   the routes. Slightly more code and visual surface, but it answers both "is
   something wrong" and "where" without a second page.

## Non-goals

- No backend change: `/api/routes/health` keeps its per-(route, node) shape.
- No split of the within-route collapse across protocol variants or provider
  keys. A route row is dead when any of its variant/key nodes is dead, so an
  edge can be red while another key still serves. Deferred; revisit if the
  payload grows a per-key view.
- Routes page, the Edit route form tags and the node card border for the
  partial case are unchanged.
- No browser visual regression tests; there is no screenshot harness.

## Testing

- `routeHealth`: all-dead, partial with route names, edges set, no health.
- `nodes`: partial mood, amber dead-face badge and title; dead still wins.
- `Topology`: `decorateEdge` dead styling and label, with and without metrics.
- Full gates: `npm test`, `npx tsc --noEmit`, `npm run build`, then
  `GOWORK=off go build ./...`, `go vet ./...`, `go test ./... -count=1`.

## Acceptance criteria

- A node dead in one of two routes shows an amber partial badge whose title
  names the dead route, a dashed red `route->node` edge, and a `dead 路由` row
  in its panel.
- A node dead in every route keeps today's red dead face, grey icon and red
  border, and every one of its `route->node` edges is dashed red.
- A node with no dead route gains no new visual, and traffic-derived error
  styling still works for healthy nodes.
- Edit mode shows the same dead edges without metrics.
- The stale "at least one route" comment is corrected.
