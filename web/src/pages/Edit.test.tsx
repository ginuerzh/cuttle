import { renderToStaticMarkup } from 'react-dom/server';
import { beforeEach, describe, expect, it } from 'vitest';
import { AddButtons, connectEntrypointEdge, disconnectEntrypointEdge, EdgeForm, EntrypointForm, entrypointBindingInputs, NodeForm, probeDurationMs, probeFromForm, probeIntervalError, probeTimeoutError, ProviderForm, RouteForm, withBindingProbe } from './Edit';
import { setActiveLang } from '../i18n';
import type { ConfigResponse, Entrypoint, EntrypointRoute, Node, Provider, Route } from '../api/types';

beforeEach(() => setActiveLang('zh-CN'));

describe('AddButtons', () => {
  it('uses the short Chinese nouns because the plus icon carries the action', () => {
    const html = renderToStaticMarkup(<AddButtons onAdd={() => undefined} />);

    expect(html).toContain('提供商');
    expect(html).toContain('节点');
    expect(html).toContain('路由');
    expect(html).toContain('入口');
    expect(html).not.toContain('添加');
  });

  it('translates every add action', () => {
    setActiveLang('en-US');
    const html = renderToStaticMarkup(<AddButtons onAdd={() => undefined} />);

    expect(html).toContain('Providers');
    expect(html).toContain('Nodes');
    expect(html).toContain('Routes');
    expect(html).toContain('Entrypoints');
    expect(html).not.toContain('Add Provider');
  });
});

describe('ProviderForm', () => {
  it('renders one named row per configured key', () => {
    const provider: Provider = {
      id: 'p_0000000001',
      name: 'p',
      type: 'generic',
      chat: 'https://a.example/v1',
      keys: [
        { id: 'pk_0000000001', name: 'account-a', keyPreview: 'sk-...a', enabled: true },
        { id: 'pk_0000000002', name: 'account-b', keyPreview: 'sk-...b', enabled: false },
      ],
    };
    const html = renderToStaticMarkup(<ProviderForm provider={provider} onSaved={async () => undefined} />);
    expect(html).toContain('account-a');
    expect(html).toContain('sk-...a');
    expect(html).toContain('account-b');
    expect(html).toContain('sk-...b');
  });

  it('says a node pins a key, so a rename does not touch nodes', () => {
    const provider: Provider = {
      id: 'p_0000000001',
      name: 'p',
      type: 'generic',
      chat: 'https://a.example/v1',
      keys: [{ id: 'pk_0000000001', name: 'account-a', keyPreview: 'sk-...a', enabled: true }],
    };
    const html = renderToStaticMarkup(<ProviderForm provider={provider} onSaved={async () => undefined} />);
    expect(html).toContain('key 改名不会影响已固定它的 node。');
    expect(html).not.toContain('按名称固定');

    setActiveLang('en-US');
    const en = renderToStaticMarkup(<ProviderForm provider={provider} onSaved={async () => undefined} />);
    expect(en).toContain('renaming a key never affects the nodes that pin it');
  });
});

describe('NodeForm', () => {
  it('renders a pinned-key select with the node provider keys', () => {
    const node: Node = {
      id: 'n_0000000001',
      name: 'n',
      provider: 'p_0000000001',
      model: 'm1',
      key: 'pk_0000000001',
    };
    const config = {
      providers: [
        {
          id: 'p_0000000001',
          name: 'p',
          type: 'generic',
          chat: 'https://a.example/v1',
          keys: [
            { id: 'pk_0000000001', name: 'account-a', keyPreview: 'sk-...a', enabled: true },
            { id: 'pk_0000000002', name: 'account-b', keyPreview: 'sk-...b', enabled: false },
          ],
        },
      ],
      nodes: [node],
      routes: [],
      entrypoints: [],
    } as unknown as ConfigResponse;
    const html = renderToStaticMarkup(<NodeForm node={node} config={config} onSaved={async () => undefined} />);
    expect(html).toContain('固定 key');
    expect(html).toContain('account-a');
  });
});

describe('RouteForm', () => {
  const config = { providers: [], nodes: [], routes: [], entrypoints: [] } as unknown as ConfigResponse;

  it('defaults retries to 0 and retryCodes to 429,5xx', () => {
    const route: Route = { id: 'r_0000000001', name: 'r', nodes: [] };
    const html = renderToStaticMarkup(<RouteForm route={route} config={config} onSaved={async () => undefined} />);
    expect(html).toContain('retries');
    expect(html).toContain('retryCodes');
    expect(html).toContain('429,5xx');
    expect(html).toContain('value="0"');
  });

  it('shows the stored retry settings of an existing route', () => {
    const route: Route = { id: 'r_0000000001', name: 'r', nodes: [], retries: 2, retryCodes: '429,502' };
    const html = renderToStaticMarkup(<RouteForm route={route} config={config} onSaved={async () => undefined} />);
    expect(html).toContain('429,502');
    expect(html).toContain('value="2"');
  });

  it('renders the route name as an editable display label', () => {
    const route: Route = { id: 'r_0000000001', name: 'strong', nodes: [] };
    const html = renderToStaticMarkup(<RouteForm route={route} config={config} onSaved={async () => undefined} />);
    expect(html).toContain('value="strong"');
    expect(html).toContain('仅用于显示');
  });
});

describe('entrypointBindingInputs', () => {
  const existing: EntrypointRoute[] = [
    { route: 'r_0000000001', probe: { command: 'curl -fsS http://127.0.0.1:9/ready', interval: '10s', timeout: '2s' } },
    { route: 'r_0000000002' },
  ];

  it('keeps the row order and carries the probe each route already has', () => {
    expect(entrypointBindingInputs([{ route: 'r_0000000002' }, { route: 'r_0000000001' }], existing)).toEqual([
      { route: 'r_0000000002' },
      { route: 'r_0000000001', probe: { command: 'curl -fsS http://127.0.0.1:9/ready', interval: '10s', timeout: '2s' } },
    ]);
  });

  it('keeps the probe object identity, so an untouched probe is never rewritten', () => {
    const [binding] = entrypointBindingInputs([{ route: 'r_0000000001' }], existing);

    expect(binding.probe).toBe(existing[0].probe);
  });

  it('drops blank rows and gives a newly added route no probe', () => {
    expect(entrypointBindingInputs([{ route: '' }, { route: 'r_0000000003' }], existing)).toEqual([
      { route: 'r_0000000003' },
    ]);
    expect(entrypointBindingInputs(undefined, existing)).toEqual([]);
  });
});

describe('withBindingProbe', () => {
  const entrypoints: Entrypoint[] = [
    {
      id: 'e_0000000001',
      name: 'all',
      match: ['*'],
      routes: [{ route: 'r_0000000001', probe: { command: 'old' } }, { route: 'r_0000000002' }],
    },
    { id: 'e_0000000002', name: 'other', match: ['gpt-*'], routes: [{ route: 'r_0000000001' }] },
  ];

  it('sets a probe on one binding and leaves every other binding and probe alone', () => {
    const next = withBindingProbe(entrypoints, 'e_0000000001', 'r_0000000002', { command: 'true', interval: '15s' });

    expect(next[0].routes).toEqual([
      { route: 'r_0000000001', probe: { command: 'old' } },
      { route: 'r_0000000002', probe: { command: 'true', interval: '15s' } },
    ]);
    expect(next[1]).toBe(entrypoints[1]);
  });

  it('replaces the probe of the named binding only', () => {
    const next = withBindingProbe(entrypoints, 'e_0000000001', 'r_0000000001', { command: 'new', timeout: '2s' });

    expect(next[0].routes[0]).toEqual({ route: 'r_0000000001', probe: { command: 'new', timeout: '2s' } });
    expect(next[0].routes[1]).toEqual({ route: 'r_0000000002' });
  });

  it('removes the probe field when the probe is cleared', () => {
    const [entrypoint] = withBindingProbe(entrypoints, 'e_0000000001', 'r_0000000001', undefined);

    expect(entrypoint.routes[0]).toEqual({ route: 'r_0000000001' });
    expect('probe' in entrypoint.routes[0]).toBe(false);
  });

  it('returns the same list when the entrypoint or binding is unknown', () => {
    expect(withBindingProbe(entrypoints, 'e_9999999999', 'r_0000000001', { command: 'true' })).toBe(entrypoints);
    expect(withBindingProbe(entrypoints, 'e_0000000001', 'r_9999999999', { command: 'true' })).toBe(entrypoints);
  });
});

describe('probeFromForm', () => {
  it('builds a probe from the filled edge form', () => {
    expect(probeFromForm({ command: ' true ', interval: '15s', timeout: '3s' })).toEqual({
      command: 'true',
      interval: '15s',
      timeout: '3s',
    });
  });

  it('omits blank optional fields so the server applies its defaults', () => {
    expect(probeFromForm({ command: 'true', interval: ' ', timeout: '' })).toEqual({ command: 'true' });
  });

  it('removes the probe when the command is blank', () => {
    expect(probeFromForm({ command: '  ', interval: '15s' })).toBeUndefined();
    expect(probeFromForm(undefined)).toBeUndefined();
  });
});

describe('probe schedule validation', () => {
  it('parses Go durations, including compound and sub-second forms', () => {
    expect(probeDurationMs('30s')).toBe(30_000);
    expect(probeDurationMs('1m30s')).toBe(90_000);
    expect(probeDurationMs('500ms')).toBe(500);
    expect(probeDurationMs(' 2m ')).toBe(120_000);
    expect(probeDurationMs('30')).toBeUndefined();
    expect(probeDurationMs('30 s')).toBeUndefined();
    expect(probeDurationMs('abc')).toBeUndefined();
    expect(probeDurationMs('')).toBeUndefined();
  });

  it('accepts a blank interval and rejects less than one second', () => {
    expect(probeIntervalError(undefined)).toBeUndefined();
    expect(probeIntervalError('30s')).toBeUndefined();
    expect(probeIntervalError('500ms')).toBe('探测间隔不能小于 1s（默认 30s）');
    expect(probeIntervalError('soon')).toBe('探测间隔必须是时长，例如 30s');
  });

  it('checks the timeout against its default and the effective interval', () => {
    expect(probeTimeoutError(undefined, undefined)).toBeUndefined();
    expect(probeTimeoutError('5s', '30s')).toBeUndefined();
    // A blank timeout means the 5s default, so a 2s interval is rejected even
    // though the operator typed no timeout.
    expect(probeTimeoutError('', '2s')).toBe('探测超时必须小于探测间隔（默认 30s / 5s）');
    expect(probeTimeoutError('30s', undefined)).toBe('探测超时必须小于探测间隔（默认 30s / 5s）');
    expect(probeTimeoutError('500ms', '30s')).toBe('探测超时不能小于 1s（默认 5s）');
    expect(probeTimeoutError('nope', '30s')).toBe('探测超时必须是时长，例如 5s');
  });
});

describe('EntrypointForm bindings', () => {
  const config = {
    providers: [],
    nodes: [],
    routes: [
      { id: 'r_0000000001', name: 'primary', nodes: [] },
      { id: 'r_0000000002', name: 'backup', nodes: [] },
    ],
    entrypoints: [],
  } as unknown as ConfigResponse;

  it('renders one ordered row per binding and no probe inputs', () => {
    const entrypoint: Entrypoint = {
      id: 'e_0000000001',
      name: 'all',
      match: ['*'],
      routes: [
        { route: 'r_0000000001', probe: { command: 'curl -fsS http://127.0.0.1:9/ready', interval: '10s', timeout: '2s' } },
        { route: 'r_0000000002' },
      ],
    };
    const html = renderToStaticMarkup(
      <EntrypointForm entrypoint={entrypoint} index={0} config={config} onSaved={async () => undefined} />,
    );
    expect(html.indexOf('primary')).toBeGreaterThan(-1);
    expect(html.indexOf('primary')).toBeLessThan(html.indexOf('backup'));
    expect(html).toContain('添加路由绑定');
    // The probe moved to the topology edge, so the drawer shows neither the
    // command nor its schedule nor the old toggle.
    expect(html).not.toContain('placeholder="curl');
    expect(html).not.toContain('placeholder="30s"');
    expect(html).not.toContain('placeholder="5s"');
    expect(html).not.toContain('添加探测命令（可选）');
    expect(html).not.toContain('移除探测命令');
    expect(html).not.toContain('curl -fsS http://127.0.0.1:9/ready');
  });

  it('translates every binding string', () => {
    const entrypoint: Entrypoint = {
      id: 'e_0000000001',
      name: 'all',
      match: ['*'],
      routes: [{ route: 'r_0000000001', probe: { command: 'true' } }, { route: 'r_0000000002' }],
    };
    setActiveLang('en-US');
    const html = renderToStaticMarkup(
      <EntrypointForm entrypoint={entrypoint} index={0} config={config} onSaved={async () => undefined} />,
    );
    setActiveLang('zh-CN');
    expect(html).toContain('Route bindings');
    expect(html).toContain('Add route binding');
    expect(html).not.toMatch(/[\u4e00-\u9fff]/);
  });
});

describe('entrypoint edge wiring', () => {
  const entrypoint: Entrypoint = {
    id: 'e_0000000001',
    name: 'all',
    match: ['*'],
    routes: [{ route: 'r_0000000001' }, { route: 'r_0000000002' }],
  };

  it('adds a binding when a route is dragged onto an entrypoint', () => {
    const draft: Entrypoint = {
      ...entrypoint,
      routes: [{ route: 'r_0000000001', probe: { command: 'true' } }],
    };

    const next = connectEntrypointEdge([draft], 'entrypoint:e_0000000001', 'route:r_0000000002');

    expect(next[0].routes).toEqual([
      { route: 'r_0000000001', probe: { command: 'true' } },
      { route: 'r_0000000002' },
    ]);
  });

  it('inserts a dragged binding before the first probe-less binding', () => {
    const draft: Entrypoint = { ...entrypoint, routes: [{ route: 'r_0000000001' }] };

    const next = connectEntrypointEdge([draft], 'entrypoint:e_0000000001', 'route:r_0000000002');

    expect(next[0].routes).toEqual([{ route: 'r_0000000002' }, { route: 'r_0000000001' }]);
  });

  it('leaves the list untouched when the route is already bound', () => {
    const entrypoints = [entrypoint];

    expect(connectEntrypointEdge(entrypoints, 'entrypoint:e_0000000001', 'route:r_0000000001')).toBe(entrypoints);
  });

  it('removes only the binding the edge names', () => {
    const next = disconnectEntrypointEdge([entrypoint], 'entrypoint:e_0000000001->route:r_0000000001');

    expect(next[0].routes).toEqual([{ route: 'r_0000000002' }]);
  });

  it('leaves a draft when the last binding is removed', () => {
    const single: Entrypoint = { ...entrypoint, routes: [{ route: 'r_0000000001' }] };

    const next = disconnectEntrypointEdge([single], 'entrypoint:e_0000000001->route:r_0000000001');

    expect(next[0].routes).toEqual([]);
  });

  it('ignores an edge that is not an entrypoint binding', () => {
    const entrypoints = [entrypoint];

    expect(disconnectEntrypointEdge(entrypoints, 'entrypoint:e_0000000001->unrouted')).toBe(entrypoints);
  });
});

describe('draft editing', () => {
  it('starts an entrypoint with one empty binding row and explains the draft', () => {
    const config = { providers: [], nodes: [], routes: [], entrypoints: [] } as unknown as ConfigResponse;
    const html = renderToStaticMarkup(
      <EntrypointForm
        entrypoint={{ id: 'e_0000000001', name: 'draft', match: ['*'], routes: [] }}
        index={0}
        config={config}
        onSaved={async () => undefined}
      />,
    );
    expect(html).toContain('选择路由');
    expect(html).toContain('没有绑定即为草稿');
  });

  it('offers a clearable provider select for a node', () => {
    const config = { providers: [], nodes: [], routes: [], entrypoints: [] } as unknown as ConfigResponse;
    const node: Node = { id: 'n_0000000001', name: 'draft', provider: '', model: '' };
    const html = renderToStaticMarkup(<NodeForm node={node} config={config} onSaved={async () => undefined} />);
    expect(html).toContain('留空即为草稿');
  });
});

describe('EdgeForm', () => {
  const config = {
    providers: [{ id: 'p_0000000001', name: 'p', type: 'generic', keys: [] }],
    nodes: [{ id: 'n_0000000001', name: 'n', provider: 'p_0000000001', model: 'm1' }],
    routes: [
      { id: 'r_0000000001', name: 'primary', nodes: [] },
      { id: 'r_0000000002', name: 'backup', nodes: [] },
    ],
    entrypoints: [{ id: 'e_0000000001', name: 'e', match: ['*'], routes: [{ route: 'r_0000000001' }] }],
  } as unknown as ConfigResponse;

  it('can disconnect an entrypoint from its route', () => {
    const html = renderToStaticMarkup(
      <EdgeForm id="entrypoint:e_0000000001->route:r_0000000001" config={config} onSaved={async () => undefined} />,
    );
    expect(html.replace(/\s+/g, '')).toContain('断开');
  });

  it('shows the route name and the probe editor of the binding it names', () => {
    const probed = {
      ...config,
      entrypoints: [
        {
          id: 'e_0000000001',
          name: 'e',
          match: ['*'],
          routes: [
            { route: 'r_0000000001', probe: { command: 'curl -fsS http://127.0.0.1:9/ready', interval: '15s', timeout: '3s' } },
            { route: 'r_0000000002' },
          ],
        },
      ],
    } as unknown as ConfigResponse;
    const html = renderToStaticMarkup(
      <EdgeForm id="entrypoint:e_0000000001->route:r_0000000001" config={probed} onSaved={async () => undefined} />,
    );
    expect(html).toContain('primary');
    expect(html).toContain('探测命令');
    expect(html).toContain('<textarea');
    expect(html).toContain('curl -fsS http://127.0.0.1:9/ready');
    expect(html).toContain('value="15s"');
    expect(html).toContain('value="3s"');
  });

  it('can disconnect a node from its provider', () => {
    const html = renderToStaticMarkup(
      <EdgeForm id="node:n_0000000001|provider:p_0000000001" config={config} onSaved={async () => undefined} />,
    );
    expect(html.replace(/\s+/g, '')).toContain('断开');
  });
});
