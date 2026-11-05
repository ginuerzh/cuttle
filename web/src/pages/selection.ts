import type { Entrypoint } from '../api/types';

export function splitId(id: string): [string, string] {
  const i = id.indexOf(':');
  if (i < 0) return [id, ''];
  return [id.slice(0, i), id.slice(i + 1)];
}

/**
 * Graph nodes are `entrypoint:<name>`, not `entrypoint:<index>`: the name
 * identifies an entrypoint in the config. Resolve it back to its list entry so
 * the inspector edits the entrypoint that was clicked, even after reordering.
 */
export function entrypointFromNodeId(
  entrypoints: Entrypoint[],
  id: string,
): { entrypoint: Entrypoint; index: number } | null {
  const [kind, name] = splitId(id);
  if (kind !== 'entrypoint') return null;
  const index = entrypoints.findIndex((e) => e.name === name);
  return index < 0 ? null : { entrypoint: entrypoints[index], index };
}
