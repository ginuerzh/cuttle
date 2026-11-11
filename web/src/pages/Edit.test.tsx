import { renderToStaticMarkup } from 'react-dom/server';
import { beforeEach, describe, expect, it } from 'vitest';
import { AddButtons, EdgeForm, EntrypointForm, NodeForm, ProviderForm, RouteForm } from './Edit';
import { setActiveLang } from '../i18n';
import type { ConfigResponse, Node, Provider, Route } from '../api/types';

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

describe('draft editing', () => {
  it('offers a clearable route select and no required marker for an entrypoint', () => {
    const config = { providers: [], nodes: [], routes: [], entrypoints: [] } as unknown as ConfigResponse;
    const html = renderToStaticMarkup(
      <EntrypointForm
        entrypoint={{ id: 'e_0000000001', name: 'draft', match: ['*'], route: '' }}
        index={0}
        config={config}
        onSaved={async () => undefined}
      />,
    );
    expect(html).toContain('留空即为草稿');
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
    entrypoints: [{ id: 'e_0000000001', name: 'e', match: ['*'], route: 'r_0000000001' }],
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
