import type { RouteHealth } from '../api/types';

/** Per-route dead state derived from the route health payload. */
export interface NodeRouteHealth {
  /** Config node names dead in every route that reports them. */
  dead: ReadonlySet<string>;
  /** Config node name -> dead route names, for nodes dead in some routes only. */
  partial: ReadonlyMap<string, readonly string[]>;
  /** `route:<route>|node:<node>` bindings whose route reports the node dead. */
  edges: ReadonlySet<string>;
}

/**
 * Folds the per-route node markers into the three views the topology needs. A
 * node is dead only when every route that reports it is dead; a node dead in
 * some routes stays usable through the others, so it is partial instead. The
 * denominator is the routes present in the payload, so a route that skipped a
 * disabled node does not count.
 */
export function nodeRouteHealth(health: readonly RouteHealth[]): NodeRouteHealth {
  const total = new Map<string, number>();
  const deadRoutes = new Map<string, string[]>();
  const edges = new Set<string>();
  for (const route of health) {
    for (const node of route.nodes) {
      total.set(node.name, (total.get(node.name) ?? 0) + 1);
      if (!node.dead) continue;
      edges.add(`route:${route.name}|node:${node.name}`);
      const names = deadRoutes.get(node.name);
      if (names) {
        names.push(route.name);
      } else {
        deadRoutes.set(node.name, [route.name]);
      }
    }
  }

  const dead = new Set<string>();
  const partial = new Map<string, readonly string[]>();
  for (const [name, names] of deadRoutes) {
    if (names.length === total.get(name)) {
      dead.add(name);
    } else {
      partial.set(name, names);
    }
  }
  return { dead, partial, edges };
}
