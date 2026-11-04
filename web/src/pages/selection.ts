import type { Alias } from '../api/types';

export function splitId(id: string): [string, string] {
  const i = id.indexOf(':');
  if (i < 0) return [id, ''];
  return [id.slice(0, i), id.slice(i + 1)];
}

/**
 * Graph nodes are `alias:<glob match>`, not `alias:<index>`: the match is what
 * identifies an alias in the config. Resolve it back to its list entry so the
 * inspector edits the alias that was clicked, even after reordering.
 */
export function aliasFromNodeId(
  aliases: Alias[],
  id: string,
): { alias: Alias; index: number } | null {
  const [kind, match] = splitId(id);
  if (kind !== 'alias') return null;
  const index = aliases.findIndex((a) => a.match === match);
  return index < 0 ? null : { alias: aliases[index], index };
}
