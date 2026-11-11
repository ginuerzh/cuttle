# Read-only Parameters in the Monitor Panel — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Show each entity's configuration, read-only, in the topology monitor's detail panel so parameters no longer require switching to edit mode.

**Architecture:** A pure module `web/src/pages/entityParams.ts` maps a graph id (`node:<id>`, `route:<id>`, `entrypoint:<id>`, `provider:<id>`) plus the loaded config to a list of `{label, value}` rows. `NodePanel` in `web/src/App.tsx` gains a required `config` prop, renders those rows in a `Descriptions` block between the title and the metrics, and renders nothing when the list is empty.

**Tech Stack:** React 18 + TypeScript, antd (`Descriptions`), React Flow, Vitest + `renderToStaticMarkup` (no DOM needed for these assertions), Vite.

**Spec:** `docs/superpowers/specs/2026-11-11-monitor-node-params-design.md`

## Global Constraints

- Monitor mode stays read-only: no editing, no dragging, no new buttons, no navigation into edit mode.
- Rows mirror fields the edit forms already expose; no entity `id` is ever displayed.
- All work is in `/root/code/go-gost/cuttle`. No Go change; no backend change.
- UI source strings are Chinese and every new one gets an entry in `web/src/locales/en-US.ts`.
- Names resolve through `configLabels(config)`; an unknown reference falls back to the raw id.
- TypeScript must pass `npx tsc --noEmit`; there is no browser visual regression test.
- Run `npm` commands from `cuttle/web`. Commit after each task; never push.

## Review Focus

- Selected entity deleted by a config reload, or `unrouted`: the panel keeps showing metrics and sessions with **no** parameter block and no crash.
- A node pinned to a provider key id that no longer exists: the 固定 key row shows the raw id, never an empty cell.
- A stale `node.api` (e.g. `gemini` on a provider with no Gemini base URL): shown verbatim, not blanked or dropped.
- A route with no nodes: the 节点 row reads `—` instead of an empty value.
- English UI: every new string has an `en-US` entry; a missing one silently leaks Chinese into the panel.

---

### Task 1: `entityParams` module

**Files:**
- Create: `web/src/pages/entityParams.ts`
- Test: `web/src/pages/entityParams.test.ts`

**Interfaces:**
- Consumes: `splitId` (`web/src/pages/selection.ts`), `configLabels`/`entityLabel` (`web/src/labels.ts`), `configuredAPIs` (`web/src/pages/presets.ts`), `translate` (`web/src/i18n.ts`), types from `web/src/api/types.ts`.
- Produces:
  ```ts
  export interface ParamRow { label: string; value: string }
  export function entityParams(config: ConfigResponse, graphId: string): ParamRow[]
  ```
  Both the label and the value are already translated (the function calls `translate` itself), so callers render them verbatim. It never throws and returns `[]` for an unknown kind or entity.

- [x] **Step 1: Write the failing test**

`web/src/pages/entityParams.test.ts`, with `beforeEach(() => setActiveLang('zh-CN'))` and this fixture (typed `as unknown as ConfigResponse` as in `labels.test.ts`):

```ts
const config = {
  gateway: { listen: ':8787' },
  admin: { listen: ':8788', sessionTTL: '12h' },
  providers: [
    {
      id: 'p_0000000001',
      name: 'DeepSeek 官方',
      type: 'deepseek',
      chat: 'https://api.deepseek.com/v1',
      responses: 'https://api.deepseek.com/v1/responses',
      keys: [
        { id: 'pk_0000000001', name: 'account-a', keyPreview: 'sk-…a', enabled: true },
        { id: 'pk_0000000002', name: 'account-b', keyPreview: 'sk-…b', enabled: false },
      ],
    },
  ],
  nodes: [
    {
      id: 'n_0000000001',
      name: 'ds-fast',
      provider: 'p_0000000001',
      model: 'deepseek-v4-flash',
      api: 'chat',
      key: 'pk_0000000002',
      backup: true,
      weight: 2,
      enabled: false,
    },
    { id: 'n_0000000002', name: 'ds-plain', provider: 'p_0000000001', model: 'deepseek-v4' },
    { id: 'n_0000000003', name: 'ds-stale', provider: 'p_0000000001', model: 'deepseek-v4', api: 'gemini' },
  ],
  routes: [
    {
      id: 'r_0000000001',
      name: 'strong',
      nodes: ['n_0000000001', 'n_0000000002'],
      strategy: 'sticky',
      maxFails: 2,
      failTimeout: '30s',
      retries: 2,
      retryCodes: '429,5xx',
    },
    { id: 'r_0000000002', name: 'bare', nodes: [] },
  ],
  entrypoints: [
    { id: 'e_0000000001', name: 'fast', match: ['*'], route: 'r_0000000001' },
    { id: 'e_0000000002', name: 'claude', match: ['gpt-*', 'claude-*'], route: 'r_0000000002' },
  ],
} as unknown as ConfigResponse;
```

Tests (one `it` each, assertions as written):

```ts
it('lists a fully configured node', () => {
  expect(entityParams(config, 'node:n_0000000001')).toEqual([
    { label: 'provider', value: 'DeepSeek 官方' },
    { label: '模型', value: 'deepseek-v4-flash' },
    { label: '目标 API（协议）', value: 'chat' },
    { label: '固定 key', value: 'account-b' },
    { label: 'backup', value: '是' },
    { label: 'weight', value: '2' },
    { label: '启用状态', value: '已禁用' },
  ]);
});

it('omits default node rows and names the round-robin fallback', () => {
  expect(entityParams(config, 'node:n_0000000002')).toEqual([
    { label: 'provider', value: 'DeepSeek 官方' },
    { label: '模型', value: 'deepseek-v4' },
    { label: '目标 API（协议）', value: 'auto' },
    { label: '固定 key', value: '全部 key（轮询）' },
  ]);
});

it('shows a pinned key id the provider no longer lists', () => {
  const stale = entityParams(
    { ...config, nodes: [{ ...config.nodes[1], key: 'pk_gone0000001' }] },
    'node:n_0000000002',
  );
  expect(stale.find((r) => r.label === '固定 key')).toEqual({ label: '固定 key', value: 'pk_gone0000001' });
});

it('keeps a stale target API verbatim', () => {
  expect(entityParams(config, 'node:n_0000000003')).toContainEqual({
    label: '目标 API（协议）',
    value: 'gemini',
  });
});

it('lists a route with retries in selection order', () => {
  expect(entityParams(config, 'route:r_0000000001')).toEqual([
    { label: 'strategy', value: 'sticky' },
    { label: '节点', value: 'ds-fast, ds-plain' },
    { label: 'maxFails', value: '2' },
    { label: 'failTimeout', value: '30s' },
    { label: 'retries', value: '2' },
    { label: 'retryCodes', value: '429,5xx' },
  ]);
});

it('falls back to route defaults, drops retry rows and marks an empty node list', () => {
  expect(entityParams(config, 'route:r_0000000002')).toEqual([
    { label: 'strategy', value: 'round' },
    { label: '节点', value: '—' },
    { label: 'maxFails', value: '1' },
    { label: 'failTimeout', value: '10s' },
  ]);
});

it('lists an entrypoint with its route name and position', () => {
  expect(entityParams(config, 'entrypoint:e_0000000002')).toEqual([
    { label: '模型匹配', value: 'gpt-*, claude-*' },
    { label: '路由', value: 'bare' },
    { label: '优先级', value: '2' },
  ]);
});

it('lists provider type, base URLs, key count and disabled state', () => {
  expect(entityParams({ ...config, providers: [{ ...config.providers[0], enabled: false }] }, 'provider:p_0000000001'))
    .toEqual([
      { label: '类型', value: 'deepseek' },
      { label: 'chat', value: 'https://api.deepseek.com/v1' },
      { label: 'responses', value: 'https://api.deepseek.com/v1/responses' },
      { label: 'key', value: '2 个 key（1 个禁用）' },
      { label: '启用状态', value: '已禁用' },
    ]);
});

it('returns nothing for unrouted, unknown ids and unknown kinds', () => {
  expect(entityParams(config, 'unrouted')).toEqual([]);
  expect(entityParams(config, 'node:n_gone000001')).toEqual([]);
  expect(entityParams(config, 'route:r_gone000001')).toEqual([]);
  expect(entityParams(config, 'edge:whatever')).toEqual([]);
});
```

- [x] **Step 2: Run the test and watch it fail**

Run: `cd cuttle/web && npx vitest run src/pages/entityParams.test.ts`
Expected: FAIL — `Failed to resolve import "./entityParams"`.

- [x] **Step 3: Implement `web/src/pages/entityParams.ts`**

```ts
export interface ParamRow {
  label: string;
  value: string;
}

export function entityParams(config: ConfigResponse, graphId: string): ParamRow[]
```

Body rules, in this order:
- `const labels = configLabels(config)` once; look the entity up by id and `return []` when it is missing.
- `node:` provider → `entityLabel(node.provider, labels.providers)`; 模型 → `node.model`; 目标 API（协议） → `node.api || 'auto'`; 固定 key → `node.key ? entityLabel(node.key, labels.providerKeys) : translate('全部 key（轮询）')`; then `backup` → `translate('是')` only when `node.backup`, `weight` → `String(node.weight)` only when `(node.weight ?? 1) !== 1`, 启用状态 → `translate('已禁用')` only when `node.enabled === false`.
- `route:` strategy → `route.strategy || 'round'`; 节点 → `route.nodes.map((id) => entityLabel(id, labels.nodes)).join(', ') || '—'`; maxFails → `String(route.maxFails ?? 1)`; failTimeout → `route.failTimeout || '10s'`; then `retries` → `String(route.retries)` and `retryCodes` → `route.retryCodes || '429,5xx'`, both only when `(route.retries ?? 0) > 0`.
- `entrypoint:` 模型匹配 → `entrypoint.match.join(', ')`; 路由 → `entityLabel(entrypoint.route, labels.routes)`; 优先级 → `String(index + 1)` where `index` is `config.entrypoints.findIndex((e) => e.id === entrypoint.id)` (`0` when not found).
- `provider:` 类型 → `provider.type`; then one row per `configuredAPIs(provider)` with the bare API name as the label and the base URL as the value; `key` → `translate('{count} 个 key', { count: (provider.keys ?? []).length })` plus `translate('（{count} 个禁用）', { count: disabled })` appended when `disabled = (provider.keys ?? []).filter((k) => k.enabled === false).length` is above 0; 启用状态 → `translate('已禁用')` only when `provider.enabled === false`.
- Everything else: `return []`.
- Wrap every literal label in `translate(...)` as well, so the returned rows are fully rendered strings.

- [x] **Step 4: Run the test and watch it pass**

Run: `cd cuttle/web && npx vitest run src/pages/entityParams.test.ts`
Expected: PASS, 9 tests.

- [x] **Step 5: Typecheck**

Run: `cd cuttle/web && npx tsc --noEmit`
Expected: no output, exit 0.

- [x] **Step 6: Commit**

```bash
git -C /root/code/go-gost/cuttle add web/src/pages/entityParams.ts web/src/pages/entityParams.test.ts docs/superpowers/specs/2026-11-11-monitor-node-params-design.md docs/superpowers/plans/2026-11-11-monitor-node-params.md
git -C /root/code/go-gost/cuttle commit -m "feat(web): map a graph id to read-only entity parameters"
```

#### Implementation record — Task 1

- Date / status: 2026-11-11 — DONE (code in `2435991c`); this record is added by the follow-up plan commit.
- Repository + commit: `github.com/ginuerzh/cuttle` @ `2435991c` (`feat(web): map a graph id to read-only entity parameters`).
- Files changed: `web/src/pages/entityParams.ts` (new), `web/src/pages/entityParams.test.ts` (new).
- Tests/commands and results: RED `npx vitest run src/pages/entityParams.test.ts` → `Failed to load url ./entityParams` (no tests collected). GREEN the same command, 9/9 passing. `npx tsc --noEmit` exit 0.
- Review findings and fixes: none yet (no review on this task; the whole-branch review covers it). Two details settled while implementing: the return type is `ParamRow[]` with both sides translated, so `NodePanel` can render rows verbatim; `configuredAPIs` from `presets.ts` is reused so the provider rows list exactly the APIs the node API select offers. Whole-branch review: no finding against this module. The reviewer's minor about `configLabels(config)` running on every panel render is deferred (a `useMemo` here would freeze the language, because `translate` reads a module-level `activeLang`); the double `find`/`findIndex` in the entrypoint branch and the `1 keys` English plural are deferred minors as well.
- Deferred items/limitations: the row order and omission rules live in this module only; the panel renders whatever it returns next task. `entrypoint.match` is joined with `, ` and a single-glob entrypoint therefore reads the same as a multi-glob one.

---

### Task 2: Render the parameter block in `NodePanel`

**Files:**
- Modify: `web/src/App.tsx` (`NodePanel` around lines 208–320; the two `<NodePanel …>` call sites around lines 733 and 757)
- Modify: `web/src/locales/en-US.ts`
- Test: `web/src/App.test.tsx`, `web/src/i18n.test.ts`

**Interfaces:**
- Consumes: `entityParams(config, graphId): ParamRow[]` and `ParamRow` from Task 1.
- Produces: `NodePanel` props become `{ config: ConfigResponse; id: string; label?: string; metrics?: NodeMetrics; since?: string; rangeMs: number; dead?: boolean; deadRoutes?: readonly string[] }` — `config` is required and the only new prop.

- [x] **Step 1: Add the English strings**

In `web/src/locales/en-US.ts` add, keeping the file's existing ordering style:

```ts
'参数': 'Parameters',
'类型': 'Type',
'模型匹配': 'Model match',
'优先级': 'Priority',
'{count} 个 key': '{count} keys',
'（{count} 个禁用）': ' ({count} disabled)',
'启用状态': 'Status',
```

- [x] **Step 2: Extend `i18n.test.ts` and watch it pass**

Add one case to the existing `describe('translateFor')`:

```ts
it('translates the monitor parameter labels', () => {
  expect(translateFor('en-US', '参数')).toBe('Parameters');
  expect(translateFor('en-US', '模型匹配')).toBe('Model match');
  expect(translateFor('en-US', '{count} 个 key', { count: 2 })).toBe('2 keys');
  expect(translateFor('en-US', '（{count} 个禁用）', { count: 1 })).toBe(' (1 disabled)');
});
```

Run: `cd cuttle/web && npx vitest run src/i18n.test.ts` — Expected: PASS.

- [x] **Step 3: Write the failing panel tests**

In `web/src/App.test.tsx`, add this fixture next to `metrics` (import `ConfigResponse` as a type):

```ts
const cfg = {
  gateway: { listen: ':8787' },
  admin: { listen: ':8788', sessionTTL: '12h' },
  providers: [{ id: 'p_0000000001', name: 'DeepSeek 官方', type: 'deepseek', keys: [] }],
  nodes: [{ id: 'n_0000000001', name: 'fast', provider: 'p_0000000001', model: 'deepseek-v4-flash' }],
  routes: [{ id: 'r_0000000001', name: 'strong', nodes: ['n_0000000001'] }],
  entrypoints: [{ id: 'e_0000000001', name: 'all', match: ['*'], route: 'r_0000000001' }],
} as unknown as ConfigResponse;
```

New cases in `describe('NodePanel')`:

```ts
it('shows the node parameters above the metrics', () => {
  const html = renderToStaticMarkup(
    <NodePanel config={cfg} id="node:n_0000000001" label="fast" metrics={metrics} since="" rangeMs={3_600_000} />,
  );
  expect(html).toContain('参数');
  expect(html).toContain('deepseek-v4-flash');
  expect(html).toContain('全部 key（轮询）');
  expect(html.indexOf('参数')).toBeLessThan(html.indexOf('P50 / P95'));
});

it('shows the provider parameters', () => {
  const html = renderToStaticMarkup(
    <NodePanel config={cfg} id="provider:p_0000000001" label="p" metrics={metrics} since="" rangeMs={3_600_000} />,
  );
  expect(html).toContain('类型');
  expect(html).toContain('deepseek');
  expect(html).toContain('0 个 key');
});

it('renders no parameter block for unrouted or unknown ids', () => {
  const unrouted = renderToStaticMarkup(
    <NodePanel config={cfg} id="unrouted" metrics={metrics} since="" rangeMs={3_600_000} />,
  );
  const gone = renderToStaticMarkup(
    <NodePanel config={cfg} id="node:n_gone000001" metrics={metrics} since="" rangeMs={3_600_000} />,
  );
  expect(unrouted).not.toContain('参数');
  expect(gone).not.toContain('参数');
});
```

Also pass `config={cfg}` to every existing `<NodePanel …>` in the file (nine call sites) so the required prop typechecks.

- [x] **Step 4: Run the tests and watch them fail**

Run: `cd cuttle/web && npx vitest run src/App.test.tsx`
Expected: FAIL — the parameter assertions find no `参数`, plus a TypeScript complaint about the unknown `config` prop.

- [x] **Step 5: Render the block in `NodePanel`**

Run: `cd cuttle/web && npx tsc --noEmit`
Expected: FAIL on the property `config` not existing on `NodePanel` props.

- [x] **Step 6: Implement the `config` prop and the block**

`NodePanel` destructures `config` and, immediately after the existing title `</Typography.Title>` and before the metrics `Descriptions`, renders:

```tsx
const params = entityParams(config, id);
...
{params.length > 0 ? (
  <>
    <Typography.Text strong style={{ display: 'block', marginBottom: 8 }}>
      {translate('参数')}
    </Typography.Text>
    <Descriptions size="small" column={1} bordered style={{ marginBottom: 16 }}>
      {params.map((row) => (
        <Descriptions.Item key={row.label} label={row.label}>
          {row.value}
        </Descriptions.Item>
      ))}
    </Descriptions>
  </>
) : null}
```

Import `entityParams` from `./pages/entityParams`. Pass `config={config}` at both `<NodePanel …>` call sites in `App.tsx`; `EdgePanel` is untouched.

- [x] **Step 7: Run the tests and verify they pass**

Run: `cd cuttle/web && npx vitest run src/App.test.tsx src/i18n.test.ts`
Expected: PASS.

- [x] **Step 8: Run the full web gate**

Run: `cd cuttle/web && npm test && npx tsc --noEmit && npm run build`
Expected: all three succeed; `npm test` reports no failures.

- [x] **Step 9: Commit**

```bash
git -C /root/code/go-gost/cuttle add web/src/App.tsx web/src/App.test.tsx web/src/i18n.test.ts web/src/locales/en-US.ts docs/superpowers/plans/2026-11-11-monitor-node-params.md
git -C /root/code/go-gost/cuttle commit -m "feat(web): show read-only entity parameters in the monitor panel"
```

#### Implementation record — Task 2

- Date / status: 2026-11-11 — DONE (code in `bad3bbd9`); this record is added by the follow-up plan commit.
- Repository + commit: `github.com/ginuerzh/cuttle` @ `bad3bbd9` (`feat(web): show read-only entity parameters in the monitor panel`).
- Files changed: `web/src/App.tsx` (`NodePanel` takes `config`, renders the 参数 block; a local `renderNodePanel(id)` now serves both the mobile drawer and the desktop aside), `web/src/App.test.tsx`, `web/src/i18n.test.ts`, `web/src/locales/en-US.ts`.
- Tests/commands and results: RED `npx vitest run src/App.test.tsx` → both new parameter assertions failed (`expected '<div><h5 …' to contain '参数'`), and `npx tsc --noEmit` → `Property 'config' does not exist on type …`. GREEN the same commands (17 `App.test.tsx` tests, 32 focused tests). Web gate: `npm test` 300/300, `npx tsc --noEmit` exit 0, `npm run build` succeeded (the existing >500 kB chunk-size warning is pre-existing, not from this change).
- Review findings and fixes: none yet (no per-task review; the whole-branch review covers it). One plan deviation, ledgered: `App`'s `config` is `ConfigResponse | null`, so `config={config}` could not typecheck; the panels go through `renderNodePanel`, which returns `null` while the config is absent. **Whole-branch review** graded the branch "ready to merge with fixes": no Critical, one Important — the plan's Review Focus bullet 5 (English leaks Chinese when an `en-US` entry is missing) was pinned by only four assertions. Fixed in `web/src/App.test.tsx` with a whole-panel English test (`renders the parameters in English`) that forbids CJK; it is RED when an `en-US` entry is removed (verified by deleting `'模型': 'Models'`) and GREEN with it. The reviewer confirmed the ledger's `renderNodePanel` ruling stands. Minors deferred: English `1 keys` plural, the count-only `key` row label, `configLabels` rebuilt per render (memoizing would freeze the panel's language), the entrypoint `find` + `findIndex` double walk.
- Deferred items/limitations: a selection can only exist once the topology rendered, which needs the config, so the `null` branch is unreachable today. The panel is 320px wide on desktop and the session list moves down by the parameter block's height; the row set keeps that to six rows or fewer. Disabled providers and disabled pinned keys are shown by name only, not flagged; that is the spec's contract and a candidate follow-up.
