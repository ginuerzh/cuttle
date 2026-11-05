import dagre from 'dagre';
import type { Edge } from '@xyflow/react';
import type { GraphNode, NodeKind } from './build';

const sizes: Record<NodeKind, { w: number; h: number }> = {
  entrypoint: { w: 250, h: 72 },
  route: { w: 280, h: 116 },
  node: { w: 250, h: 96 },
  provider: { w: 260, h: 82 },
  unrouted: { w: 210, h: 72 },
};

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
