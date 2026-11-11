# Read-only parameters in the monitor detail panel — design

Date: 2026-11-11
Status: proposed
Repository: `ginuerzh/cuttle`

## Intent

Today a node's settings are only reachable by switching the overview to edit
mode and clicking the card, which opens the editable form. Looking at how a
node is configured therefore means leaving the live monitor — the opposite of
what an operator checking traffic wants.

Monitor mode should show the same parameters, read-only, in the panel it already
opens on click. This is a visibility change only: monitor mode stays read-only.

## Behavior

### Placement

The monitor panel (`NodePanel` in `web/src/App.tsx`) becomes, top to bottom:

1. Title (with the existing dead icon)
2. **参数** — a compact read-only `Descriptions size="small" column={1} bordered`
   block
3. The existing metrics block
4. The existing 最近会话 list

The parameter block is not collapsible and has no button; opening a card shows
it immediately. It sits in both the mobile drawer and the desktop aside, which
render the same `NodePanel`.

Edit mode is unchanged: its panel is still the editable form.

### Rows per kind

The rows mirror the fields the edit forms expose, so nothing new becomes
visible that the console cannot already show.

- `node:` provider name, 模型, 目标 API (`auto` when empty), 固定 key (the pinned
  key's name, otherwise the existing `全部 key（轮询）` label the key select uses).
  `backup` only when on, `weight` only when it differs from `1`, and 启用状态
  only when the node is disabled.
- `route:` 策略, 节点 (display names in selection order, `—` when empty),
  `maxFails`, `failTimeout`, and `retries` / `retryCodes` only when `retries > 0`
  (the codes have no effect without retries).
- `entrypoint:` 模型匹配 (globs joined by `, `), 路由 (display name), 优先级
  (`index + 1` of the ordered entrypoint list).
- `provider:` 类型, one row per configured API base URL (`chat` / `responses` /
  `anthropic` / `gemini`, only the non-empty ones, the row label is the bare API
  name and the value its base URL), key count (`2 个 key`, with
  `（1 个禁用）` appended when any key is disabled), 启用状态 only when disabled.
- `unrouted` or an id the config no longer lists: no parameter block; the panel
  renders exactly as it does today.

Values are strings. Names resolve through the existing `ConfigLabels`
(`configLabels(config)`) so a renamed entity shows its current name; a missing
reference falls back to the raw id like every other label in the console. Ids
themselves are not shown, matching the edit forms.

### Data

New pure module `web/src/pages/entityParams.ts`:

```ts
export interface ParamRow { label: string; value: string }
export function entityParams(config: ConfigResponse, graphId: string): ParamRow[]
```

It splits the graph id with the existing `splitId`, looks the entity up by id,
and returns the rows above. `NodePanel` gains a required `config:
ConfigResponse` prop and renders `entityParams(config, id)`; when the array is
empty no block is rendered. `EdgePanel` is untouched.

New UI strings are Chinese source strings translated through
`web/src/locales/en-US.ts`.

## Approaches considered

1. **Inline read-only block (chosen).** One click shows parameters beside the
   live metrics. Cost: the panel gets longer, so metric rows and the session
   list move down; the omission rules keep it at six rows or fewer.
2. **Collapsible block.** Same content wrapped in an antd `Collapse`. Default
   expanded is approach 1 plus a toggle; default collapsed reintroduces the
   extra click this change removes.
3. **Separate drawer.** A 查看参数 button opens a drawer with the full entity,
   including ids, auth and headers. Keeps the panel compact and can show more,
   but costs a click and does not answer "I opened the card, where is it
   configured".

## Non-goals

- No backend or API change; everything comes from the existing
  `GET /api/config` payload.
- No editing in monitor mode and no jump button into edit mode. The monitor
  panel remains strictly read-only, and monitor mode still forbids dragging and
  connecting.
- No parameters for edges; `EdgePanel` already names the path and its traffic.
- No change to the topology cards, the list pages or the edit forms.
- No browser visual regression test; verification is Vitest plus TypeScript.

## Testing

- `web/src/pages/entityParams.test.ts`: one case per kind, a pinned vs unpinned
  key, the omission rules (backup/weight/disabled/retries), a route with no
  nodes, and an unknown id returning `[]`.
- `web/src/App.test.tsx`: `NodePanel` renders the 参数 block for a node and for
  a provider, and renders none for `unrouted`.
- Web gate: `npm test`, `npx tsc --noEmit`, `npm run build`.
