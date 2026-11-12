import { describe, expect, it } from 'vitest';
import { buildGraph, ROUTE_PALETTE, routeColor } from './build';
import { setActiveLang } from '../i18n';
import type { ConfigResponse } from '../api/types';

const cfg: Pick<ConfigResponse, 'entrypoints' | 'routes' | 'providers' | 'nodes'> = {
  entrypoints: [
    {
      id: 'e_0000000001',
      name: 'strong',
      match: ['claude-opus*', 'gpt-5.5*'],
      routes: [{ route: 'r_0000000001' }],
    },
    { id: 'e_0000000002', name: 'fast', match: ['gpt-4.5*'], routes: [{ route: 'r_0000000002' }] },
  ],
  providers: [
    { id: 'p_0000000001', name: 'p1', type: 'deepseek', chat: 'https://a/v1', keys: [] },
    { id: 'p_0000000002', name: 'p2', type: 'generic', anthropic: 'https://b', keys: [] },
  ],
  nodes: [
    { id: 'n_0000000001', name: 'p1', provider: 'p_0000000001', model: 'm1' },
    { id: 'n_0000000002', name: 'p2', provider: 'p_0000000002', model: 'm2' },
  ],
  routes: [
    { id: 'r_0000000001', name: 'strong', nodes: ['n_0000000001', 'n_0000000002'], strategy: 'round' },
    { id: 'r_0000000002', name: 'fast', nodes: ['n_0000000001'], strategy: 'fifo' },
  ],
};

describe('buildGraph', () => {
  it('keys graph nodes and edges by entity id', () => {
    const graph = buildGraph({ ...cfg, providers: [{ id: 'p_3k9x2m4q7a', name: 'DeepSeek', type: 'deepseek', keys: [] }] });
    expect(graph.nodes.some((n) => n.id === 'provider:p_3k9x2m4q7a')).toBe(true);
  });

  it('links entrypoints to their routes', () => {
    const { nodes, edges } = buildGraph(cfg);
    expect(nodes.some((n) => n.id === 'entrypoint:e_0000000001')).toBe(true);
    expect(edges.some((e) => e.id === 'entrypoint:e_0000000001->route:r_0000000001')).toBe(true);
    expect(edges.some((e) => e.id === 'entrypoint:e_0000000002->route:r_0000000002')).toBe(true);
    expect(edges.some((e) => e.id.startsWith('alias:'))).toBe(false);
  });

  it('draws one edge per binding', () => {
    const multi: Pick<ConfigResponse, 'entrypoints' | 'routes' | 'providers' | 'nodes'> = {
      ...cfg,
      entrypoints: [
        {
          id: 'e_0000000001',
          name: 'strong',
          match: ['claude-opus*'],
          routes: [{ route: 'r_0000000001' }, { route: 'r_0000000002' }],
        },
      ],
    };
    const { nodes, edges } = buildGraph(multi);
    const outgoing = edges.filter((e) => e.source === 'entrypoint:e_0000000001');
    expect(outgoing.map((e) => e.id).sort()).toEqual([
      'entrypoint:e_0000000001->route:r_0000000001',
      'entrypoint:e_0000000001->route:r_0000000002',
    ]);
    expect(nodes.find((n) => n.id === 'entrypoint:e_0000000001')?.data.unconnected).toBe(false);
    expect(nodes.find((n) => n.id === 'entrypoint:e_0000000001')?.data.sub).toContain('strong');
    expect(nodes.find((n) => n.id === 'entrypoint:e_0000000001')?.data.sub).toContain('fast');
  });

  it('keeps a binding with a probe on the same edge id', () => {
    const probed: Pick<ConfigResponse, 'entrypoints' | 'routes' | 'providers' | 'nodes'> = {
      ...cfg,
      entrypoints: [
        {
          id: 'e_0000000001',
          name: 'strong',
          match: ['*'],
          routes: [
            { route: 'r_0000000001', probe: { command: 'true', interval: '30s', timeout: '5s' } },
            { route: 'r_0000000002' },
          ],
        },
      ],
    };
    const { edges } = buildGraph(probed);
    expect(edges.some((e) => e.id === 'entrypoint:e_0000000001->route:r_0000000001')).toBe(true);
    expect(edges.some((e) => e.id === 'entrypoint:e_0000000001->route:r_0000000002')).toBe(true);
  });

  it('emits route -> node and node -> provider edges', () => {
    const { edges } = buildGraph(cfg);
    expect(edges.some((e) => e.id === 'route:r_0000000001|node:n_0000000001')).toBe(true);
    expect(edges.some((e) => e.id === 'route:r_0000000002|node:n_0000000001')).toBe(true);
    expect(edges.some((e) => e.id === 'node:n_0000000001|provider:p_0000000001')).toBe(true);
    expect(edges.some((e) => e.id === 'node:n_0000000002|provider:p_0000000002')).toBe(true);
  });

  it('keeps the display names as labels', () => {
    const { nodes } = buildGraph(cfg);
    expect(nodes.find((n) => n.id === 'provider:p_0000000001')?.data.label).toBe('p1');
    expect(nodes.find((n) => n.id === 'node:n_0000000001')?.data.label).toBe('p1');
    expect(nodes.find((n) => n.id === 'route:r_0000000001')?.data.label).toBe('strong');
    expect(nodes.find((n) => n.id === 'entrypoint:e_0000000001')?.data.label).toBe('strong');
  });

  it('marks a providerless node as a draft without a provider edge', () => {
    setActiveLang('zh-CN');
    const draft = {
      ...cfg,
      nodes: [...cfg.nodes, { id: 'n_0000000003', name: 'draft', provider: '', model: '' }],
    };
    const { nodes, edges } = buildGraph(draft);
    const node = nodes.find((n) => n.id === 'node:n_0000000003');
    expect(node?.data.unconnected).toBe(true);
    expect(node?.data.sub).toContain('未连接');
    expect(edges.some((e) => e.id.startsWith('node:n_0000000003|'))).toBe(false);
  });

  it('links a draft entrypoint with no bindings to unrouted', () => {
    setActiveLang('zh-CN');
    const draft = {
      ...cfg,
      entrypoints: [...cfg.entrypoints, { id: 'e_0000000003', name: 'draft', match: ['mystery*'], routes: [] }],
    };
    const { nodes, edges } = buildGraph(draft);
    expect(nodes.find((n) => n.id === 'entrypoint:e_0000000003')?.data.unconnected).toBe(true);
    expect(nodes.find((n) => n.id === 'entrypoint:e_0000000003')?.data.sub).toContain('未连接');
    const edge = edges.find((e) => e.id === 'entrypoint:e_0000000003->unrouted');
    expect(edge?.data?.color).toBe('#d97706');
    expect(edge?.data?.draft).toBe(true);
    expect(edges.some((e) => e.id.startsWith('entrypoint:e_0000000003->route:'))).toBe(false);
  });
});

describe('routeColor', () => {
  const hue = (hex: string): number => {
    const [r, g, b] = [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16) / 255);
    const max = Math.max(r, g, b);
    const min = Math.min(r, g, b);
    const d = max - min;
    if (!d) return 0;
    const h = max === r ? ((g - b) / d) % 6 : max === g ? (b - r) / d + 2 : (r - g) / d + 4;
    return (h * 60 + 360) % 360;
  };

  it('is stable for the same route name', () => {
    expect(routeColor('dadax')).toBe(routeColor('dadax'));
    expect(ROUTE_PALETTE).toContain(routeColor('dadax'));
  });

  it('keeps the palette free of pink and fuchsia, which read as a fault', () => {
    for (const color of ROUTE_PALETTE) {
      const h = hue(color);
      expect(h < 285 || h > 350).toBe(true);
    }
  });

  it('never colours a route with the health colours', () => {
    for (const color of ROUTE_PALETTE) {
      expect(['#dc2626', '#d97706', '#16a34a', '#cbd5e1']).not.toContain(color);
    }
  });
});
