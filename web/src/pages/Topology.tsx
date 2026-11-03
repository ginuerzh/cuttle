import { useMemo, useState } from 'react';
import {
  Background,
  Controls,
  MiniMap,
  Panel,
  ReactFlow,
  type Connection,
  type Edge,
  type EdgeMouseHandler,
  type NodeMouseHandler,
  type OnNodeDrag,
  type XYPosition,
} from '@xyflow/react';
import type { ConfigResponse, GraphMetrics, NodeMetrics } from '../api/types';
import { buildGraph, type GraphNode } from '../graph/build';
import { layoutGraph } from '../graph/layout';
import { nodeTypes } from '../graph/nodes';
import { edgeTypes } from './edges';

export type Selection =
  | { kind: 'node'; id: string }
  | { kind: 'edge'; id: string }
  | null;

interface Props {
  config: ConfigResponse;
  metrics: GraphMetrics | null;
  onSelect: (s: Selection) => void;
  editable?: boolean;
  onConnect?: (c: Connection) => void;
}

function edgeColor(m?: { requests: number; errors: number }): string {
  if (!m || m.requests === 0) return '#cbd5e1';
  const r = m.errors / m.requests;
  if (r > 0.05) return '#dc2626';
  if (r >= 0.01) return '#d97706';
  return '#16a34a';
}

function withMetrics(
  nodes: GraphNode[],
  edges: Edge[],
  metrics: GraphMetrics | null,
): { nodes: GraphNode[]; edges: Edge[] } {
  const ns = nodes.map((n) => ({
    ...n,
    data: { ...n.data, metrics: metrics?.nodes[n.id] },
  }));
  const es = edges.map((e) => {
    const m = metrics?.edges[e.id];
    const data = e.data as { color?: string; tier?: string } | undefined;
    const prefix = data?.tier && data.tier !== 'base' ? `${data.tier} · ` : '';
    const label = m ? `${prefix}${m.requests}${m.errors ? ` · ${m.errors} err` : ''}` : prefix || undefined;
    const width = m ? Math.min(4, 1.5 + Math.log10(m.requests + 1)) : 1.5;
    const color = m && m.requests > 0 ? edgeColor(m) : data?.color ?? '#94a3b8';
    return {
      ...e,
      label,
      type: 'dot',
      style: { ...e.style, stroke: color, strokeWidth: width },
      markerEnd: undefined,
      labelStyle: { fontSize: 10, fill: '#475569' },
      labelBgStyle: { fill: '#f8fafc', fillOpacity: 0.9 },
    } as Edge;
  });
  return { nodes: ns, edges: es };
}

const POSITIONS_KEY = 'llm-gw.positions';

function loadPositions(): Record<string, XYPosition> {
  try {
    const raw = localStorage.getItem(POSITIONS_KEY);
    return raw ? (JSON.parse(raw) as Record<string, XYPosition>) : {};
  } catch {
    return {};
  }
}

function savePositions(p: Record<string, XYPosition>) {
  try {
    localStorage.setItem(POSITIONS_KEY, JSON.stringify(p));
  } catch {
    /* ignore quota/private mode */
  }
}

export function Topology({ config, metrics, onSelect, editable, onConnect }: Props) {
  const [positions, setPositions] = useState<Record<string, XYPosition>>(loadPositions);

  const graph = useMemo(() => {
    const raw = buildGraph(config);
    const laid = layoutGraph(raw.nodes, raw.edges);
    const placed = laid.map((n) => ({ ...n, position: positions[n.id] ?? n.position }));
    return withMetrics(placed, raw.edges, metrics);
  }, [config, metrics, positions]);

  const onNodeClick: NodeMouseHandler = (_event, node) => {
    onSelect({ kind: 'node', id: node.id });
  };
  const onEdgeClick: EdgeMouseHandler = (_event, edge) => {
    onSelect({ kind: 'edge', id: edge.id });
  };
  const onNodeDragStop: OnNodeDrag<GraphNode> = (_event, node) => {
    setPositions((prev) => {
      const next = { ...prev, [node.id]: node.position };
      savePositions(next);
      return next;
    });
  };
  const resetLayout = () => {
    setPositions({});
    savePositions({});
  };

  return (
    <div style={{ width: '100%', height: '100%' }}>
      <ReactFlow
        nodes={graph.nodes}
        edges={graph.edges}
        nodeTypes={nodeTypes}
        edgeTypes={edgeTypes}
        onNodeClick={onNodeClick}
        onEdgeClick={onEdgeClick}
        onPaneClick={() => onSelect(null)}
        onConnect={onConnect}
        onNodeDragStop={onNodeDragStop}
        nodesConnectable={editable}
        nodesDraggable
        deleteKeyCode={null}
        connectionLineStyle={{ stroke: '#0ea5e9', strokeWidth: 2 }}
        fitView
        minZoom={0.2}
        maxZoom={1.6}
        proOptions={{ hideAttribution: true }}
      >
        <Background gap={20} color="#e2e8f0" />
        <Controls showInteractive={false} />
        <MiniMap pannable zoomable nodeStrokeWidth={2} />
        <Panel position="top-right">
          <button
            onClick={resetLayout}
            style={{
              border: '1px solid #cbd5e1',
              background: '#fff',
              borderRadius: 6,
              padding: '4px 10px',
              fontSize: 12,
              cursor: 'pointer',
              color: '#475569',
            }}
          >
            重置布局
          </button>
        </Panel>
      </ReactFlow>
    </div>
  );
}

export function nodeMetrics(metrics: GraphMetrics | null, id: string): NodeMetrics | undefined {
  return metrics?.nodes[id];
}
