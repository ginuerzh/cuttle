import type { RouteHealth } from '../api/types';

/** Config node names marked dead by at least one route's x marker. */
export function deadNodeNames(health: readonly RouteHealth[]): Set<string> {
  const names = new Set<string>();
  for (const route of health) {
    for (const node of route.nodes) {
      if (node.dead) names.add(node.name);
    }
  }
  return names;
}
