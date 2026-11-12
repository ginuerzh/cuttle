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

/**
 * The topology drag-connect outcome. The server rejects a node that carries a
 * provider without a model, so when the node has no model the connection is
 * staged in the panel instead of being saved.
 */
export function planNodeProviderConnect(node: Node, provider: Provider): { next: Node; needsModel: boolean } {
  const next = connectNodeProvider(node, provider);
  return { next, needsModel: !next.model.trim() };
}

/** Turns a node back into a draft: nothing that pointed at the provider survives. */
export function disconnectNodeProvider(node: Node): Node {
  return { ...node, provider: '', model: '', key: '', api: undefined };
}

/**
 * Adds a route binding to an entrypoint and keeps the configured order. A route
 * the entrypoint already binds is left alone: a binding is identified by its
 * route, so a second drag of the same edge must not duplicate it.
 *
 * The new binding has no probe, and only the first probe-less binding can serve,
 * so appending it after one would leave it unreachable. It is inserted before
 * the first probe-less binding instead, and appended only when there is none.
 */
export function connectEntrypointRoute(e: Entrypoint, routeID: string): Entrypoint {
  const routes = e.routes ?? [];
  if (!routeID || routes.some((b) => b.route === routeID)) return e;
  const binding = { route: routeID };
  const plain = routes.findIndex((b) => !b.probe);
  if (plain < 0) return { ...e, routes: [...routes, binding] };
  return { ...e, routes: [...routes.slice(0, plain), binding, ...routes.slice(plain)] };
}

/** Removes one route binding and leaves the rest in order. */
export function disconnectEntrypointBinding(e: Entrypoint, routeID: string): Entrypoint {
  const routes = e.routes ?? [];
  if (!routes.some((b) => b.route === routeID)) return e;
  return { ...e, routes: routes.filter((b) => b.route !== routeID) };
}
