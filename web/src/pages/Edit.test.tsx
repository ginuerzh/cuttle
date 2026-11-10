import { renderToStaticMarkup } from 'react-dom/server';
import { beforeEach, describe, expect, it } from 'vitest';
import { AddButtons, NodeForm, ProviderForm } from './Edit';
import { setActiveLang } from '../i18n';
import type { ConfigResponse, Node, Provider } from '../api/types';

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
      name: 'p',
      type: 'generic',
      chat: 'https://a.example/v1',
      keys: [
        { name: 'account-a', keyPreview: 'sk-...a', enabled: true },
        { name: 'account-b', keyPreview: 'sk-...b', enabled: false },
      ],
    };
    const html = renderToStaticMarkup(<ProviderForm provider={provider} onSaved={async () => undefined} />);
    expect(html).toContain('account-a');
    expect(html).toContain('sk-...a');
    expect(html).toContain('account-b');
    expect(html).toContain('sk-...b');
  });
});

describe('NodeForm', () => {
  it('renders a pinned-key select with the node provider keys', () => {
    const node: Node = { name: 'n', provider: 'p', model: 'm1', key: 'account-a' };
    const config = {
      providers: [
        {
          name: 'p',
          type: 'generic',
          chat: 'https://a.example/v1',
          keys: [
            { name: 'account-a', keyPreview: 'sk-...a', enabled: true },
            { name: 'account-b', keyPreview: 'sk-...b', enabled: false },
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
