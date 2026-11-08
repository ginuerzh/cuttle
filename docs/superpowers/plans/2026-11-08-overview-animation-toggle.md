# Overview Animation Switch and Icon Flash Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rename the left-nav Gateway keys entry to `密钥`, replace the node card ring with a one-shot icon flash, and add a persisted master switch that stops all Overview animations.

**Architecture:** A small preference module owns the `localStorage` value; `App` holds the live state and passes it to the toolbar and `Topology`. `Topology` gates activity accumulation and edge decoration, while the node `IconBadge` plays a CSS keyframe flash keyed by the activity pulse.

**Tech Stack:** React 18, Ant Design 5, React Flow 12, TypeScript, Vitest, Vite; CSS keyframes in `web/src/styles.css`.

**Spec:** `docs/superpowers/specs/2026-11-08-overview-animation-toggle-design.md`

## Global Constraints

- UI source strings are Chinese; add every new string to `web/src/locales/en-US.ts`.
- Animation preference storage key is exactly `llm-gw.animation`; `1` means on, `0` means off, anything else means on.
- The icon flash is one-shot, about 600 ms, scale `1 -> 1.24 -> 1.10 -> 1`, with a cyan halo; error pulses use a red halo.
- Do not change the edge light-dot animation or its colors; only gate it.
- Do not add server configuration, Admin API fields or database changes.
- Run web commands from `web/`; run Go commands from the repository root with `GOWORK=off`.
- Commit after each task, including that task's Implementation record. Never push.
- Keep code, comments, docs and commit messages in English.

## Review Focus

- **Animation switched off mid-flight:** removing the pulse/classes must stop an in-progress flash and unmount edge dots immediately.
- **Toggling back on:** old activity recorded while the switch was off must not replay.
- **Rapid snapshots:** a second pulse before the first flash ends restarts it once; flashes must not stack.
- **Reduced motion:** `prefers-reduced-motion: reduce` must suppress the icon flash as it already suppresses edge dots.
- **Storage failures:** blocked or corrupt `localStorage` must default on and never throw.
- **Edit mode:** hiding the monitor controls must not reset the saved toggle state.

## File Structure

- `web/src/nav.ts` — navigation labels; single source for the sidebar text.
- `web/src/animation.ts` (new) — storage key, normalization, load/save helpers.
- `web/src/components/OverviewToolbar.tsx` — monitor controls, including the new switch.
- `web/src/App.tsx` — owns the live animation state and passes it down.
- `web/src/graph/activity.ts` — activity diff/merge plus the animation-aware accumulator.
- `web/src/graph/nodes.tsx` — node renderers and the icon badge flash.
- `web/src/pages/Topology.tsx` — passes the animation flag into node/edge data and activity.
- `web/src/styles.css` — flash keyframes; removes the old ring.
- `web/src/graph/nodes.test.tsx`, `web/src/graph/activity.test.ts`, `web/src/pages/Topology.test.tsx`, `web/src/components/OverviewToolbar.test.tsx` — behavior tests.
- `web/src/animation.test.ts`, `web/src/components/OverviewToolbar.dom.test.tsx` (new) — preference and switch-interaction tests.

---

### Task 1: Rename the Gateway keys navigation label

**Files:**
- Modify: `web/src/nav.ts:32`
- Test: `web/src/nav.test.ts:21-25`

**Interfaces:**
- Consumes: nothing.
- Produces: `NAV_LABELS['gateway-keys'] === '密钥'`, translating to `Key` in `en-US`.

- [ ] **Step 1: Update the failing navigation test**

In `web/src/nav.test.ts`, change the gateway-keys assertion to:

```ts
expect(NAV_LABELS['gateway-keys']).toBe('密钥');
expect(translateFor('en-US', NAV_LABELS['gateway-keys'])).toBe('Key');
```

Keep the existing test name `labels the page in Chinese and English`.

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd web && npx vitest run src/nav.test.ts`
Expected: FAIL because the label is still `网关密钥`.

- [ ] **Step 3: Change the label**

In `web/src/nav.ts`, set:

```ts
'gateway-keys': '密钥',
```

Do not change `web/src/pages/GatewayKeys.tsx`, its dialogs, alerts or the Sessions filter; only the navigation label changes.

- [ ] **Step 4: Run the test to verify it passes**

Run: `cd web && npx vitest run src/nav.test.ts`
Expected: PASS.

- [ ] **Step 5: Fill the Implementation record**

Add the record fields below this task with the actual date, commit SHA, files, commands/results and any deferred items.

- [ ] **Step 6: Commit**

```bash
git add web/src/nav.ts web/src/nav.test.ts docs/superpowers/plans/2026-11-08-overview-animation-toggle.md
git commit -m "feat(web): rename gateway keys navigation label"
```

**Implementation record**
- Date/status: 2026-11-08 — complete
- Repo/commit: `llm-gw` @ `0e7095d6`
- Files changed: `web/src/nav.ts`, `web/src/nav.test.ts`
- Tests/commands and results: `npx vitest run src/nav.test.ts` → 4 passed; RED first showed `网关密钥` vs `密钥`.
- Review findings/fixes: none; the existing `Key` translation in `en-US.ts` already covers the new label.
- Deferred items/limitations: the page heading, dialogs and Sessions filter intentionally keep `网关密钥`.

---

### Task 2: Add the persisted animation preference and toolbar switch

**Files:**
- Create: `web/src/animation.ts`
- Create: `web/src/animation.test.ts`
- Modify: `web/src/components/OverviewToolbar.tsx`
- Modify: `web/src/components/OverviewToolbar.test.tsx`
- Modify: `web/src/locales/en-US.ts`
- Modify: `web/src/i18n.test.ts`
- Modify: `web/src/App.tsx`

**Interfaces:**
- Produces:
  - `ANIMATION_STORAGE_KEY: string` equal to `'llm-gw.animation'`
  - `normalizeAnimationEnabled(raw: string | null | undefined): boolean`
  - `loadAnimationEnabled(): boolean`
  - `saveAnimationEnabled(enabled: boolean): void`
  - `OverviewToolbarProps.animate: boolean` and `OverviewToolbarProps.onAnimate: (animate: boolean) => void`
- Consumes: `translate` from `web/src/i18n`.

- [ ] **Step 1: Write the failing preference test**

Create `web/src/animation.test.ts` with `// @vitest-environment jsdom` at the top and these cases:

```ts
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  ANIMATION_STORAGE_KEY,
  loadAnimationEnabled,
  normalizeAnimationEnabled,
  saveAnimationEnabled,
} from './animation';

beforeEach(() => {
  window.localStorage.clear();
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe('normalizeAnimationEnabled', () => {
  it('defaults to on and only treats the literal 0 as off', () => {
    expect(normalizeAnimationEnabled('0')).toBe(false);
    expect(normalizeAnimationEnabled('1')).toBe(true);
    expect(normalizeAnimationEnabled(null)).toBe(true);
    expect(normalizeAnimationEnabled(undefined)).toBe(true);
    expect(normalizeAnimationEnabled('no')).toBe(true);
  });
});

describe('animation storage', () => {
  it('round-trips the choice under the documented key', () => {
    expect(loadAnimationEnabled()).toBe(true);
    saveAnimationEnabled(false);
    expect(window.localStorage.getItem(ANIMATION_STORAGE_KEY)).toBe('0');
    expect(loadAnimationEnabled()).toBe(false);
    saveAnimationEnabled(true);
    expect(window.localStorage.getItem(ANIMATION_STORAGE_KEY)).toBe('1');
    expect(loadAnimationEnabled()).toBe(true);
  });

  it('defaults on and does not throw when storage is blocked', () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('blocked');
    });
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('blocked');
    });
    expect(loadAnimationEnabled()).toBe(true);
    expect(() => saveAnimationEnabled(false)).not.toThrow();
  });
});
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd web && npx vitest run src/animation.test.ts`
Expected: FAIL because `./animation` does not exist.

- [ ] **Step 3: Implement the preference module**

Create `web/src/animation.ts` with the exact key `llm-gw.animation` and the three functions above. `normalizeAnimationEnabled` returns `raw !== '0'`; `loadAnimationEnabled` and `saveAnimationEnabled` swallow storage exceptions and fall back to `true` / no-op.

- [ ] **Step 4: Run the preference test to verify it passes**

Run: `cd web && npx vitest run src/animation.test.ts`
Expected: PASS.

- [ ] **Step 5: Write the failing toolbar markup test**

In `web/src/components/OverviewToolbar.test.tsx`, add `animate={true}` and `onAnimate={() => undefined}` to both existing renders. Add these assertions to the monitor-mode test:

```ts
expect(html).toContain('ant-switch');
expect(html).toContain('aria-checked="true"');
expect(html).toContain('>动画<');
```

Add a second render in the same test with `animate={false}` and assert `aria-checked="false"`. In the edit-mode test, assert `html` does not contain `ant-switch`.

- [ ] **Step 6: Run the toolbar test to verify it fails**

Run: `cd web && npx vitest run src/components/OverviewToolbar.test.tsx`
Expected: FAIL because the props and switch do not exist.

- [ ] **Step 7: Add the props and switch**

In `web/src/components/OverviewToolbar.tsx`:

- Import `Switch` from `antd`.
- Add `animate: boolean` and `onAnimate: (animate: boolean) => void` to `OverviewToolbarProps` and destructuring.
- In the monitor-mode `Space`, before the range `Select`, render:

```tsx
<Space size={4}>
  <span>{t('动画')}</span>
  <Switch size="small" checked={animate} onChange={onAnimate} aria-label={t('动画')} />
</Space>
```

- [ ] **Step 8: Run the toolbar test to verify it passes**

Run: `cd web && npx vitest run src/components/OverviewToolbar.test.tsx`
Expected: PASS.

- [ ] **Step 9: (Ruling) Do not add the Switch click test**

A minimal Ant Design `Switch` rendered with `createRoot` + `act` in jsdom
exhausts the Vitest worker (OOM), even without the toolbar around it. The
SSR assertions from Step 5 cover the rendered state, and the `onChange`
wiring is a single typed expression; verify the click manually in the
console instead. Do not add `OverviewToolbar.dom.test.tsx`.

- [ ] **Step 10: Add the English translation and its test**

In `web/src/locales/en-US.ts`, add `'动画': 'Animation'`. In `web/src/i18n.test.ts`, add to the translation test:

```ts
expect(translateFor('en-US', '动画')).toBe('Animation');
```

Run: `cd web && npx vitest run src/i18n.test.ts`
Expected: PASS.

- [ ] **Step 11: Wire the state in App**

In `web/src/App.tsx`:

- Import `loadAnimationEnabled` and `saveAnimationEnabled` from `./animation`.
- Near the other Overview state, add:

```tsx
const [animate, setAnimateState] = useState(loadAnimationEnabled);
const setAnimate = useCallback((next: boolean) => {
  saveAnimationEnabled(next);
  setAnimateState(next);
}, []);
```

- Pass `animate={animate}` and `onAnimate={setAnimate}` to `OverviewToolbar`.

Do not pass `animate` to `Topology` yet; Task 3 adds that prop.

- [ ] **Step 12: Run the focused tests and typecheck**

Run:

```bash
cd web
npx vitest run src/animation.test.ts src/components/OverviewToolbar.test.tsx src/i18n.test.ts
npx tsc --noEmit
```

Expected: all PASS and no type errors.

- [ ] **Step 13: Fill the Implementation record and commit**

```bash
git add web/src/animation.ts web/src/animation.test.ts \
  web/src/components/OverviewToolbar.tsx web/src/components/OverviewToolbar.test.tsx \
  web/src/locales/en-US.ts web/src/i18n.test.ts web/src/App.tsx
git commit -m "feat(web): add overview animation preference"
```

**Implementation record**
- Date/status: 2026-11-08 — complete
- Repo/commit: `llm-gw` @ `e282fff0` (code), implementation record committed separately
- Files changed: `web/src/animation.ts`, `web/src/animation.test.ts`, `web/src/components/OverviewToolbar.tsx`, `web/src/components/OverviewToolbar.test.tsx`, `web/src/locales/en-US.ts`, `web/src/i18n.test.ts`, `web/src/App.tsx`
- Tests/commands and results: `npx vitest run src/animation.test.ts src/components/OverviewToolbar.test.tsx src/i18n.test.ts` → 10 passed; `npx tsc --noEmit` → clean. RED first showed the missing module, then the missing switch markup.
- Review findings/fixes: the planned jsdom Switch click test was dropped because antd `Switch` OOMs the Vitest worker under jsdom; SSR assertions plus typed wiring cover the state, and the click is a manual check.
- Deferred items/limitations: no automated click coverage for the switch callback; the toggle is per-browser `localStorage`, not shared between admins or devices.

---

### Task 3: Replace the node ring with a gated icon flash

**Files:**
- Modify: `web/src/graph/nodes.tsx`
- Modify: `web/src/graph/nodes.test.tsx`
- Modify: `web/src/graph/activity.ts`
- Modify: `web/src/graph/activity.test.ts`
- Modify: `web/src/pages/Topology.tsx`
- Modify: `web/src/pages/Topology.test.tsx`
- Modify: `web/src/App.tsx`
- Modify: `web/src/styles.css:110-144`

**Interfaces:**
- Consumes: `Activity`, `ActivityMap`, `diffActivity`, `mergeActivity`, `emptyActivity` from `web/src/graph/activity.ts`.
- Produces:
  - `IconBadge({ color, children, pulse, error }: { color: string; children: React.ReactNode; pulse?: number; error?: boolean })`
  - `accumulateActivity(current: ActivityMap, prev: GraphMetrics | null, next: GraphMetrics, animate: boolean, pulse: number): ActivityMap`
  - `decorateEdge(e: Edge, metrics: GraphMetrics | null, activity?: Activity, animate = true): Edge`
  - `Topology` prop `animate?: boolean` (default `true`).

- [ ] **Step 1: Write the failing activity tests**

In `web/src/graph/activity.test.ts`, import `accumulateActivity` and add:

```ts
describe('accumulateActivity', () => {
  const before = metrics({ 'node:a': [2, 0] });
  const after = metrics({ 'node:a': [5, 1] });

  it('merges new traffic while animations are on', () => {
    const out = accumulateActivity(emptyActivity(), before, after, true, 7);
    expect(out.nodes['node:a']).toMatchObject({ pulse: 7, error: true });
  });

  it('clears activity while animations are off', () => {
    const current = {
      nodes: { 'node:a': { pulse: 1, error: false, duration: 0.9 } },
      edges: {},
    };
    expect(accumulateActivity(current, before, after, false, 7)).toEqual(emptyActivity());
  });

  it('does not replay activity recorded while animations were off', () => {
    const cleared = accumulateActivity(emptyActivity(), before, after, false, 7);
    const reenabled = accumulateActivity(cleared, after, after, true, 8);
    expect(reenabled).toEqual(emptyActivity());
  });

  it('does not animate the first snapshot', () => {
    expect(accumulateActivity(emptyActivity(), null, after, true, 7)).toEqual(emptyActivity());
  });
});
```

- [ ] **Step 2: Run the activity test to verify it fails**

Run: `cd web && npx vitest run src/graph/activity.test.ts`
Expected: FAIL because `accumulateActivity` is not exported.

- [ ] **Step 3: Implement `accumulateActivity`**

In `web/src/graph/activity.ts`, add:

```ts
/**
 * Advance the activity baseline every snapshot, but only keep an animation
 * when animations are enabled. Clearing while disabled is what prevents an
 * old pulse from replaying when the switch is turned back on.
 */
export function accumulateActivity(
  current: ActivityMap,
  prev: GraphMetrics | null,
  next: GraphMetrics,
  animate: boolean,
  pulse: number,
): ActivityMap {
  if (!animate || prev === null) return emptyActivity();
  return mergeActivity(current, diffActivity(prev, next, pulse));
}
```

- [ ] **Step 4: Run the activity test to verify it passes**

Run: `cd web && npx vitest run src/graph/activity.test.ts`
Expected: PASS.

- [ ] **Step 5: Write the failing node flash test**

In `web/src/graph/nodes.test.tsx`, replace the `renders one activity ring...` test with:

```ts
it('flashes the icon for a traffic pulse and turns red for errors', () => {
  const pulseProps = {
    ...props,
    data: { ...props.data, pulse: 3, error: false },
  } as unknown as NodeProps<GraphNode>;
  const html = renderToStaticMarkup(
    <ReactFlowProvider>
      <BindingNode {...pulseProps} />
    </ReactFlowProvider>,
  );
  expect(html).toContain('gw-node-flash');
  expect(html).not.toContain('gw-node-pulse');
  expect(html).not.toContain('gw-node-flash-error');

  const errorProps = {
    ...props,
    data: { ...props.data, pulse: 3, error: true },
  } as unknown as NodeProps<GraphNode>;
  const errorHtml = renderToStaticMarkup(
    <ReactFlowProvider>
      <BindingNode {...errorProps} />
    </ReactFlowProvider>,
  );
  expect(errorHtml).toContain('gw-node-flash-error');
});

it('does not flash while idle', () => {
  const html = renderToStaticMarkup(
    <ReactFlowProvider>
      <BindingNode {...props} />
    </ReactFlowProvider>,
  );
  expect(html).not.toContain('gw-node-flash');
});
```

- [ ] **Step 6: Run the node test to verify it fails**

Run: `cd web && npx vitest run src/graph/nodes.test.tsx`
Expected: FAIL because the flash classes do not exist.

- [ ] **Step 7: Implement the icon flash**

In `web/src/graph/nodes.tsx`:

- Extend `IconBadge` with `pulse` and `error`; give the returned `div` `key={pulse || 0}` and add `gw-node-flash` / `gw-node-flash-error` classes when `pulse` is truthy.
- Add `pulse={data.pulse}` and `error={data.error}` to every `IconBadge` in the five node renderers.
- Delete the `NodePulse` component and every `<NodePulse data={data} />` usage.

In `web/src/styles.css`, replace the `gw-node-pulse` block with a one-shot flash:

```css
@keyframes gw-node-flash-burst {
  0% {
    transform: scale(1);
    filter: brightness(1);
  }
  18% {
    transform: scale(1.24);
    filter: brightness(1.5);
    box-shadow: 0 0 0 3px rgba(255, 255, 255, 0.85), 0 0 28px 9px rgba(var(--gw-flash-rgb), 0.95);
  }
  45% {
    transform: scale(1.1);
    filter: brightness(1.18);
    box-shadow: 0 0 0 1px rgba(255, 255, 255, 0.5), 0 0 12px 3px rgba(var(--gw-flash-rgb), 0.55);
  }
  100% {
    transform: scale(1);
    filter: brightness(1);
    box-shadow: 0 0 0 0 rgba(var(--gw-flash-rgb), 0);
  }
}

.gw-node-flash {
  --gw-flash-rgb: 56, 189, 248;
  animation: gw-node-flash-burst 600ms ease-out 1;
}

.gw-node-flash-error {
  --gw-flash-rgb: 220, 38, 38;
}

@media (prefers-reduced-motion: reduce) {
  .gw-node-flash {
    animation: none;
  }
}
```

- [ ] **Step 8: Run the node test to verify it passes**

Run: `cd web && npx vitest run src/graph/nodes.test.tsx`
Expected: PASS.

- [ ] **Step 9: Write the failing edge-gating test**

In `web/src/pages/Topology.test.tsx`, inside `describe('decorateEdge')`, add:

```ts
it('suppresses the pulse when animations are off', () => {
  const edge = { id: 'route:r|node:n', source: 'route:r', target: 'node:n' } as Edge;
  const decorated = decorateEdge(edge, null, { pulse: 7, error: false, duration: 0.6 }, false);
  expect(decorated.data).toMatchObject({ pulse: 0 });
});
```

- [ ] **Step 10: Run the topology test to verify it fails**

Run: `cd web && npx vitest run src/pages/Topology.test.tsx`
Expected: FAIL because `decorateEdge` ignores the fourth argument.

- [ ] **Step 11: Gate activity and edges in Topology**

In `web/src/pages/Topology.tsx`:

- Add `animate?: boolean` to `Props` and destructure with a default of `true`.
- Add a fourth parameter `animate = true` to `decorateEdge`; set `pulse: animate ? activity?.pulse ?? 0 : 0` and `error: animate ? !!activity?.error : false`.
- Replace the metrics/activity effect body with an `accumulateActivity` call that keeps `prevMetricsRef` advancing even while off:

```ts
const pulse = ++pulseRef.current;
const prev = prevMetricsRef.current;
if (prev === null || sinceRef.current !== since) {
  prevMetricsRef.current = metrics;
  sinceRef.current = since;
  setActivity(emptyActivity());
  return;
}
setActivity((current) => accumulateActivity(current, prev, metrics, animate, pulse));
prevMetricsRef.current = metrics;
```

- Add `animate` to that effect's dependency list.
- In the node/edge update effect, compute `const pulse = animate ? (a?.pulse ?? 0) : 0;`, pass `animate` to `decorateEdge`, and add `animate` to the dependency list.
- In the structure effect, pass `animate` to `decorateEdge` and add `animate` to that effect's dependency list.
- Update the activity imports: `Topology` now uses `accumulateActivity` instead of `diffActivity` and `mergeActivity`.

- [ ] **Step 12: Pass the flag from App**

In `web/src/App.tsx`, wrap the monitor `<Topology ... />` with `animate={animate}`.

- [ ] **Step 13: Run the focused tests and typecheck**

Run:

```bash
cd web
npx vitest run src/graph/activity.test.ts src/graph/nodes.test.tsx src/pages/Topology.test.tsx
npx tsc --noEmit
```

Expected: all PASS and no type errors.

- [ ] **Step 14: Fill the Implementation record and commit**

```bash
git add web/src/graph/nodes.tsx web/src/graph/nodes.test.tsx \
  web/src/graph/activity.ts web/src/graph/activity.test.ts \
  web/src/pages/Topology.tsx web/src/pages/Topology.test.tsx \
  web/src/App.tsx web/src/styles.css \
  docs/superpowers/plans/2026-11-08-overview-animation-toggle.md
git commit -m "feat(web): flash node icons and gate overview animation"
```

**Implementation record**
- Date/status: 2026-11-08 — complete
- Repo/commit: `llm-gw` @ `70e5380f` (code), implementation record committed separately
- Files changed: `web/src/graph/nodes.tsx`, `web/src/graph/nodes.test.tsx`, `web/src/graph/activity.ts`, `web/src/graph/activity.test.ts`, `web/src/pages/Topology.tsx`, `web/src/pages/Topology.test.tsx`, `web/src/App.tsx`, `web/src/styles.css`
- Tests/commands and results: focused run `npx vitest run src/graph/activity.test.ts src/graph/nodes.test.tsx src/pages/Topology.test.tsx` → 33 passed; `npx tsc --noEmit` → clean after removing the now-unused `GraphNodeData` import; full `npm test` → 29 files / 207 tests passed.
- Review findings/fixes: the flash class lives on the badge and is keyed by the pulse so the one-shot animation restarts correctly; `accumulateActivity` clears while disabled, which prevents replay; `Topology` gates node and edge pulses immediately when `animate` flips.
- Deferred items/limitations: CSS animation timing and `prefers-reduced-motion` are not covered by a browser visual test; verify manually in the console.

---

### Task 4: Update guidance and run the full gates

**Files:**
- Modify: `AGENTS.md:317-325`
- Modify: `README.md:275` (Overview description) and the Admin console table if needed
- Modify: `docs/superpowers/plans/2026-11-08-overview-animation-toggle.md`

**Interfaces:**
- Consumes: the finished behavior from Tasks 1-3.
- Produces: no code interfaces; documentation and a verified build.

- [ ] **Step 1: Update the web invariants in AGENTS.md**

Replace the sentence saying the navigation has a `网关密钥` page with one that says the navigation labels it `密钥` (`gateway-keys`) and that the page heading, dialogs and Sessions filter keep `网关密钥`. Add one invariant:

```markdown
- Overview animations share one toolbar switch persisted in
  `localStorage` (`llm-gw.animation`). Off stops node icon flashes and edge
  light dots without pausing metrics. The node flash is one-shot; do not
  reintroduce the card ring.
```

- [ ] **Step 2: Note the switch in the README Overview description**

In `README.md`, extend the Overview row or the monitor-mode paragraph to say the toolbar animation switch stops node flashes and edge light dots and is remembered per browser.

- [ ] **Step 3: Run the full web gate**

Run:

```bash
cd web
npm test
npx tsc --noEmit
npm run build
```

Expected: all tests PASS, no type errors, and the Vite build writes to `../internal/admin/dist`.

- [ ] **Step 4: Run the full Go gate**

Run:

```bash
GOWORK=off go build ./...
GOWORK=off go vet ./...
GOWORK=off go test ./... -count=1
```

Expected: build and vet clean; all Go tests PASS.

- [ ] **Step 5: Fill the Implementation record and commit**

```bash
git add AGENTS.md README.md docs/superpowers/plans/2026-11-08-overview-animation-toggle.md
git commit -m "docs: document overview animation switch"
```

**Implementation record**
- Date/status: TODO
- Repo/commit: TODO
- Files changed: TODO
- Tests/commands and results: TODO
- Review findings/fixes: TODO
- Deferred items/limitations: TODO

---

## Self-Review

- Spec coverage: navigation label (Task 1); preference, persistence and toolbar switch (Task 2); icon flash, ring removal, activity/edge gating and reduced motion (Task 3); docs and full gates (Task 4). No spec requirement is unassigned.
- Step scan: every step names the file, the exact assertion or signature, and the command; no TBD or unspecified validation remains.
- Type consistency: `animate` is the same boolean name in `App`, the toolbar props, `Topology` props, `decorateEdge` and `accumulateActivity`; pulse types stay `number`.
- Review Focus: "off mid-flight" is covered by clearing activity and unmounting dots; "toggling back on" by the no-replay test; "rapid snapshots" by the keyed badge and one pulse map entry; reduced motion and edit-mode state are documented as manual/CSS checks; storage failure is tested in `animation.test.ts`.
- Proportion: four small tasks, mostly signature-level instructions; no full component transcript.
