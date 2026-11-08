import type { GraphMetrics } from '../api/types';

export interface Activity {
  /** Unique id for one one-shot traversal; used as the React key. */
  pulse: number;
  error: boolean;
  /** Seconds for one dot to traverse the edge. */
  duration: number;
}

export interface ActivityMap {
  nodes: Record<string, Activity>;
  edges: Record<string, Activity>;
}

const SLOW_DURATION = 0.9;
const FAST_DURATION = 0.45;

export function emptyActivity(): ActivityMap {
  return { nodes: {}, edges: {} };
}

/** More requests per tick means a faster dot traversal. */
export function flowDuration(delta: number): number {
  if (delta <= 0) return SLOW_DURATION;
  return Math.min(SLOW_DURATION, Math.max(FAST_DURATION, SLOW_DURATION / Math.sqrt(delta)));
}

function totalRequests(m: GraphMetrics): number {
  return Object.values(m.nodes).reduce((sum, node) => sum + node.requests, 0);
}

/**
 * Activity introduced between two cumulative snapshots. The first snapshot has
 * no history and must not animate all historical traffic; a lower total means
 * the window restarted.
 */
export function diffActivity(prev: GraphMetrics | null, next: GraphMetrics, pulse: number): ActivityMap {
  const out = emptyActivity();
  if (!prev || totalRequests(next) < totalRequests(prev)) return out;

  for (const [id, metric] of Object.entries(next.nodes)) {
    const before = prev.nodes[id];
    const delta = metric.requests - (before?.requests ?? 0);
    if (delta <= 0) continue;
    out.nodes[id] = {
      pulse,
      error: metric.errors > (before?.errors ?? 0),
      duration: flowDuration(delta),
    };
  }
  for (const [id, metric] of Object.entries(next.edges)) {
    const before = prev.edges[id];
    const delta = metric.requests - (before?.requests ?? 0);
    if (delta <= 0) continue;
    out.edges[id] = {
      pulse,
      error: metric.errors > (before?.errors ?? 0),
      duration: flowDuration(delta),
    };
  }
  return out;
}

function hasEntries(map: ActivityMap): boolean {
  return Object.keys(map.nodes).length > 0 || Object.keys(map.edges).length > 0;
}

/** Keep the last pulse per node/edge; a newer pulse replaces it. */
export function mergeActivity(current: ActivityMap, next: ActivityMap): ActivityMap {
  if (!hasEntries(next)) return current;
  return {
    nodes: { ...current.nodes, ...next.nodes },
    edges: { ...current.edges, ...next.edges },
  };
}
