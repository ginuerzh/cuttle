import { describe, expect, it } from 'vitest';
import type { RouteHealth } from '../api/types';
import { deadNodeNames } from './routeHealth';

const node = (name: string, dead: boolean) => ({
  name,
  failCount: dead ? 2 : 0,
  dead,
});

describe('deadNodeNames', () => {
  it('marks a node dead only when every route that references it is dead', () => {
    const health: RouteHealth[] = [
      { name: 'r1', maxFails: 1, failTimeout: '10s', nodes: [node('a', true), node('b', false)] },
      { name: 'r2', maxFails: 1, failTimeout: '10s', nodes: [node('b', true), node('c', false)] },
    ];

    expect([...deadNodeNames(health)].sort()).toEqual(['a']);
  });

  it('marks a shared node dead when all of its routes are dead', () => {
    const health: RouteHealth[] = [
      { name: 'r1', maxFails: 1, failTimeout: '10s', nodes: [node('a', true)] },
      { name: 'r2', maxFails: 1, failTimeout: '10s', nodes: [node('a', true)] },
    ];

    expect([...deadNodeNames(health)]).toEqual(['a']);
  });

  it('returns an empty set when every node is healthy', () => {
    const health: RouteHealth[] = [
      { name: 'r1', maxFails: 1, failTimeout: '10s', nodes: [node('a', false)] },
    ];

    expect(deadNodeNames(health).size).toBe(0);
  });
});
