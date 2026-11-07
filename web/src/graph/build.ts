import { MarkerType, type Edge, type Node } from '@xyflow/react';
import type { ConfigResponse, Entrypoint, Node as BindNode, NodeMetrics, Provider, Route } from '../api/types';

export type NodeKind = 'entrypoint' | 'route' | 'node' | 'provider' | 'unrouted';

export interface GraphNodeData extends Record<string, unknown> {
  kind: NodeKind;
  label: string;
  sub?: string;
  disabled?: boolean;
  provider?: Provider;
  route?: Route;
  entrypoint?: Entrypoint;
  node?: BindNode;
  metrics?: NodeMetrics;
  /** The node's x marker is dead in at least one route. */
  dead?: boolean;
}

export type GraphNode = Node<GraphNodeData>;

const PALETTE = [
  '#2563eb', // blue
  '#7c3aed', // violet
  '#db2777', // pink
  '#ea580c', // orange
  '#0891b2', // cyan
  '#65a30d', // lime
  '#c026d3', // fuchsia
  '#0d9488', // teal
];

const ENTRYPOINT_COLOR = '#6366f1';
const UNROUTED_COLOR = '#dc2626';

/** Stable colour per route, so every edge of a route shares one identity. */
export function routeColor(name: string): string {
  let h = 0;
  for (let i = 0; i < name.length; i++) {
    h = (h * 31 + name.charCodeAt(i)) >>> 0;
  }
  return PALETTE[h % PALETTE.length];
}

interface EdgeData extends Record<string, unknown> {
  color: string;
}

function edge(
  id: string,
  source: string,
  target: string,
  sourceHandle?: string,
  label?: string,
  data?: EdgeData,
): Edge {
  const color = data?.color ?? '#94a3b8';
  return {
    id,
    source,
    target,
    sourceHandle,
    label,
    type: 'dot',
    data,
    style: {
      stroke: color,
      strokeWidth: 1.5,
    },
    markerEnd: { type: MarkerType.ArrowClosed, color, width: 14, height: 14 },
  };
}

/**
 * Builds the routing DAG from the gateway config.
 *
 * An entrypoint owns the client model globs and points at exactly one route.
 * A route references nodes, and each node points at one provider model.
 */
export function buildGraph(
  cfg: Pick<ConfigResponse, 'entrypoints' | 'routes' | 'providers' | 'nodes'>,
): {
  nodes: GraphNode[];
  edges: Edge[];
} {
  const nodes: GraphNode[] = [];
  const edges: Edge[] = [];

  const routes = cfg.routes ?? [];
  const entrypoints = cfg.entrypoints ?? [];
  const providers = cfg.providers ?? [];
  const bindings = cfg.nodes ?? [];
  const bindingByName = new Map(bindings.map((n) => [n.name, n]));
  const routeByName = new Map(routes.map((r) => [r.name, r]));

  for (const p of providers) {
    nodes.push({
      id: `provider:${p.name}`,
      type: 'provider',
      position: { x: 0, y: 0 },
      data: {
        kind: 'provider',
        label: p.name,
        sub: [p.type, `keys×${p.keysSet}`].filter(Boolean).join(' · '),
        provider: p,
        disabled: p.enabled === false,
      },
    });
  }

  for (const n of bindings) {
    const p = providers.find((x) => x.name === n.provider);
    nodes.push({
      id: `node:${n.name}`,
      type: 'node',
      position: { x: 0, y: 0 },
      data: {
        kind: 'node',
        label: n.name,
        sub: [n.provider, n.model].filter(Boolean).join(' · '),
        node: n,
        disabled: n.enabled === false || (p ? p.enabled === false : false),
      },
    });
    if (p) {
      edges.push(
        edge(`node:${n.name}|provider:${n.provider}`, `node:${n.name}`, `provider:${n.provider}`, undefined, undefined, {
          color: '#0891b2',
        }),
      );
    }
  }

  for (const r of routes) {
    nodes.push({
      id: `route:${r.name}`,
      type: 'route',
      position: { x: 0, y: 0 },
      data: {
        kind: 'route',
        label: r.name,
        sub: [`${(r.nodes ?? []).length} nodes`, r.strategy || 'round'].join(' · '),
        route: r,
      },
    });

    const color = routeColor(r.name);
    for (const ref of r.nodes ?? []) {
      const n = bindingByName.get(ref);
      if (!n) {
        continue;
      }
      edges.push(
        edge(`route:${r.name}|node:${n.name}`, `route:${r.name}`, `node:${n.name}`, undefined, undefined, {
          color,
        }),
      );
    }
  }

  const entrypointIds = new Set<string>();
  let needsUnrouted = false;
  for (const e of entrypoints) {
    const id = `entrypoint:${e.name}`;
    if (entrypointIds.has(id)) {
      continue;
    }
    entrypointIds.add(id);
    nodes.push({
      id,
      type: 'entrypoint',
      position: { x: 0, y: 0 },
      data: {
        kind: 'entrypoint',
        label: e.name,
        sub: `${(e.match ?? []).join(', ')} → ${e.route}`,
        entrypoint: e,
      },
    });

    const target = routeByName.get(e.route);
    if (target) {
      edges.push(edge(`${id}->route:${target.name}`, id, `route:${target.name}`, undefined, undefined, { color: ENTRYPOINT_COLOR }));
    } else {
      needsUnrouted = true;
      edges.push(edge(`${id}->unrouted`, id, 'unrouted', undefined, undefined, { color: UNROUTED_COLOR }));
    }
  }
  if (needsUnrouted) {
    nodes.push({
      id: 'unrouted',
      type: 'unrouted',
      position: { x: 0, y: 0 },
      data: { kind: 'unrouted', label: 'unrouted', sub: 'no route matched' },
    });
  }

  return { nodes, edges };
}
