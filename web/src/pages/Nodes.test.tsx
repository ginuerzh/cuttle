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
});
