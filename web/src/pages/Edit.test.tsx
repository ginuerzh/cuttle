import { renderToStaticMarkup } from 'react-dom/server';
import { beforeEach, describe, expect, it } from 'vitest';
import { AddButtons, EdgeForm, EntrypointForm, entrypointBindingInputs, NodeForm, ProviderForm, RouteForm } from './Edit';
import { setActiveLang } from '../i18n';
import type { ConfigResponse, Entrypoint, Node, Provider, Route } from '../api/types';

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
  it('keeps the row order and maps a filled probe', () => {
    expect(
      entrypointBindingInputs([
        { route: 'r_0000000002', probe: { command: 'curl -fsS http://127.0.0.1:9/ready', interval: '10s', timeout: '2s' } },
        { route: 'r_0000000001' },
      ]),
    ).toEqual([
      { route: 'r_0000000002', probe: { command: 'curl -fsS http://127.0.0.1:9/ready', interval: '10s', timeout: '2s' } },
      { route: 'r_0000000001' },
    ]);
  });

  it('drops blank rows and a probe without a command', () => {
    expect(entrypointBindingInputs([{ route: '' }, { route: 'r_0000000001', probe: { command: '  ' } }])).toEqual([
      { route: 'r_0000000001' },
    ]);
    expect(entrypointBindingInputs(undefined)).toEqual([]);
  });

  it('omits blank optional probe fields so the server applies its defaults', () => {
    expect(entrypointBindingInputs([{ route: 'r_0000000001', probe: { command: ' true ', interval: '', timeout: '' } }])).toEqual([
      { route: 'r_0000000001', probe: { command: 'true' } },
    ]);
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

  it('renders one ordered row per binding with its probe command', () => {
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
    expect(html).toContain('curl -fsS http://127.0.0.1:9/ready');
    expect(html).toContain('value="10s"');
    expect(html).toContain('value="2s"');
    expect(html).toContain('添加路由绑定');
    expect(html).toContain('添加探测命令（可选）');
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
    expect(html).toContain('Probe command');
    expect(html).toContain('Remove probe command');
    expect(html).not.toMatch(/[\u4e00-\u9fff]/);
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
    routes: [{ id: 'r_0000000001', name: 'r', nodes: [] }],
    entrypoints: [{ id: 'e_0000000001', name: 'e', match: ['*'], routes: [{ route: 'r_0000000001' }] }],
  } as unknown as ConfigResponse;

  it('can disconnect an entrypoint from its route', () => {
    const html = renderToStaticMarkup(
      <EdgeForm id="entrypoint:e_0000000001->route:r_0000000001" config={config} onSaved={async () => undefined} />,
    );
    expect(html.replace(/\s+/g, '')).toContain('断开');
  });

  it('can disconnect a node from its provider', () => {
    const html = renderToStaticMarkup(
      <EdgeForm id="node:n_0000000001|provider:p_0000000001" config={config} onSaved={async () => undefined} />,
    );
    expect(html.replace(/\s+/g, '')).toContain('断开');
  });
});
