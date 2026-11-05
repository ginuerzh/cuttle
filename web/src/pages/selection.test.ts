import { describe, expect, it } from 'vitest';
import { buildGraph } from '../graph/build';
import type { ConfigResponse, ErrorRow } from '../api/types';
import { entrypointFromNodeId, errorMatchesNodeId } from './selection';

const cfg: Pick<ConfigResponse, 'entrypoints' | 'routes' | 'providers' | 'nodes'> = {
  entrypoints: [
    { name: 'strong', match: ['claude-opus*'], route: 'strong' },
    { name: 'fast', match: ['gpt-4.5*'], route: 'fast' },
  ],
  providers: [{ name: 'p1', type: 'deepseek', chat: 'https://a/v1', keysSet: 1, keyPreviews: [] }],
  nodes: [{ name: 'p1', provider: 'p1', model: 'm1' }],
  routes: [
    { name: 'strong', nodes: ['p1'] },
    { name: 'fast', nodes: ['p1'] },
  ],
};

describe('entrypointFromNodeId', () => {
  it('resolves the entrypoint node id drawn on the canvas', () => {
    const { nodes } = buildGraph(cfg);
    const node = nodes.find((n) => n.id === 'entrypoint:strong');
    expect(node).toBeTruthy();

    const found = entrypointFromNodeId(cfg.entrypoints, node!.id);
    expect(found?.index).toBe(0);
    expect(found?.entrypoint.route).toBe('strong');
  });

  it('returns null for an unknown or non-entrypoint id', () => {
    expect(entrypointFromNodeId(cfg.entrypoints, 'entrypoint:missing')).toBeNull();
    expect(entrypointFromNodeId(cfg.entrypoints, 'route:strong')).toBeNull();
  });
});

describe('errorMatchesNodeId', () => {
  it('matches entrypoint errors by entrypoint name', () => {
    const err = {
      requestId: '1',
      ts: '2026-11-05T00:00:00Z',
      entrypoint: 'strong',
      route: 'strong',
      node: 'n',
      provider: 'p',
      status: 500,
      message: 'boom',
    } as ErrorRow;
    expect(errorMatchesNodeId('entrypoint:strong', err)).toBe(true);
    expect(errorMatchesNodeId('entrypoint:fast', err)).toBe(false);
    expect(errorMatchesNodeId('route:strong', err)).toBe(true);
  });
});
