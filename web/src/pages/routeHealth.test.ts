import { describe, expect, it } from 'vitest';
import type { RouteHealth } from '../api/types';
import { deadNodeNames } from './routeHealth';

const node = (name: string, dead: boolean) => ({
  name,
  failCount: dead ? 2 : 0,
  dead,
});

describe('deadNodeNames', () => {
  it('collects nodes marked dead in any route', () => {
    const health: RouteHealth[] = [
      { name: 'r1', maxFails: 1, failTimeout: '10s', nodes: [node('a', true), node('b', false)] },
      { name: 'r2', maxFails: 1, failTimeout: '10s', nodes: [node('b', true), node('c', false)] },
    ];

    expect([...deadNodeNames(health)].sort()).toEqual(['a', 'b']);
  });

  it('returns an empty set when every node is healthy', () => {
    const health: RouteHealth[] = [
      { name: 'r1', maxFails: 1, failTimeout: '10s', nodes: [node('a', false)] },
    ];

    expect(deadNodeNames(health).size).toBe(0);
  });
});
