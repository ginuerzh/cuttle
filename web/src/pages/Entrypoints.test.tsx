import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import { setActiveLang } from '../i18n';
import type { ConfigResponse } from '../api/types';
import { EntrypointsPage } from './Entrypoints';

setActiveLang('zh-CN');

describe('EntrypointsPage', () => {
  it('labels an unconnected entrypoint', () => {
    const config = {
      providers: [],
      nodes: [],
      routes: [],
      entrypoints: [{ id: 'e_0000000001', name: 'draft', match: ['*'], route: '' }],
    } as unknown as ConfigResponse;
    const html = renderToStaticMarkup(<EntrypointsPage config={config} onReload={async () => undefined} />);
    expect(html).toContain('未连接');
  });
});
