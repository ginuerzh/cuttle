import type { RouteHealth } from '../api/types';

/** Per-route dead state derived from the route health payload. */
export interface NodeRouteHealth {
  /** Node ids dead in every route that reports them. */
  dead: ReadonlySet<string>;
  /** Node id -> ids of the routes where the node is dead, for nodes dead in some routes only. */
  partial: ReadonlyMap<string, readonly string[]>;
  /** `route:<routeId>|node:<nodeId>` bindings whose route reports the node dead. */
  edges: ReadonlySet<string>;
}

/**
 * Folds the per-route node markers into the three views the topology needs. A
 * node is dead only when every route that reports it is dead; a node dead in
 * some routes stays usable through the others, so it is partial instead. The
 * denominator is the routes present in the payload, so a route that skipped a
 * disabled node does not count. Nodes and routes are matched by entity id, the
 * same key the topology graph uses.
 */
export function nodeRouteHealth(health: readonly RouteHealth[]): NodeRouteHealth {
  const total = new Map<string, number>();
  const deadRoutes = new Map<string, string[]>();
  const edges = new Set<string>();
  for (const route of health) {
    for (const node of route.nodes) {
      total.set(node.id, (total.get(node.id) ?? 0) + 1);
      if (!node.dead) continue;
      edges.add(`route:${route.id}|node:${node.id}`);
      const ids = deadRoutes.get(node.id);
      if (ids) {
        ids.push(route.id);
      } else {
        deadRoutes.set(node.id, [route.id]);
      }
    }
  }

  const dead = new Set<string>();
  const partial = new Map<string, readonly string[]>();
  for (const [id, ids] of deadRoutes) {
    if (ids.length === total.get(id)) {
      dead.add(id);
    } else {
      partial.set(id, ids);
    }
  }
  return { dead, partial, edges };
}
