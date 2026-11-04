import { describe, expect, it } from 'vitest';
import { buildGraph } from './build';
import type { ConfigResponse } from '../api/types';

const cfg: Pick<ConfigResponse, 'aliases' | 'routes' | 'providers' | 'nodes'> = {
  aliases: [
    { match: 'claude-opus*', model: 'strong' },
    { match: 'gpt-5.5*', model: 'strong' },
    { match: 'gpt-4.5*', model: 'fast' },
  ],
  providers: [
    { name: 'p1', type: 'deepseek', chat: 'https://a/v1', keysSet: 1, keyPreviews: [] },
    { name: 'p2', type: 'generic', anthropic: 'https://b', keysSet: 2, keyPreviews: [] },
  ],
  nodes: [
    { name: 'p1', provider: 'p1', models: { fast: 'm1' } },
    { name: 'p2', provider: 'p2' },
  ],
  routes: [
    { name: 'strong', nodes: ['p1', 'p2'], strategy: 'round', match: { model: 'strong' } },
    { name: 'fast', nodes: ['p1'], strategy: 'fifo', match: { model: 'fast' } },
  ],
};

describe('buildGraph', () => {
  it('links aliases to logical-model routes', () => {
    const { edges } = buildGraph(cfg);
    expect(edges.some((e) => e.id === 'alias:claude-opus*->route:strong')).toBe(true);
    expect(edges.some((e) => e.id === 'alias:gpt-5.5*->route:strong')).toBe(true);
    expect(edges.some((e) => e.id === 'alias:gpt-4.5*->route:fast')).toBe(true);
  });

  it('emits route -> node and node -> provider edges', () => {
    const { edges } = buildGraph(cfg);
    expect(edges.some((e) => e.id === 'route:strong|node:p1')).toBe(true);
    expect(edges.some((e) => e.id === 'route:fast|node:p1')).toBe(true);
    expect(edges.some((e) => e.id === 'node:p1|provider:p1')).toBe(true);
    expect(edges.some((e) => e.id === 'node:p2|provider:p2')).toBe(true);
  });

  it('falls back to a catch-all route for unknown logical models', () => {
    const withCatchAll = {
      ...cfg,
      aliases: [...cfg.aliases, { match: 'mystery*', model: 'other' }],
      routes: [...cfg.routes, { name: 'default', nodes: ['p1'], match: {} }],
    };
    const { edges } = buildGraph(withCatchAll);
    expect(edges.some((e) => e.id === 'alias:mystery*->route:default')).toBe(true);
  });

  it('adds an unrouted node when nothing matches', () => {
    const noRoute = {
      ...cfg,
      aliases: [{ match: 'mystery*', model: 'other' }],
    };
    const { nodes, edges } = buildGraph(noRoute);
    expect(nodes.some((n) => n.id === 'unrouted')).toBe(true);
    expect(edges.some((e) => e.id === 'alias:mystery*->unrouted')).toBe(true);
  });
});
