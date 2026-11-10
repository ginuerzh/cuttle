import { describe, expect, it } from 'vitest';
import { buildGraph } from './build';
import type { ConfigResponse } from '../api/types';

const cfg: Pick<ConfigResponse, 'entrypoints' | 'routes' | 'providers' | 'nodes'> = {
  entrypoints: [
    { id: 'e_0000000001', name: 'strong', match: ['claude-opus*', 'gpt-5.5*'], route: 'r_0000000001' },
    { id: 'e_0000000002', name: 'fast', match: ['gpt-4.5*'], route: 'r_0000000002' },
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

  it('adds an unrouted node when an entrypoint route is missing', () => {
    const missing = {
      ...cfg,
      entrypoints: [
        ...cfg.entrypoints,
        { id: 'e_0000000003', name: 'broken', match: ['mystery*'], route: 'r_0000000009' },
      ],
    };
    const { nodes, edges } = buildGraph(missing);
    expect(nodes.some((n) => n.id === 'unrouted')).toBe(true);
    expect(edges.some((e) => e.id === 'entrypoint:e_0000000003->unrouted')).toBe(true);
  });
});
