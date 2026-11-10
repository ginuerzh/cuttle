# Per-route dead state in the topology Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Show which route→node binding is dead on the topology, and show a partial dead state on the node card, instead of hiding a node that is dead in only some of its routes.

**Architecture:** The web console derives a richer view from the existing `/api/routes/health` payload: nodes dead in every reporting route (`dead`), nodes dead in some routes (`partial` with the route names), and the set of dead `route:<r>|node:<n>` edge ids. The topology draws dead bindings as dashed red edges, keeps the red all-dead badge, adds an amber partial badge, and the node panel lists the dead routes. No backend, config or API change.

**Tech Stack:** React 18, Ant Design, React Flow, TypeScript, Vitest, Vite.

**Spec:** `docs/superpowers/specs/2026-11-10-partial-dead-topology-design.md`

**Prerequisites:**
- Work in the `cuttle` repository only; no sibling repository changes and no `go.mod` change.
- Web commands run from `web/`; Go commands run from the repository root with `GOWORK=off`.
- Every task ends with its **Implementation record** filled in and included in that task's commit.
- Commit after each task. Never push.

## Global Constraints

- Web-only change: `/api/routes/health` keeps its per-(route, node) shape, and the within-route collapse across protocol variants and provider keys is unchanged.
- A route row reports `dead`; the denominator for "all routes dead" is the routes that report the node in that payload.
- Node card: all routes dead keeps today's red border, grey icon and red dead face; partial death keeps the traffic-derived border and icon colour.
- Single badge slot priority: `dead`, `partial`, `error`, `warn`, `healthy`, `idle`.
- Dead edges are only `route:<route>|node:<node>` ids, constructed exactly as `web/src/graph/build.ts` constructs them. `entrypoint->route` edges are never dead.
- Colors: dead red `#dc2626`, partial amber `#d97706`; dead edge dash `6 4`.
- Every new UI string is a Chinese source string in the component plus an entry in `web/src/locales/en-US.ts`.
- Gates: `cd web && npm test && npx tsc --noEmit && npm run build`, then `GOWORK=off go build ./... && GOWORK=off go vet ./... && GOWORK=off go test ./... -count=1`. Rebuild the console before the final Go build.
- There is no browser visual harness; state that UI behavior was verified by Vitest, SSR markup and HTTP checks only.

## Review Focus

Inputs and conditions most likely to bite, each pinned by a test in the owning task:

1. **A node reported by exactly one route** — must read as all-dead, never partial. (Task 1.)
2. **A node missing from some routes' health rows** (disabled node, skipped at compile time) — the missing route must not count toward the denominator. (Task 1.)
3. **Empty or unavailable health** — no dead, partial or edge state, and no crash. (Task 1.)
4. **A dead binding that also has traffic** — the counts stay in the label, the stroke is still dashed red, and the traffic error colour does not win. (Task 2.)
5. **A partial node with an elevated error rate** — the partial badge wins over `error`/`warn`, and a node that is both all-dead and partial reads as dead. (Task 3.)

---

### Task 1: Derive per-route node health and wire it through (no visual change)

**Files:**
- Modify: `web/src/pages/routeHealth.ts`
- Modify: `web/src/pages/routeHealth.test.ts`
- Modify: `web/src/App.tsx:51,423,659-666,713,735`
- Modify: `web/src/pages/Topology.tsx:55-66,402-410`
- Modify: `web/src/pages/Edit.tsx:60-64,1168`

**Interfaces:**
- Consumes: `RouteHealth` from `web/src/api/types.ts` (`{ name, maxFails, failTimeout, nodes: RouteNodeHealth[] }`, each node `{ name, provider?, failCount, lastFailure?, dead, deadUntil? }`).
- Produces:
  - `interface NodeRouteHealth { dead: ReadonlySet<string>; partial: ReadonlyMap<string, readonly string[]>; edges: ReadonlySet<string> }`
  - `function nodeRouteHealth(health: readonly RouteHealth[]): NodeRouteHealth`
  - `Topology` and `Edit` take `nodeHealth?: NodeRouteHealth` in place of `deadNodes?: ReadonlySet<string>`.

- [ ] **Step 1: Rewrite the failing test file**

Replace `web/src/pages/routeHealth.test.ts` with:

```ts
import { describe, expect, it } from 'vitest';
import type { RouteHealth } from '../api/types';
import { nodeRouteHealth } from './routeHealth';

const node = (name: string, dead: boolean) => ({ name, failCount: dead ? 2 : 0, dead });
const route = (name: string, ...nodes: ReturnType<typeof node>[]): RouteHealth => ({
  name,
  maxFails: 1,
  failTimeout: '10s',
  nodes,
});

describe('nodeRouteHealth', () => {
  it('marks a node dead when every route that reports it is dead', () => {
    const health = [
      route('r1', node('a', true), node('b', false)),
      route('r2', node('b', true), node('c', false)),
      route('r3', node('d', true)),
    ];

    const got = nodeRouteHealth(health);

    expect([...got.dead].sort()).toEqual(['a', 'd']);
  });

  it('reports partial death with the dead route names in route order', () => {
    const health = [
      route('r1', node('a', true)),
      route('r2', node('a', false)),
      route('r3', node('a', true)),
    ];

    const got = nodeRouteHealth(health);

    expect([...got.partial.keys()]).toEqual(['a']);
    expect(got.partial.get('a')).toEqual(['r1', 'r3']);
    expect(got.dead.size).toBe(0);
  });

  it('lists every dead route→node binding as an edge', () => {
    const health = [route('r1', node('a', true), node('b', false)), route('r2', node('a', false))];

    expect([...nodeRouteHealth(health).edges].sort()).toEqual(['route:r1|node:a']);
  });

  it('does not count a route that does not report the node', () => {
    const health = [route('r1', node('a', true)), route('r2', node('b', false))];

    const got = nodeRouteHealth(health);

    expect([...got.dead]).toEqual(['a']);
    expect(got.partial.size).toBe(0);
  });

  it('returns empty results without health', () => {
    const got = nodeRouteHealth([]);

    expect(got.dead.size).toBe(0);
    expect(got.partial.size).toBe(0);
    expect(got.edges.size).toBe(0);
  });
});
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd web && npx vitest run src/pages/routeHealth.test.ts`
Expected: FAIL — `nodeRouteHealth` is not exported (and `deadNodeNames` no longer imported).

- [ ] **Step 3: Implement `nodeRouteHealth` in `web/src/pages/routeHealth.ts`**

Replace the whole file. Keep the doc comment accurate:

```ts
import type { RouteHealth } from '../api/types';

/** Per-route dead state derived from the route health payload. */
export interface NodeRouteHealth {
  /** Config node names dead in every route that reports them. */
  dead: ReadonlySet<string>;
  /** Config node name -> dead route names, for nodes dead in some routes only. */
  partial: ReadonlyMap<string, readonly string[]>;
  /** `route:<route>|node:<node>` bindings whose route reports the node dead. */
  edges: ReadonlySet<string>;
}

/**
 * Folds the per-route node markers into the three views the topology needs. A
 * node is dead only when every route that reports it is dead; a node dead in
 * some routes stays usable through the others, so it is partial instead. The
 * denominator is the routes present in the payload, so a route that skipped a
 * disabled node does not count.
 */
export function nodeRouteHealth(health: readonly RouteHealth[]): NodeRouteHealth {
  const total = new Map<string, number>();
  const deadRoutes = new Map<string, string[]>();
  const edges = new Set<string>();
  for (const route of health) {
    for (const node of route.nodes) {
      total.set(node.name, (total.get(node.name) ?? 0) + 1);
      if (!node.dead) continue;
      edges.add(`route:${route.name}|node:${node.name}`);
      const names = deadRoutes.get(node.name);
      if (names) {
        names.push(route.name);
      } else {
        deadRoutes.set(node.name, [route.name]);
      }
    }
  }

  const dead = new Set<string>();
  const partial = new Map<string, readonly string[]>();
  for (const [name, names] of deadRoutes) {
    if (names.length === total.get(name)) {
      dead.add(name);
    } else {
      partial.set(name, names);
    }
  }
  return { dead, partial, edges };
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `cd web && npx vitest run src/pages/routeHealth.test.ts`
Expected: PASS, 5 tests.

- [ ] **Step 5: Wire the derived value through App, Topology and Edit**

In `web/src/App.tsx`:

- Replace the `deadNodeNames` import with `nodeRouteHealth` from `./pages/routeHealth`.
- Replace `const deadNodes = useMemo(() => deadNodeNames(routeHealth), [routeHealth]);` with `const nodeHealth = useMemo(() => nodeRouteHealth(routeHealth), [routeHealth]);`.
- In the edit-mode branch pass `nodeHealth={nodeHealth}` to `Edit`.
- In the monitor branch pass `nodeHealth={nodeHealth}` to `Topology`.
- Both `NodePanel` call sites keep the same `dead` expression, now reading `nodeHealth.dead.has(...)`.

In `web/src/pages/Topology.tsx`:

- Import `type NodeRouteHealth` from `./routeHealth` and replace the `deadNodes?: ReadonlySet<string>` prop with `nodeHealth?: NodeRouteHealth`.
- Rename the destructured prop and change the node effect to read `nodeHealth?.dead.has(n.id.slice('node:'.length)) ?? false`; keep the same early-return comparison and dependencies (`[nodeHealth, setNodes]`).

In `web/src/pages/Edit.tsx`:

- Import `type NodeRouteHealth` from `./routeHealth`, rename `deadNodes` to `nodeHealth` in `Props` and in the `Topology` call.

- [ ] **Step 6: Verify types and the whole web suite**

Run: `cd web && npx tsc --noEmit && npm test`
Expected: no type errors, all tests pass. The rendered UI is unchanged in this task.

- [ ] **Step 7: Commit**

```bash
git add web/src/pages/routeHealth.ts web/src/pages/routeHealth.test.ts web/src/App.tsx web/src/pages/Topology.tsx web/src/pages/Edit.tsx docs/superpowers/specs/2026-11-10-partial-dead-topology-design.md docs/superpowers/plans/2026-11-10-partial-dead-topology.md
git commit -m "feat(web): derive per-route node health for the topology"
```

**Implementation record** (fill after the task):
- Date/status: 2026-11-10 / done
- Repository + commit: cuttle `f4784fed` — `feat(web): derive per-route node health for the topology`
- Files changed: `web/src/pages/routeHealth.ts` (`NodeRouteHealth`, `nodeRouteHealth` replacing `deadNodeNames`), `web/src/pages/routeHealth.test.ts` (5 cases), `web/src/App.tsx` (derive `nodeHealth`, pass it to `Edit`/`Topology`/`NodePanel`), `web/src/pages/Topology.tsx` (`nodeHealth` prop and node effect), `web/src/pages/Edit.tsx` (pass-through); plus the design and plan docs.
- Tests/commands and results: `npx vitest run src/pages/routeHealth.test.ts` RED (5 failed, `nodeRouteHealth is not a function`) then GREEN (5 passed); `npx tsc --noEmit` clean; `npm test` 38 files / 246 tests passed.
- Review findings and fixes: none (native execution, self-review only).
- Deferred items/limitations: the partial badge, dead edges and the node-panel row land in Tasks 2–3; the rendered UI is unchanged in this task.

---

### Task 2: Draw dead route→node edges

**Files:**
- Modify: `web/src/pages/Topology.tsx:97-128,318-360`
- Modify: `web/src/pages/Topology.test.tsx:46-65`

**Interfaces:**
- Consumes: `NodeRouteHealth.edges` from Task 1 (`ReadonlySet<string>` of `route:<route>|node:<node>` ids).
- Produces: `decorateEdge(e: Edge, metrics: GraphMetrics | null, activity?: Activity, animate = true, deadEdges?: ReadonlySet<string>): Edge` — a dead edge has `stroke: '#dc2626'`, `strokeDasharray: '6 4'` and a label ending in `dead`.

- [ ] **Step 1: Add the failing tests**

Append to `describe('decorateEdge', ...)` in `web/src/pages/Topology.test.tsx` and add `import type { GraphMetrics } from '../api/types';` next to the existing imports:

```ts
  it('draws a dead binding as a red dashed edge labelled dead', () => {
    const edge = { id: 'route:r|node:n', source: 'route:r', target: 'node:n' } as Edge;

    const decorated = decorateEdge(edge, null, undefined, true, new Set([edge.id]));

    expect(decorated.style).toMatchObject({ stroke: '#dc2626', strokeDasharray: '6 4' });
    expect(renderToStaticMarkup(<>{decorated.label}</>)).toContain('dead');
  });

  it('keeps the traffic counts and still marks a dead binding', () => {
    const edge = { id: 'route:r|node:n', source: 'route:r', target: 'node:n' } as Edge;
    const metrics = {
      nodes: {},
      edges: { [edge.id]: { requests: 10, errors: 2 } },
    } as unknown as GraphMetrics;

    const decorated = decorateEdge(edge, metrics, undefined, true, new Set([edge.id]));
    const html = renderToStaticMarkup(<>{decorated.label}</>);

    expect(html).toContain('10');
    expect(html).toContain('dead');
    expect(decorated.style).toMatchObject({ stroke: '#dc2626', strokeDasharray: '6 4' });
  });

  it('leaves a live binding solid', () => {
    const edge = { id: 'route:r|node:n', source: 'route:r', target: 'node:n' } as Edge;

    const decorated = decorateEdge(edge, null, undefined, true, new Set());

    expect(decorated.style).toMatchObject({ stroke: '#94a3b8' });
    expect((decorated.style as { strokeDasharray?: string }).strokeDasharray).toBeUndefined();
  });
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd web && npx vitest run src/pages/Topology.test.tsx`
Expected: FAIL — `decorateEdge` ignores the fifth argument, so the dead assertions fail.

- [ ] **Step 3: Extend `decorateEdge` and pass the dead edges**

In `web/src/pages/Topology.tsx`, add the `deadEdges?: ReadonlySet<string>` parameter, compute `const dead = deadEdges?.has(e.id) ?? false;`, and:

- label: `dead` alone when there are no metrics, otherwise the existing `edgeLabel(m)` followed by `<span style={{ color: '#dc2626' }}> · dead</span>`;
- stroke: `dead ? '#dc2626' : <existing expression>`;
- style: add `strokeDasharray: dead ? '6 4' : undefined`.

Pass `nodeHealth?.edges` as the fifth argument at all three `decorateEdge` call sites (initial `useState`, the `[structure, animate]` effect, the `[metrics, activity, animate]` effect), and add `nodeHealth?.edges` to those effects' dependencies so a health change re-decorates without waiting for traffic.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd web && npx vitest run src/pages/Topology.test.tsx && npx tsc --noEmit`
Expected: PASS, no type errors.

- [ ] **Step 5: Commit**

```bash
git add web/src/pages/Topology.tsx web/src/pages/Topology.test.tsx docs/superpowers/plans/2026-11-10-partial-dead-topology.md
git commit -m "feat(web): mark dead route-to-node edges in the topology"
```

**Implementation record** (fill after the task):
- Date/status: 2026-11-10 / done
- Repository + commit: cuttle `47663b0d` — `feat(web): mark dead route-to-node edges in the topology`
- Files changed: `web/src/pages/Topology.tsx` (`decorateEdge` fifth `deadEdges` argument, dead stroke/dash/label, `nodeHealth?.edges` at all three call sites and in the effect dependencies), `web/src/pages/Topology.test.tsx` (3 cases).
- Tests/commands and results: `npx vitest run src/pages/Topology.test.tsx` RED (2 failed: label had no `dead`) then GREEN (11 passed); `npx tsc --noEmit` clean.
- Review findings and fixes: none (native execution, self-review only). The third new test (`leaves a live binding solid`) is a regression guard and passed immediately by design.
- Deferred items/limitations: the partial badge, node-card title and panel row land in Task 3; the edge label is plain text plus a red `· dead` suffix, no icon.

---

### Task 3: Partial dead badge, node panel row and docs

**Files:**
- Modify: `web/src/graph/build.ts:16-17`
- Modify: `web/src/graph/nodes.tsx:24-31,135-180,294-320`
- Modify: `web/src/graph/nodes.test.tsx`
- Modify: `web/src/pages/Topology.tsx:402-410`
- Modify: `web/src/App.tsx:203-245,713,735`
- Modify: `web/src/App.test.tsx:39-48`
- Modify: `web/src/locales/en-US.ts:265-270`
- Modify: `README.md:678-686`

**Interfaces:**
- Consumes: `NodeRouteHealth.partial` from Task 1 (`ReadonlyMap<string, readonly string[]>`).
- Produces:
  - `type NodeMood = 'dead' | 'partial' | 'error' | 'warn' | 'healthy' | 'idle'`
  - `function nodeMood(m?: NodeMetrics, dead = false, partial = false): NodeMood`
  - `GraphNodeData.deadRoutes?: string[]`
  - `NodePanel` prop `deadRoutes?: readonly string[]`

- [ ] **Step 1: Add the failing tests**

In `web/src/graph/nodes.test.tsx`, extend the `nodeMood` test (rename it to cover five states) and add a partial-badge test inside `describe('BindingNode', ...)`, where the `render` helper lives:

```ts
  it('gives partial death its own mood and lets dead win', () => {
    expect(nodeMood(stats(10, 0), false, true)).toBe('partial');
    expect(nodeMood(stats(10, 0), true, true)).toBe('dead');
    expect(nodeMood(stats(10, 2), false, true)).toBe('partial');
  });
```

```ts
  it('shows an amber dead face naming the dead routes', () => {
    const html = render({ deadRoutes: ['r1'] });

    expect(html).toContain('data-status="partial"');
    expect(html).toContain('title="dead in r1"');
    expect(html).toContain('color:#d97706');
    expect(html).not.toContain('background:var(--gw-text-muted)');
  });
```

In `web/src/App.test.tsx`, add to `describe('NodePanel', ...)`:

```ts
  it('lists the routes where a partially dead node is dead', () => {
    const html = renderToStaticMarkup(
      <NodePanel
        id="node:fast"
        metrics={metrics}
        since=""
        rangeMs={3_600_000}
        deadRoutes={['r1', 'r3']}
      />,
    );

    expect(html).toContain('dead 路由');
    expect(html).toContain('r1, r3');
  });
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd web && npx vitest run src/graph/nodes.test.tsx src/App.test.tsx`
Expected: FAIL — `partial` is not a mood, the badge has no title prop, and `NodePanel` has no `deadRoutes` prop.

- [ ] **Step 3: Implement the partial state**

- [ ] **Step 3a: Node data type and mood**

In `web/src/graph/build.ts`, replace the `dead` comment with `/** The node's x marker is dead in every route that references it. */` and add `/** Routes where the marker is dead; set only for a partially dead node. */ deadRoutes?: string[];`.

In `web/src/graph/nodes.tsx`, add `'partial'` to `NodeMood`, add `if (partial) return 'partial';` after the `dead` check in `nodeMood`, and add `partial: { color: '#d97706', label: '部分 dead' }` to `MOOD_STYLE`.

- [ ] **Step 3b: Badge and binding node**

Still in `web/src/graph/nodes.tsx`: render `DeadFaceIcon` for both `dead` and `partial`, give `NodeMoodBadge` an optional `title` prop used as the `title` attribute (falling back to the translated label) while `aria-label` stays the translated label, and in `BindingNode` compute `const deadRoutes = data.deadRoutes ?? [];` and `const partial = !data.dead && deadRoutes.length > 0;`. Set the card `title` to `data.dead ? 'dead' : partial ? t('dead in {routes}', { routes: deadRoutes.join(', ') }) : undefined`, keep the icon grey only for `data.dead`, and pass `nodeMood(data.metrics, data.dead, partial)` plus that title to `NodeMoodBadge`.

- [ ] **Step 3c: Topology node data**

In `web/src/pages/Topology.tsx`, extend the node effect from Task 1 to also set `deadRoutes: [...(nodeHealth?.partial.get(name) ?? [])]`, and skip the update only when both `dead` and `deadRoutes` are unchanged (compare the arrays element-wise with a local helper).

- [ ] **Step 3d: Node panel row**

In `web/src/App.tsx`, add `deadRoutes = []` to `NodePanel`'s props (`deadRoutes?: readonly string[]`) and render a `dead 路由` description item with `deadRoutes.join(', ')` when it is non-empty. Pass `deadRoutes={selection.id.startsWith('node:') ? nodeHealth.partial.get(selection.id.slice('node:'.length)) : undefined}` at both `NodePanel` call sites.

- [ ] **Step 3e: Strings and docs**

In `web/src/locales/en-US.ts`, add next to the existing dead entries:

```ts
  '部分 dead': 'partial dead',
  'dead in {routes}': 'dead in {routes}',
  'dead 路由': 'Dead routes',
```

In `README.md`, extend the `/api/routes/health` paragraph: the topology marks a binding node dead only when every route that reports it is dead, a node dead in some routes shows an amber badge, its dead `route -> node` edges are drawn dashed red, and the node panel lists the dead routes.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd web && npx vitest run src/graph/nodes.test.tsx src/App.test.tsx && npx tsc --noEmit`
Expected: PASS, no type errors.

- [ ] **Step 5: Commit**

```bash
git add web/src/graph/build.ts web/src/graph/nodes.tsx web/src/graph/nodes.test.tsx web/src/pages/Topology.tsx web/src/App.tsx web/src/App.test.tsx web/src/locales/en-US.ts README.md docs/superpowers/plans/2026-11-10-partial-dead-topology.md
git commit -m "feat(web): show partially dead nodes on the topology"
```

**Implementation record** (fill after the task):
- Date/status:
- Repository + commit:
- Files changed:
- Tests/commands and results:
- Review findings and fixes:
- Deferred items/limitations:

---

### Task 4: Full gate

**Files:**
- Modify: none expected; only fixes the gate requires.

**Interfaces:**
- Consumes: everything from Tasks 1–3.
- Produces: a green web gate and a green Go gate with the rebuilt console bundle.

- [ ] **Step 1: Run the web gate**

Run: `cd web && npm test && npx tsc --noEmit && npm run build`
Expected: all tests pass, no type errors, Vite writes `internal/admin/dist`.

- [ ] **Step 2: Run the Go gate**

Run: `GOWORK=off go build ./... && GOWORK=off go vet ./... && GOWORK=off go test ./... -count=1`
Expected: all pass. `internal/admin/dist` is git-ignored; do not stage it.

- [ ] **Step 3: Commit any gate fixes**

Only if the gate required changes:

```bash
git add -A web/src README.md docs/superpowers/plans/2026-11-10-partial-dead-topology.md
git commit -m "fix(web): satisfy the full gate"
```

If nothing changed, record the gate results in the implementation record instead.

**Implementation record** (fill after the task):
- Date/status:
- Repository + commit:
- Files changed:
- Tests/commands and results:
- Review findings and fixes:
- Deferred items/limitations:
