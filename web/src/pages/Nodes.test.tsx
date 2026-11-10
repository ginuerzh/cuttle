import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import { setActiveLang } from '../i18n';
import type { ConfigResponse } from '../api/types';
import { NodesPage } from './Nodes';

setActiveLang('zh-CN');

describe('NodesPage', () => {
  it('shows the pinned key name on the node card', () => {
    const config = {
      providers: [],
      nodes: [{ name: 'n', provider: 'p', model: 'm1', key: 'account-a' }],
      routes: [],
      entrypoints: [],
    } as unknown as ConfigResponse;
    const html = renderToStaticMarkup(<NodesPage config={config} onReload={async () => undefined} />);
    expect(html).toContain('account-a');
  });
});
