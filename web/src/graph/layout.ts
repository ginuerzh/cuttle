import dagre from 'dagre';
import type { Edge, XYPosition } from '@xyflow/react';
import type { GraphNode, NodeKind } from './build';
import type { PositionMap } from './positions';

const sizes: Record<NodeKind, { w: number; h: number }> = {
  entrypoint: { w: 250, h: 72 },
  route: { w: 280, h: 116 },
  node: { w: 250, h: 96 },
  provider: { w: 260, h: 82 },
  unrouted: { w: 210, h: 72 },
};

/** Estimated footprint of a node, used by both dagre and collision checks. */
export function nodeSize(kind: NodeKind): { w: number; h: number } {
  return sizes[kind] ?? sizes.provider;
}

const GAP = 24;
const MAX_COLUMNS = 40;
const MAX_ROWS = 40;

interface Rect {
  x: number;
  y: number;
  w: number;
  h: number;
}

function overlaps(a: Rect, b: Rect): boolean {
  return (
    a.x < b.x + b.w + GAP &&
    a.x + a.w + GAP > b.x &&
    a.y < b.y + b.h + GAP &&
    a.y + a.h + GAP > b.y
  );
}

/** Find the first free slot, preferring to move down before moving right. */
function findFree(start: XYPosition, size: { w: number; h: number }, occupied: Rect[]): XYPosition {
  const stepX = size.w + GAP;
  const stepY = size.h + GAP;
  for (let col = 0; col < MAX_COLUMNS; col++) {
    for (let row = 0; row < MAX_ROWS; row++) {
      const candidate = { x: start.x + col * stepX, y: start.y + row * stepY };
      if (!occupied.some((other) => overlaps({ ...candidate, ...size }, other))) {
        return candidate;
      }
    }
  }
  return start;
}

/**
 * Pick positions for nodes that are not in the saved layout yet. dagre does
 * not know about drag-adjusted saved positions, so its slot for a new node can
 * land exactly under an existing node. Walk down (then right) from that slot
 * until the card is clear.
 */
export function placeNewNodes(laid: GraphNode[], saved: PositionMap): PositionMap {
  const occupied: Rect[] = [];
  for (const node of laid) {
    const p = saved[node.id];
    if (!p) continue;
    const s = nodeSize(node.data.kind);
    occupied.push({ x: p.x, y: p.y, w: s.w, h: s.h });
  }

  const placed: PositionMap = {};
  for (const node of laid) {
    if (saved[node.id]) continue;
    const s = nodeSize(node.data.kind);
    const position = findFree(node.position, s, occupied);
    placed[node.id] = position;
    occupied.push({ x: position.x, y: position.y, w: s.w, h: s.h });
  }
  return placed;
}

export function layoutGraph(nodes: GraphNode[], edges: Edge[]): GraphNode[] {
  const g = new dagre.graphlib.Graph();
  g.setGraph({ rankdir: 'LR', nodesep: 36, ranksep: 110, marginx: 24, marginy: 24 });
  g.setDefaultEdgeLabel(() => ({}));

  for (const n of nodes) {
    const s = sizes[n.data.kind] ?? sizes.provider;
    g.setNode(n.id, { width: s.w, height: s.h });
  }
  for (const e of edges) {
    g.setEdge(e.source, e.target);
  }

  dagre.layout(g);

  return nodes.map((n) => {
    const s = sizes[n.data.kind] ?? sizes.provider;
    const p = g.node(n.id);
    return {
      ...n,
      position: p ? { x: p.x - s.w / 2, y: p.y - s.h / 2 } : n.position,
    };
  });
}
