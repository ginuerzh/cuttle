import { describe, expect, it } from 'vitest';
import type { RouteHealth } from '../api/types';
import { nodeRouteHealth } from './routeHealth';

const node = (id: string, dead: boolean) => ({ id, name: id, failCount: dead ? 2 : 0, dead });
const route = (id: string, ...nodes: ReturnType<typeof node>[]): RouteHealth => ({
  id,
  name: id,
  maxFails: 1,
  failTimeout: '10s',
  nodes,
});

describe('nodeRouteHealth', () => {
  it('marks a node dead when every route that reports it is dead', () => {
    const health = [
      route('r_0000000001', node('n_0000000001', true), node('n_0000000002', false)),
      route('r_0000000002', node('n_0000000002', true), node('n_0000000003', false)),
      route('r_0000000003', node('n_0000000004', true)),
    ];

    const got = nodeRouteHealth(health);

    expect([...got.dead].sort()).toEqual(['n_0000000001', 'n_0000000004']);
  });

  it('folds by node id, not by the display name', () => {
    const health = [
      route('r_0000000001', { id: 'n_0000000001', name: 'alpha', failCount: 2, dead: true }),
      route('r_0000000002', { id: 'n_0000000001', name: 'renamed', failCount: 0, dead: false }),
    ];

    const got = nodeRouteHealth(health);

    expect([...got.partial.keys()]).toEqual(['n_0000000001']);
    expect(got.dead.size).toBe(0);
  });

  it('reports partial death with the dead route ids in route order', () => {
    const health = [
      route('r_0000000001', node('n_0000000001', true)),
      route('r_0000000002', node('n_0000000001', false)),
      route('r_0000000003', node('n_0000000001', true)),
    ];

    const got = nodeRouteHealth(health);

    expect([...got.partial.keys()]).toEqual(['n_0000000001']);
    expect(got.partial.get('n_0000000001')).toEqual(['r_0000000001', 'r_0000000003']);
    expect(got.dead.size).toBe(0);
  });

  it('lists every dead route→node binding as an id-based edge', () => {
    const health = [
      route('r_0000000001', node('n_0000000001', true), node('n_0000000002', false)),
      route('r_0000000002', node('n_0000000001', false)),
    ];

    expect([...nodeRouteHealth(health).edges].sort()).toEqual([
      'route:r_0000000001|node:n_0000000001',
    ]);
  });

  it('does not count a route that does not report the node', () => {
    const health = [
      route('r_0000000001', node('n_0000000001', true)),
      route('r_0000000002', node('n_0000000002', false)),
    ];

    const got = nodeRouteHealth(health);

    expect([...got.dead]).toEqual(['n_0000000001']);
    expect(got.partial.size).toBe(0);
  });

  it('returns empty results without health', () => {
    const got = nodeRouteHealth([]);

    expect(got.dead.size).toBe(0);
    expect(got.partial.size).toBe(0);
    expect(got.edges.size).toBe(0);
  });
});
