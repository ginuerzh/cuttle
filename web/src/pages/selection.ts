import type { Entrypoint, ErrorRow } from '../api/types';

export function splitId(id: string): [string, string] {
  const i = id.indexOf(':');
  if (i < 0) return [id, ''];
  return [id.slice(0, i), id.slice(i + 1)];
}

/**
 * Graph nodes are `entrypoint:<id>`, not `entrypoint:<index>`: the entity id
 * identifies an entrypoint in the config. Resolve it back to its list entry so
 * the inspector edits the entrypoint that was clicked, even after reordering
 * or a rename.
 */
export function entrypointFromNodeId(
  entrypoints: Entrypoint[],
  id: string,
): { entrypoint: Entrypoint; index: number } | null {
  const [kind, entrypointId] = splitId(id);
  if (kind !== 'entrypoint') return null;
  const index = entrypoints.findIndex((e) => e.id === entrypointId);
  return index < 0 ? null : { entrypoint: entrypoints[index], index };
}

/** errorMatchesNodeId reports whether a graph node id owns an error row. */
export function errorMatchesNodeId(id: string, e: ErrorRow): boolean {
  const [kind, name] = splitId(id);
  switch (kind) {
    case 'provider':
      return e.provider === name;
    case 'route':
      return e.route === name;
    case 'node':
      return e.node === name;
    case 'entrypoint':
      return e.entrypoint === name;
    default:
      return false;
  }
}
