import type { XYPosition } from '@xyflow/react';

export type PositionMap = Record<string, XYPosition>;

/**
 * Keep every node's position across config changes. Nodes that were never
 * dragged still get their first layout position persisted, so adding or
 * deleting an edge cannot re-run layout for the whole graph.
 */
export function mergePositions(
  saved: Record<string, XYPosition>,
  nodes: Array<{ id: string; position: XYPosition }>,
): Record<string, XYPosition> {
  let next = saved;
  for (const node of nodes) {
    if (next[node.id]) continue;
    if (next === saved) next = { ...saved };
    next[node.id] = node.position;
  }
  return next;
}

/**
 * Merge rebuilt graph nodes into the live node state. Replacing them with fresh
 * objects drops React Flow's `measured` dimensions and handle bounds, and an
 * edge whose endpoint is unmeasured is not rendered.
 */
export function mergeNodeState<T extends { id: string; data: Record<string, unknown> }>(
  current: readonly T[],
  next: readonly T[],
): T[] {
  const byId = new Map(current.map((node) => [node.id, node]));
  return next.map((node) => {
    const existing = byId.get(node.id);
    if (!existing) return node;
    return {
      ...existing,
      ...node,
      data: { ...existing.data, ...node.data },
    };
  });
}

/** Server positions win; an empty server is seeded from the local backup. */
export function pickInitialPositions(
  server: PositionMap,
  local: PositionMap,
  pending = false,
  mutated = false,
): { positions: PositionMap | null; seed: PositionMap | null } {
  // A local edit happened before the server read resolved; keep the in-memory
  // map instead of letting a stale snapshot snap the graph back.
  if (mutated) {
    return { positions: null, seed: null };
  }
  // Local changes that never reached the server are newer than the server map.
  if (pending && Object.keys(local).length > 0) {
    return { positions: local, seed: local };
  }
  if (Object.keys(server).length > 0) {
    return { positions: server, seed: null };
  }
  if (Object.keys(local).length > 0) {
    return { positions: local, seed: local };
  }
  return { positions: {}, seed: null };
}

/** Serialize full-map saves so the last caller wins on the server. */
export function createPositionSaver(
  save: (positions: PositionMap) => Promise<void>,
): (positions: PositionMap) => Promise<void> {
  let chain: Promise<void> = Promise.resolve();
  return (positions) => {
    const next = chain.then(() => save(positions));
    chain = next.catch(() => {});
    return next;
  };
}
