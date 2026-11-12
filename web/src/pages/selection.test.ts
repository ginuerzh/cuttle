import { describe, expect, it } from 'vitest';
import { buildGraph } from '../graph/build';
import type { ConfigResponse, ErrorRow } from '../api/types';
import { entrypointFromNodeId, errorMatchesNodeId, splitEdgeId } from './selection';

const cfg: Pick<ConfigResponse, 'entrypoints' | 'routes' | 'providers' | 'nodes'> = {
  entrypoints: [
    { id: 'e_0000000001', name: 'strong', match: ['claude-opus*'], routes: [{ route: 'r_0000000001' }] },
    { id: 'e_0000000002', name: 'fast', match: ['gpt-4.5*'], routes: [{ route: 'r_0000000002' }] },
  ],
  providers: [{ id: 'p_0000000001', name: 'p1', type: 'deepseek', chat: 'https://a/v1', keys: [] }],
  nodes: [{ id: 'n_0000000001', name: 'p1', provider: 'p_0000000001', model: 'm1' }],
  routes: [
    { id: 'r_0000000001', name: 'strong', nodes: ['n_0000000001'] },
    { id: 'r_0000000002', name: 'fast', nodes: ['n_0000000001'] },
  ],
};

describe('entrypointFromNodeId', () => {
  it('resolves the entrypoint node id drawn on the canvas', () => {
    const { nodes } = buildGraph(cfg);
    const node = nodes.find((n) => n.id === 'entrypoint:e_0000000001');
    expect(node).toBeTruthy();

    const found = entrypointFromNodeId(cfg.entrypoints, node!.id);
    expect(found?.index).toBe(0);
    expect(found?.entrypoint.routes.map((b) => b.route)).toEqual(['r_0000000001']);
  });

  it('returns null for an unknown or non-entrypoint id', () => {
    expect(entrypointFromNodeId(cfg.entrypoints, 'entrypoint:missing')).toBeNull();
    expect(entrypointFromNodeId(cfg.entrypoints, 'route:strong')).toBeNull();
  });
});

describe('errorMatchesNodeId', () => {
  it('matches entrypoint errors by the recorded entity id', () => {
    const err = {
      requestId: '1',
      ts: '2026-11-05T00:00:00Z',
      entrypoint: 'e_0000000001',
      route: 'r_0000000001',
      node: 'n_0000000001',
      provider: 'p_0000000001',
      status: 500,
      message: 'boom',
    } as ErrorRow;
    expect(errorMatchesNodeId('entrypoint:e_0000000001', err)).toBe(true);
    expect(errorMatchesNodeId('entrypoint:e_0000000002', err)).toBe(false);
    expect(errorMatchesNodeId('route:r_0000000001', err)).toBe(true);
  });
});

describe('splitEdgeId', () => {
  it('splits an entrypoint binding edge drawn with an arrow', () => {
    expect(splitEdgeId('entrypoint:e_0000000001->route:r_0000000001')).toEqual({
      source: 'entrypoint:e_0000000001',
      target: 'route:r_0000000001',
    });
  });

  it('splits a pipe edge', () => {
    expect(splitEdgeId('route:r_0000000001|node:n_0000000001')).toEqual({
      source: 'route:r_0000000001',
      target: 'node:n_0000000001',
    });
  });

  it('returns the id as the source when there is no separator', () => {
    expect(splitEdgeId('unrouted')).toEqual({ source: 'unrouted', target: '' });
  });
});
