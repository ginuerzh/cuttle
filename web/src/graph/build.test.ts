import { describe, expect, it } from 'vitest';
import { buildGraph } from './build';
import type { ConfigResponse } from '../api/types';

const cfg: Pick<ConfigResponse, 'entrypoints' | 'routes' | 'providers' | 'nodes'> = {
  entrypoints: [
    { name: 'strong', match: ['claude-opus*', 'gpt-5.5*'], route: 'strong' },
    { name: 'fast', match: ['gpt-4.5*'], route: 'fast' },
  ],
  providers: [
    { name: 'p1', type: 'deepseek', chat: 'https://a/v1', keysSet: 1, keyPreviews: [] },
    { name: 'p2', type: 'generic', anthropic: 'https://b', keysSet: 2, keyPreviews: [] },
  ],
  nodes: [
    { name: 'p1', provider: 'p1', model: 'm1' },
    { name: 'p2', provider: 'p2', model: 'm2' },
  ],
  routes: [
    { name: 'strong', nodes: ['p1', 'p2'], strategy: 'round' },
    { name: 'fast', nodes: ['p1'], strategy: 'fifo' },
  ],
};

describe('buildGraph', () => {
  it('links entrypoints to their routes', () => {
    const { edges } = buildGraph(cfg);
    expect(edges.some((e) => e.id === 'entrypoint:strong->route:strong')).toBe(true);
    expect(edges.some((e) => e.id === 'entrypoint:fast->route:fast')).toBe(true);
    expect(edges.some((e) => e.id.startsWith('alias:'))).toBe(false);
  });

  it('emits route -> node and node -> provider edges', () => {
    const { edges } = buildGraph(cfg);
    expect(edges.some((e) => e.id === 'route:strong|node:p1')).toBe(true);
    expect(edges.some((e) => e.id === 'route:fast|node:p1')).toBe(true);
    expect(edges.some((e) => e.id === 'node:p1|provider:p1')).toBe(true);
    expect(edges.some((e) => e.id === 'node:p2|provider:p2')).toBe(true);
  });

  it('adds an unrouted node when an entrypoint route is missing', () => {
    const missing = {
      ...cfg,
      entrypoints: [...cfg.entrypoints, { name: 'broken', match: ['mystery*'], route: 'missing' }],
    };
    const { nodes, edges } = buildGraph(missing);
    expect(nodes.some((n) => n.id === 'unrouted')).toBe(true);
    expect(edges.some((e) => e.id === 'entrypoint:broken->unrouted')).toBe(true);
  });
});
