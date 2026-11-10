import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import { setActiveLang } from '../i18n';
import type { ConfigResponse } from '../api/types';
import { NodesPage } from './Nodes';

setActiveLang('zh-CN');

describe('NodesPage', () => {
  it('shows the pinned key name on the node card', () => {
    const config = {
      providers: [
        {
          id: 'p_0000000001',
          name: 'p',
          type: 'generic',
          chat: 'https://a.example/v1',
          keys: [{ id: 'pk_0000000001', name: 'account-a', keyPreview: 'sk-...a', enabled: true }],
        },
      ],
      nodes: [{ id: 'n_0000000001', name: 'n', provider: 'p_0000000001', model: 'm1', key: 'pk_0000000001' }],
      routes: [],
      entrypoints: [],
    } as unknown as ConfigResponse;
    const html = renderToStaticMarkup(<NodesPage config={config} onReload={async () => undefined} />);
    expect(html).toContain('account-a');
  });

  it('resolves the provider and key names and falls back to the raw ids', () => {
    const config = {
      providers: [
        {
          id: 'p_0000000001',
          name: 'DeepSeek 官方',
          type: 'deepseek',
          keys: [{ id: 'pk_0000000001', name: 'account-a', keyPreview: 'sk-...a', enabled: true }],
        },
      ],
      nodes: [
        { id: 'n_0000000001', name: 'ds-fast', provider: 'p_0000000001', model: 'm1', key: 'pk_0000000001' },
        { id: 'n_0000000002', name: 'gone', provider: 'p_deleted0001', model: 'm2', key: 'pk_deleted001' },
      ],
      routes: [],
      entrypoints: [],
    } as unknown as ConfigResponse;
    const html = renderToStaticMarkup(<NodesPage config={config} onReload={async () => undefined} />);

    expect(html).toContain('DeepSeek 官方');
    expect(html).toContain('account-a');
    expect(html).toContain('p_deleted0001');
    expect(html).toContain('pk_deleted001');
  });
});
