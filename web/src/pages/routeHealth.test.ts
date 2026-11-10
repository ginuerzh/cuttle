import { describe, expect, it } from 'vitest';
import type { RouteHealth } from '../api/types';
import { nodeRouteHealth } from './routeHealth';

const node = (name: string, dead: boolean) => ({ name, failCount: dead ? 2 : 0, dead });
const route = (name: string, ...nodes: ReturnType<typeof node>[]): RouteHealth => ({
  name,
  maxFails: 1,
  failTimeout: '10s',
  nodes,
});

describe('nodeRouteHealth', () => {
  it('marks a node dead when every route that reports it is dead', () => {
    const health = [
      route('r1', node('a', true), node('b', false)),
      route('r2', node('b', true), node('c', false)),
      route('r3', node('d', true)),
    ];

    const got = nodeRouteHealth(health);

    expect([...got.dead].sort()).toEqual(['a', 'd']);
  });

  it('reports partial death with the dead route names in route order', () => {
    const health = [
      route('r1', node('a', true)),
      route('r2', node('a', false)),
      route('r3', node('a', true)),
    ];

    const got = nodeRouteHealth(health);

    expect([...got.partial.keys()]).toEqual(['a']);
    expect(got.partial.get('a')).toEqual(['r1', 'r3']);
    expect(got.dead.size).toBe(0);
  });

  it('lists every dead route→node binding as an edge', () => {
    const health = [route('r1', node('a', true), node('b', false)), route('r2', node('a', false))];

    expect([...nodeRouteHealth(health).edges].sort()).toEqual(['route:r1|node:a']);
  });

  it('does not count a route that does not report the node', () => {
    const health = [route('r1', node('a', true)), route('r2', node('b', false))];

    const got = nodeRouteHealth(health);

    expect([...got.dead]).toEqual(['a']);
    expect(got.partial.size).toBe(0);
  });

  it('returns empty results without health', () => {
    const got = nodeRouteHealth([]);

    expect(got.dead.size).toBe(0);
    expect(got.partial.size).toBe(0);
    expect(got.edges.size).toBe(0);
  });
});
