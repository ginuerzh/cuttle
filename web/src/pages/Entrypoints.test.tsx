import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import { setActiveLang } from '../i18n';
import type { ConfigResponse, EntrypointHealth } from '../api/types';
import { EntrypointsPage } from './Entrypoints';

setActiveLang('zh-CN');

const config = {
  providers: [],
  nodes: [],
  routes: [
    { id: 'r_0000000001', name: 'primary', nodes: [] },
    { id: 'r_0000000002', name: 'backup', nodes: [] },
  ],
  entrypoints: [
    {
      id: 'e_0000000001',
      name: 'all',
      match: ['*'],
      routes: [{ route: 'r_0000000001' }, { route: 'r_0000000002' }],
    },
  ],
} as unknown as ConfigResponse;

const health: EntrypointHealth[] = [
  {
    entrypointId: 'e_0000000001',
    entrypointName: 'all',
    active: { routeId: 'r_0000000001', fallback: true },
    bindings: [
      { index: 0, routeId: 'r_0000000001', routeName: 'primary', eligible: false },
      { index: 1, routeId: 'r_0000000002', routeName: 'backup', eligible: true },
    ],
  },
];

describe('EntrypointsPage', () => {
  it('labels an unconnected entrypoint', () => {
    const draft = {
      ...config,
      entrypoints: [{ id: 'e_0000000002', name: 'draft', match: ['*'], routes: [] }],
    } as unknown as ConfigResponse;
    const html = renderToStaticMarkup(<EntrypointsPage config={draft} onReload={async () => undefined} />);
    expect(html).toContain('未连接');
  });

  it('lists every binding with its route name and eligibility', () => {
    const html = renderToStaticMarkup(
      <EntrypointsPage config={config} onReload={async () => undefined} health={health} />,
    );
    expect(html).toContain('primary');
    expect(html).toContain('backup');
    expect(html).toContain('未通过');
    expect(html).toContain('可用');
    expect(html).toContain('回退');
  });

  it('lists the bindings by name before health arrives', () => {
    const html = renderToStaticMarkup(<EntrypointsPage config={config} onReload={async () => undefined} />);
    expect(html).toContain('primary');
    expect(html).toContain('backup');
    expect(html).not.toContain('未通过');
  });

  it('renders the binding states in English', () => {
    setActiveLang('en-US');
    const html = renderToStaticMarkup(
      <EntrypointsPage config={config} onReload={async () => undefined} health={health} />,
    );
    setActiveLang('zh-CN');
    expect(html).toContain('Available');
    expect(html).toContain('Unavailable');
    expect(html).toContain('Fallback');
    expect(html).not.toMatch(/[\u4e00-\u9fff]/);
  });
});
