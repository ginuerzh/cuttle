import { describe, expect, it } from 'vitest';
import { addRouteNode, reorderNodes } from './routeNodes';

describe('reorderNodes', () => {
  it('moves a node down and preserves the rest', () => {
    expect(reorderNodes(['a', 'b', 'c'], 0, 2)).toEqual(['b', 'c', 'a']);
  });

  it('moves a node up', () => {
    expect(reorderNodes(['a', 'b', 'c'], 2, 0)).toEqual(['c', 'a', 'b']);
  });

  it('returns the original slice for invalid moves', () => {
    const nodes = ['a', 'b'];
    expect(reorderNodes(nodes, 0, 0)).toBe(nodes);
    expect(reorderNodes(nodes, -1, 1)).toBe(nodes);
    expect(reorderNodes(nodes, 0, 2)).toBe(nodes);
  });
});

describe('addRouteNode', () => {
  it('appends a new node and ignores duplicates', () => {
    expect(addRouteNode(['a'], 'b')).toEqual(['a', 'b']);
    expect(addRouteNode(['a'], 'a')).toEqual(['a']);
  });
});
