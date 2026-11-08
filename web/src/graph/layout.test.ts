import { describe, expect, it } from 'vitest';
import type { GraphNode, NodeKind } from './build';
import { nodeSize, placeNewNodes } from './layout';

function node(id: string, position: { x: number; y: number }, kind: NodeKind = 'node'): GraphNode {
  return { id, type: kind, position, data: { kind, label: id } };
}

function overlaps(
  a: { x: number; y: number },
  b: { x: number; y: number },
  kind: NodeKind = 'node',
): boolean {
  const s = nodeSize(kind);
  return (
    a.x < b.x + s.w &&
    a.x + s.w > b.x &&
    a.y < b.y + s.h &&
    a.y + s.h > b.y
  );
}

describe('placeNewNodes', () => {
  it('moves a new node out from under a saved node', () => {
    const saved = { 'node:a': { x: 0, y: 0 } };
    const laid = [node('node:a', { x: 0, y: 0 }), node('node:b', { x: 0, y: 0 })];

    const placed = placeNewNodes(laid, saved);

    expect(placed['node:a']).toBeUndefined();
    expect(placed['node:b']).not.toEqual({ x: 0, y: 0 });
    expect(overlaps(placed['node:b'], saved['node:a'])).toBe(false);
  });

  it('keeps a clear dagre slot', () => {
    const placed = placeNewNodes(
      [node('node:a', { x: 0, y: 0 }), node('node:b', { x: 500, y: 300 })],
      { 'node:a': { x: 0, y: 0 } },
    );

    expect(placed['node:b']).toEqual({ x: 500, y: 300 });
  });

  it('places several new nodes without overlapping each other', () => {
    const laid = [
      node('node:a', { x: 0, y: 0 }),
      node('node:b', { x: 0, y: 0 }),
      node('node:c', { x: 0, y: 0 }),
    ];

    const placed = placeNewNodes(laid, {});

    expect(overlaps(placed['node:a'], placed['node:b'])).toBe(false);
    expect(overlaps(placed['node:a'], placed['node:c'])).toBe(false);
    expect(overlaps(placed['node:b'], placed['node:c'])).toBe(false);
  });
});
