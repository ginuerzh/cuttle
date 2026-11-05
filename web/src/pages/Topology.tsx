import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  Background,
  applyEdgeChanges,
  Controls,
  MiniMap,
  Panel,
  ReactFlow,
  type Connection,
  type Edge,
  type EdgeMouseHandler,
  type NodeMouseHandler,
  type OnEdgesChange,
  useNodesState,
  type OnNodeDrag,
} from '@xyflow/react';
import { message } from 'antd';
import type { ConfigResponse, GraphMetrics, NodeMetrics } from '../api/types';
import { getTopologyPositions, putTopologyPositions } from '../api/client';
import { buildGraph, type GraphNode } from '../graph/build';
import { layoutGraph } from '../graph/layout';
import {
  createPositionSaver,
  mergePositions,
  pickInitialPositions,
  type PositionMap,
} from '../graph/positions';
import { nodeTypes } from '../graph/nodes';
import { edgeTypes } from './edges';
import { translate as t } from '../i18n';

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

function decorateEdge(e: Edge, metrics: GraphMetrics | null): Edge {
  const m = metrics?.edges[e.id];
  const data = e.data as { color?: string } | undefined;
  const label = m ? `${m.requests}${m.errors ? ` · ${m.errors} err` : ''}` : undefined;
  const width = m ? Math.min(4, 1.5 + Math.log10(m.requests + 1)) : 1.5;
  const color = m && m.requests > 0 ? edgeColor(m) : data?.color ?? '#94a3b8';
  return {
    ...e,
    label,
    type: 'dot',
    style: { ...e.style, stroke: color, strokeWidth: width },
    markerEnd: undefined,
  } as Edge;
}

const POSITIONS_KEY = 'llm-gw.positions';
const PENDING_KEY = 'llm-gw.positions.pending';

function loadLocalPositions(): PositionMap {
  try {
    const raw = localStorage.getItem(POSITIONS_KEY);
    return raw ? (JSON.parse(raw) as PositionMap) : {};
  } catch {
    return {};
  }
}

function loadLocalPending(): boolean {
  try {
    return localStorage.getItem(PENDING_KEY) === '1';
  } catch {
    return false;
  }
}

function saveLocalPositions(p: PositionMap, pending: boolean) {
  try {
    localStorage.setItem(POSITIONS_KEY, JSON.stringify(p));
    if (pending) {
      localStorage.setItem(PENDING_KEY, '1');
    } else {
      localStorage.removeItem(PENDING_KEY);
    }
  } catch {
    /* ignore quota/private mode */
  }
}

export function Topology({ config, metrics, onSelect, editable, onConnect }: Props) {
  const [positions, setPositions] = useState<PositionMap>(loadLocalPositions);
  const [serverReady, setServerReady] = useState(false);
  const [serverReadOK, setServerReadOK] = useState(false);
  const mutatedRef = useRef(false);
  const saveVersionRef = useRef(0);
  const saveToServerRef = useRef(createPositionSaver(putTopologyPositions));
  const saveToServer = saveToServerRef.current;

  const persist = useCallback(
    (next: PositionMap) => {
      mutatedRef.current = true;
      const version = ++saveVersionRef.current;
      saveLocalPositions(next, true);
      void saveToServer(next)
        .then(() => {
          // Only the latest edit may clear the pending marker; an older save
          // finishing later must not mask a newer unsynced map.
          if (version === saveVersionRef.current) {
            saveLocalPositions(next, false);
          }
        })
        .catch(() => {
          message.warning({
            content: t('拓扑布局已保存到本地，但同步服务端失败'),
            key: 'topology-save',
          });
        });
    },
    [saveToServer],
  );

  useEffect(() => {
    let active = true;
    getTopologyPositions()
      .then((server) => {
        if (!active) return;
        const local = loadLocalPositions();
        const pending = loadLocalPending();
        const picked = pickInitialPositions(server, local, pending, mutatedRef.current);
        if (picked.positions !== null) {
          setPositions(picked.positions);
          if (picked.seed) {
            persist(picked.seed);
          } else {
            saveLocalPositions(picked.positions, false);
          }
        } else if (pending && Object.keys(local).length > 0) {
          // The server read raced a local edit whose save failed; retry it.
          persist(local);
        }
        setServerReadOK(true);
      })
      .catch(() => {
        if (!active) return;
        message.warning({
          content: t('无法读取服务端拓扑布局，已使用本地备份'),
          key: 'topology-load',
        });
      })
      .finally(() => {
        if (active) setServerReady(true);
      });
    return () => {
      active = false;
    };
  }, [persist, saveToServer]);

  // Structure (positions from dagre or the saved layout) only changes with the
  // config; metrics must not rebuild it, otherwise a poll resets the node
  // React Flow is currently dragging.
  const structure = useMemo(() => {
    const raw = buildGraph(config);
    const laid = layoutGraph(raw.nodes, raw.edges);
    return {
      nodes: laid.map((n) => ({ ...n, position: positions[n.id] ?? n.position })),
      edges: raw.edges,
    };
  }, [config, positions]);

  const [nodes, setNodes, onNodesChange] = useNodesState<GraphNode>(structure.nodes);
  const [edges, setEdges] = useState<Edge[]>(structure.edges.map((e) => decorateEdge(e, metrics)));
  const onEdgesChange: OnEdgesChange = (changes) => {
    setEdges((current) => applyEdgeChanges(changes, current));
  };

  // Persist the first layout for every node. Without this, only dragged nodes
  // have saved positions and any config change (adding/deleting an edge)
  // re-runs dagre for the rest of the graph.
  useEffect(() => {
    if (!serverReady) return;
    const next = mergePositions(positions, structure.nodes);
    if (next === positions) return;
    setPositions(next);
    if (serverReadOK) persist(next);
  }, [persist, positions, serverReadOK, serverReady, structure.nodes]);

  useEffect(() => {
    setNodes(structure.nodes);
    setEdges(structure.edges.map((e) => decorateEdge(e, metrics)));
    // metrics is intentionally read once per structure change; the metrics
    // effect below keeps labels fresh without touching positions.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [structure]);

  useEffect(() => {
    setNodes((prev) =>
      prev.map((n) => (n.data.metrics === metrics?.nodes[n.id] ? n : { ...n, data: { ...n.data, metrics: metrics?.nodes[n.id] } })),
    );
    setEdges((prev) => prev.map((e) => decorateEdge(e, metrics)));
  }, [metrics, setNodes]);

  const onNodeClick: NodeMouseHandler = (_event, node) => {
    onSelect({ kind: 'node', id: node.id });
  };
  const onEdgeClick: EdgeMouseHandler = (_event, edge) => {
    onSelect({ kind: 'edge', id: edge.id });
  };
  const onNodeDragStop: OnNodeDrag<GraphNode> = (_event, node) => {
    const next = { ...positions, [node.id]: node.position };
    setPositions(next);
    persist(next);
  };
  const resetLayout = () => {
    const raw = buildGraph(config);
    const laid = layoutGraph(raw.nodes, raw.edges);
    const next: PositionMap = {};
    for (const n of laid) next[n.id] = n.position;
    setPositions(next);
    persist(next);
  };

  return (
    <div style={{ width: '100%', height: '100%' }}>
      <ReactFlow
        nodes={nodes}
        edges={edges}
        onNodesChange={onNodesChange}
        onEdgesChange={onEdgesChange}
        nodeTypes={nodeTypes}
        edgeTypes={edgeTypes}
        onNodeClick={onNodeClick}
        onEdgeClick={onEdgeClick}
        onPaneClick={() => onSelect(null)}
        onConnect={onConnect}
        onNodeDragStop={editable ? onNodeDragStop : undefined}
        nodesConnectable={editable}
        nodesDraggable={!!editable}
        deleteKeyCode={null}
        connectionLineStyle={{ stroke: '#0ea5e9', strokeWidth: 2 }}
        fitView
        minZoom={0.2}
        maxZoom={1.6}
        proOptions={{ hideAttribution: true }}
      >
        <Background gap={20} color="var(--gw-border)" />
        <Controls showInteractive={false} />
        <MiniMap pannable zoomable nodeStrokeWidth={2} />
        <Panel position="top-right">
          <button
            onClick={resetLayout}
            style={{
              border: '1px solid var(--gw-control-border)',
              background: 'var(--gw-surface)',
              borderRadius: 6,
              padding: '4px 10px',
              fontSize: 12,
              cursor: 'pointer',
              color: 'var(--gw-control-text)',
            }}
          >
            {t('重置布局')}
          </button>
        </Panel>
      </ReactFlow>
    </div>
  );
}

export function nodeMetrics(metrics: GraphMetrics | null, id: string): NodeMetrics | undefined {
  return metrics?.nodes[id];
}
