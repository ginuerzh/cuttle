import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from 'react';
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
  type OnMove,
  type OnMoveEnd,
  type OnMoveStart,
  type ReactFlowInstance,
  useNodesState,
  type OnNodeDrag,
  type Viewport,
} from '@xyflow/react';
import { message } from 'antd';
import type { ConfigResponse, EntrypointHealth, GraphMetrics, NodeMetrics } from '../api/types';
import { getTopologyPositions, putTopologyPositions } from '../api/client';
import { buildGraph, type GraphNode } from '../graph/build';
import { layoutGraph, placeNewNodes } from '../graph/layout';
import {
  createPositionSaver,
  mergeNodeState,
  mergePositions,
  pickInitialPositions,
  type PositionMap,
} from '../graph/positions';
import { nodeTypes } from '../graph/nodes';
import { configLabels, graphLabel } from '../labels';
import {
  accumulateActivity,
  emptyActivity,
  visibleActivity,
  type Activity,
  type ActivityMap,
} from '../graph/activity';
import {
  clampVelocity,
  decayVelocity,
  dragVelocity,
  INERTIA_MIN_SPEED,
  INERTIA_MIN_START_SPEED,
  speedOf,
  type PanSample,
} from '../graph/inertia';
import { edgeTypes, bindingEdgeBadge, bindingEdgeHealth, type BindingEdgeState } from './edges';
import { translate as t } from '../i18n';
import type { NodeRouteHealth } from './routeHealth';

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
  nodeHealth?: NodeRouteHealth;
  /** Binding eligibility of the current build; omitted leaves every edge plain. */
  entrypointHealth?: readonly EntrypointHealth[];
  since?: string;
  animate?: boolean;
}

/**
 * Monitor mode is read-only. Only an explicit `editable` may start or change
 * connections; leaving this undefined would fall back to React Flow's default
 * of `true` and keep the handles interactive.
 */
export function canEditTopology(editable?: boolean): boolean {
  return editable === true;
}

/** Compares dead-route id lists so an unchanged health snapshot is a no-op. */
function sameRoutes(a?: readonly string[], b?: readonly string[]): boolean {
  const left = a ?? [];
  const right = b ?? [];
  return left.length === right.length && left.every((id, i) => id === right[i]);
}

/**
 * Folds route health onto the binding nodes: an all-dead node sets `dead`, and
 * a partially dead node carries the routes where it is dead. Nodes whose state
 * did not change keep their identity so React Flow does not re-render every
 * card on each health snapshot.
 */
export function mergeNodeHealth(
  nodes: GraphNode[],
  nodeHealth?: NodeRouteHealth,
  labelRoute?: (id: string) => string,
): GraphNode[] {
  return nodes.map((n) => {
    if (!n.id.startsWith('node:')) return n;
    const nodeId = n.id.slice('node:'.length);
    const dead = nodeHealth?.dead.has(nodeId) ?? false;
    const deadRoutes = [...(nodeHealth?.partial.get(nodeId) ?? [])];
    // The badge tooltip shows display names; the ids stay in `deadRoutes` so the
    // edge set and the equality check keep working on identities.
    const deadRouteLabels = labelRoute ? deadRoutes.map((id) => labelRoute(id)) : deadRoutes;
    if (
      (n.data.dead ?? false) === dead &&
      sameRoutes(n.data.deadRoutes, deadRoutes) &&
      sameRoutes(n.data.deadRouteLabels, deadRouteLabels)
    ) {
      return n;
    }
    return { ...n, data: { ...n.data, dead, deadRoutes, deadRouteLabels } };
  });
}

function edgeColor(m?: { requests: number; errors: number }): string {
  if (!m || m.requests === 0) return '#cbd5e1';
  const r = m.errors / m.requests;
  if (r > 0.05) return '#dc2626';
  if (r >= 0.01) return '#d97706';
  return '#16a34a';
}

export function edgeLabel(m: {
  requests: number;
  errors: number;
  attempts?: number;
  retriedRequests?: number;
}): ReactNode {
  const attempts = m.attempts ?? 0;
  let label: ReactNode = null;
  const append = (node: ReactNode) => {
    label = label === null ? node : <>{label} · {node}</>;
  };
  // A binding that only ever discarded attempts has no request row of its own,
  // so a bare "0" would hide the only evidence the edge carries any traffic.
  if (m.requests > 0 || attempts === 0) {
    append(`${m.requests}`);
  }
  if (m.errors) {
    append(<span style={{ color: '#dc2626' }}>{m.errors}</span>);
  }
  if (attempts > 0) {
    append(
      <span
        title={t('重试尝试 {attempts} · 重试请求 {requests}', {
          attempts,
          requests: m.retriedRequests ?? 0,
        })}
      >
        {`↻${attempts}`}
      </span>,
    );
  }
  return label;
}

interface EdgeData extends Record<string, unknown> {
  color: string;
  pulse?: number;
  error?: boolean;
  duration?: number;
  /** A draft edge stays dashed while it carries no traffic. */
  draft?: boolean;
}

export function decorateEdge(
  e: Edge,
  metrics: GraphMetrics | null,
  activity?: Activity,
  animate = true,
  deadEdges?: ReadonlySet<string>,
  bindingHealth?: ReadonlyMap<string, BindingEdgeState>,
): Edge {
  const m = metrics?.edges[e.id];
  const data = e.data as EdgeData | undefined;
  const dead = deadEdges?.has(e.id) ?? false;
  const badge = bindingEdgeBadge(bindingHealth?.get(e.id));
  const trafficLabel = m ? edgeLabel(m) : undefined;
  const badgeLabel = badge.text ? (
    badge.invalid ? <span style={{ color: '#d97706' }}>{badge.text}</span> : badge.text
  ) : null;
  // A dead binding stays silently red and dashed: the line style already
  // carries the state, so the label only keeps its traffic counts.
  let label: ReactNode = trafficLabel;
  if (badgeLabel !== null) {
    label = label === null || label === undefined ? badgeLabel : <>{label} · {badgeLabel}</>;
  }
  const width = m
    ? Math.min(4, 1.5 + Math.log10(Math.max(m.requests, m.attempts ?? 0) + 1))
    : 1.5;
  const color = dead
    ? '#dc2626'
    : badge.invalid
      ? '#d97706'
      : m && m.requests > 0
        ? edgeColor(m)
        : data?.color ?? '#94a3b8';
  return {
    ...e,
    label,
    type: 'dot',
    data: {
      ...data,
      pulse: animate ? activity?.pulse ?? 0 : 0,
      error: animate ? !!activity?.error : false,
      duration: activity?.duration ?? 0,
    },
    style: {
      ...e.style,
      stroke: color,
      strokeWidth: width,
      strokeDasharray: dead || badge.invalid || data?.draft ? '6 4' : undefined,
    },
    markerEnd: undefined,
  } as Edge;
}

const POSITIONS_KEY = 'cuttle.positions';
const PENDING_KEY = 'cuttle.positions.pending';

/**
 * Edit mode mounts a second Topology, so the React Flow viewport must survive
 * a component remount; otherwise every mode switch fits the graph again.
 */
let lastViewport: Viewport | null = null;

export interface ViewportInit {
  fitView: boolean;
  defaultViewport?: Viewport;
}

/** Fit only until the canvas has been seen; restore the saved view afterwards. */
export function viewportInit(saved: Viewport | null): ViewportInit {
  return saved ? { fitView: false, defaultViewport: saved } : { fitView: true };
}

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

export function Topology({ config, metrics, onSelect, editable, onConnect, nodeHealth, entrypointHealth, since, animate = true }: Props) {
  const canEdit = canEditTopology(editable);
  const [initialViewport] = useState(() => viewportInit(lastViewport));
  const [positions, setPositions] = useState<PositionMap>(loadLocalPositions);
  const [serverReady, setServerReady] = useState(false);
  const [serverReadOK, setServerReadOK] = useState(false);
  const mutatedRef = useRef(false);
  const saveVersionRef = useRef(0);
  const saveToServerRef = useRef(createPositionSaver(putTopologyPositions));
  const saveToServer = saveToServerRef.current;
  const flowRef = useRef<ReactFlowInstance<GraphNode, Edge> | null>(null);
  const dragRef = useRef<{ active: boolean; samples: PanSample[] }>({
    active: false,
    samples: [],
  });
  const inertiaRef = useRef<number | null>(null);
  const [activity, setActivity] = useState<ActivityMap>(emptyActivity);
  const prevMetricsRef = useRef<GraphMetrics | null>(null);
  const sinceRef = useRef<string | undefined>(since);
  const activityRef = useRef(activity);
  activityRef.current = activity;
  const pulseRef = useRef(0);
  // Dead-route names for the badge tooltip; ids stay in the node data.
  const routeLabels = useMemo(() => configLabels(config), [config]);
  const labelRoute = useCallback((id: string) => graphLabel(`route:${id}`, routeLabels), [routeLabels]);
  // Binding eligibility keyed by edge id. The effects re-decorate the previous
  // edge objects with it, so React Flow's `selected` flag survives a snapshot.
  const bindingHealth = useMemo(() => bindingEdgeHealth(entrypointHealth), [entrypointHealth]);

  useEffect(() => {
    if (!metrics) {
      prevMetricsRef.current = null;
      sinceRef.current = since;
      setActivity(emptyActivity());
      return;
    }
    const pulse = ++pulseRef.current;
    const prev = prevMetricsRef.current;
    if (prev === null || sinceRef.current !== since) {
      prevMetricsRef.current = metrics;
      sinceRef.current = since;
      setActivity(emptyActivity());
      return;
    }
    setActivity((current) => accumulateActivity(current, prev, metrics, animate, pulse));
    prevMetricsRef.current = metrics;
  }, [metrics, since, animate]);

  const stopInertia = useCallback(() => {
    if (inertiaRef.current !== null) {
      cancelAnimationFrame(inertiaRef.current);
      inertiaRef.current = null;
    }
  }, []);

  useEffect(() => stopInertia, [stopInertia]);

  const startMove: OnMoveStart = useCallback(
    (event) => {
      if (!event) return;
      if (event.type !== 'mousedown' && event.type !== 'touchstart') return;
      if (event.type === 'touchstart' && (event as TouchEvent).touches.length > 1) return;
      stopInertia();
      dragRef.current = { active: true, samples: [] };
    },
    [stopInertia],
  );

  const trackMove: OnMove = useCallback((_event, viewport) => {
    if (!dragRef.current.active) return;
    const samples = dragRef.current.samples;
    samples.push({ t: performance.now(), x: viewport.x, y: viewport.y });
    if (samples.length > 10) samples.shift();
  }, []);

  const endMove: OnMoveEnd = useCallback((_event, viewport) => {
    lastViewport = viewport;
    if (!dragRef.current.active) return;
    dragRef.current.active = false;

    const velocity = clampVelocity(dragVelocity(dragRef.current.samples, performance.now()));
    if (speedOf(velocity) < INERTIA_MIN_START_SPEED) return;

    let current = velocity;
    let last = performance.now();
    const step = (now: number) => {
      const dt = Math.min(now - last, 32);
      last = now;
      const instance = flowRef.current;
      if (!instance) {
        inertiaRef.current = null;
        return;
      }
      const viewportNow = instance.getViewport();
      const next = {
        x: viewportNow.x + current.vx * dt,
        y: viewportNow.y + current.vy * dt,
        zoom: viewportNow.zoom,
      };
      instance.setViewport(next);
      lastViewport = next;
      current = decayVelocity(current, dt);
      if (speedOf(current) < INERTIA_MIN_SPEED) {
        inertiaRef.current = null;
        return;
      }
      inertiaRef.current = requestAnimationFrame(step);
    };
    inertiaRef.current = requestAnimationFrame(step);
  }, []);

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
    // Nodes added since the saved layout keep the dagre slot only when it is
    // clear; otherwise they are moved out from under the existing cards.
    const placed = placeNewNodes(laid, positions);
    return {
      nodes: laid.map((n) => ({ ...n, position: positions[n.id] ?? placed[n.id] ?? n.position })),
      edges: raw.edges,
    };
  }, [config, positions]);

  const [nodes, setNodes, onNodesChange] = useNodesState<GraphNode>(structure.nodes);
  const [edges, setEdges] = useState<Edge[]>(
    structure.edges.map((e) => decorateEdge(e, metrics, undefined, true, nodeHealth?.edges, bindingHealth)),
  );
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
    setNodes((prev) => mergeNodeState(prev, structure.nodes));
    setEdges(
      structure.edges.map((e) =>
        decorateEdge(e, metrics, activityRef.current.edges[e.id], animate, nodeHealth?.edges, bindingHealth),
      ),
    );
    // metrics is intentionally read once per structure change; the metrics
    // effect below keeps labels fresh without touching positions. Dead edges
    // are re-decorated from the previous edge objects instead: rebuilding them
    // here would drop the selection state React Flow keeps on each edge.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [structure, animate]);

  useEffect(() => {
    setNodes((prev) =>
      prev.map((n) => {
        const { pulse, error } = visibleActivity(activity.nodes[n.id], animate);
        const m = metrics?.nodes[n.id];
        if (n.data.metrics === m && (n.data.pulse ?? 0) === pulse && (n.data.error ?? false) === error) {
          return n;
        }
        return { ...n, data: { ...n.data, metrics: m, pulse, error } };
      }),
    );
    setEdges((prev) => prev.map((e) => decorateEdge(e, metrics, activity.edges[e.id], animate, nodeHealth?.edges, bindingHealth)));
  }, [metrics, activity, animate, nodeHealth?.edges, bindingHealth, setNodes]);

  useEffect(() => {
    setNodes((prev) => mergeNodeHealth(prev, nodeHealth, labelRoute));
  }, [nodeHealth, labelRoute, setNodes]);

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
    const next = placeNewNodes(laid, {});
    setPositions(next);
    persist(next);
  };

  return (
    <div
      style={{ width: '100%', height: '100%' }}
      onPointerDownCapture={stopInertia}
      onWheelCapture={stopInertia}
    >
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
        onConnect={canEdit ? onConnect : undefined}
        onNodeDragStop={canEdit ? onNodeDragStop : undefined}
        onInit={(instance) => {
          flowRef.current = instance;
        }}
        onMoveStart={startMove}
        onMove={trackMove}
        onMoveEnd={endMove}
        nodesConnectable={canEdit}
        nodesDraggable={canEdit}
        connectOnClick={canEdit}
        edgesReconnectable={canEdit}
        deleteKeyCode={null}
        connectionLineStyle={{ stroke: '#0ea5e9', strokeWidth: 2 }}
        fitView={initialViewport.fitView}
        defaultViewport={initialViewport.defaultViewport}
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
