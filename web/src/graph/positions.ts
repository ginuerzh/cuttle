import type { XYPosition } from '@xyflow/react';

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
