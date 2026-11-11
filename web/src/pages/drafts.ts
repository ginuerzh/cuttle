import type { Entrypoint, Node, Provider } from '../api/types';

/**
 * Binds a node to a provider. A pinned key belongs to its provider, so a key
 * the new provider does not own is cleared; the model text is kept as a hint
 * for the operator to confirm against the new catalog.
 */
export function connectNodeProvider(node: Node, provider: Provider): Node {
  const owned = provider.keys?.some((k) => k.id === node.key) ?? false;
  return { ...node, provider: provider.id, key: owned ? node.key : '' };
}

/** Turns a node back into a draft: nothing that pointed at the provider survives. */
export function disconnectNodeProvider(node: Node): Node {
  return { ...node, provider: '', model: '', key: '', api: undefined };
}

/** Turns an entrypoint back into a draft: the model globs and order stay. */
export function disconnectEntrypointRoute(e: Entrypoint): Entrypoint {
  return { ...e, route: '' };
}
