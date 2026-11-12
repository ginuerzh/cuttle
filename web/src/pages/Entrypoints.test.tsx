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
    { id: 'r_0000000003', name: 'manual', nodes: [] },
  ],
  entrypoints: [
    {
      id: 'e_0000000001',
      name: 'all',
      match: ['*'],
      routes: [
        { route: 'r_0000000001', probe: { command: 'true', interval: '30s', timeout: '5s' } },
        { route: 'r_0000000002', probe: { command: 'true', interval: '30s', timeout: '5s' } },
        { route: 'r_0000000003' },
      ],
    },
  ],
} as unknown as ConfigResponse;

// The server reports the first eligible binding in priority order as active,
// and sets `fallback` only when at least one binding has a probe and none of the
// probed bindings passed. The row that serves then is the first probe-less
// binding, so it stays eligible.
const healthy: EntrypointHealth[] = [
  {
    entrypointId: 'e_0000000001',
    entrypointName: 'all',
    active: { routeId: 'r_0000000001', fallback: false },
    bindings: [
      { index: 0, routeId: 'r_0000000001', routeName: 'primary', eligible: true },
      { index: 1, routeId: 'r_0000000002', routeName: 'backup', eligible: false },
      { index: 2, routeId: 'r_0000000003', routeName: 'manual', eligible: true },
    ],
  },
];

const probeLessFallback: EntrypointHealth[] = [
  {
    entrypointId: 'e_0000000001',
    entrypointName: 'all',
    active: { routeId: 'r_0000000003', fallback: true },
    bindings: [
      { index: 0, routeId: 'r_0000000001', routeName: 'primary', eligible: false },
      { index: 1, routeId: 'r_0000000002', routeName: 'backup', eligible: false },
      { index: 2, routeId: 'r_0000000003', routeName: 'manual', eligible: true },
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
      <EntrypointsPage config={config} onReload={async () => undefined} health={healthy} />,
    );
    expect(html).toContain('primary');
    expect(html).toContain('backup');
    expect(html).toContain('未通过');
    expect(html).toContain('可用');
    expect(html).not.toContain('回退');
  });

  it('marks the binding that serves next with a dot', () => {
    const html = renderToStaticMarkup(
      <EntrypointsPage config={config} onReload={async () => undefined} health={healthy} />,
    );
    expect(html).toContain('● primary');
    expect(html).not.toContain('● backup');
  });

  it('marks the probe-less fallback binding when no probe passed', () => {
    const html = renderToStaticMarkup(
      <EntrypointsPage config={config} onReload={async () => undefined} health={probeLessFallback} />,
    );
    expect(html).toContain('未通过');
    expect(html).toContain('回退');
    expect(html).toContain('可用');
    expect(html).toContain('● manual');
    expect(html).not.toContain('● primary');
  });

  it('lists the bindings by name before health arrives', () => {
    const html = renderToStaticMarkup(<EntrypointsPage config={config} onReload={async () => undefined} />);
    expect(html).toContain('primary');
    expect(html).toContain('backup');
    expect(html).not.toContain('未通过');
    expect(html).not.toContain('●');
  });

  it('renders the binding states in English', () => {
    setActiveLang('en-US');
    const healthyHtml = renderToStaticMarkup(
      <EntrypointsPage config={config} onReload={async () => undefined} health={healthy} />,
    );
    const fallbackHtml = renderToStaticMarkup(
      <EntrypointsPage config={config} onReload={async () => undefined} health={probeLessFallback} />,
    );
    setActiveLang('zh-CN');
    expect(healthyHtml).toContain('Available');
    expect(healthyHtml).toContain('Unavailable');
    expect(healthyHtml).toContain('● primary');
    expect(healthyHtml).not.toContain('Fallback');
    expect(fallbackHtml).toContain('Fallback');
    expect(fallbackHtml).toContain('● manual');
    expect(healthyHtml + fallbackHtml).not.toMatch(/[\u4e00-\u9fff]/);
  });
});
