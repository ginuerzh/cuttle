import type { RouteHealth } from '../api/types';

/**
 * Config node names marked dead by every route that references them. A node
 * shared by several routes stays healthy as long as any route can still use
 * it; the Routes page shows the per-route state.
 */
export function deadNodeNames(health: readonly RouteHealth[]): Set<string> {
  const total = new Map<string, number>();
  const dead = new Map<string, number>();
  for (const route of health) {
    for (const node of route.nodes) {
      total.set(node.name, (total.get(node.name) ?? 0) + 1);
      if (node.dead) dead.set(node.name, (dead.get(node.name) ?? 0) + 1);
    }
  }

  const names = new Set<string>();
  for (const [name, count] of dead) {
    if (count === total.get(name)) names.add(name);
  }
  return names;
}
