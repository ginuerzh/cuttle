/** Move one route node to a new index, preserving the rest of the order. */
export function reorderNodes(nodes: string[], from: number, to: number): string[] {
  if (from === to || from < 0 || to < 0 || from >= nodes.length || to >= nodes.length) {
    return nodes;
  }
  const next = [...nodes];
  const [item] = next.splice(from, 1);
  next.splice(to, 0, item);
  return next;
}

/** Append a node once; selecting an existing node is a no-op. */
export function addRouteNode(nodes: string[], name: string): string[] {
  return nodes.includes(name) ? nodes : [...nodes, name];
}
