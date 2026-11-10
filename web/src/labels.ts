import type { ConfigResponse } from './api/types';
import { splitId } from './pages/selection';

/** An entity id to display-name lookup. */
export type LabelMap = ReadonlyMap<string, string>;

/** Build an id to name map from anything that carries both. */
export function labelMap(entities?: readonly { id: string; name: string }[]): LabelMap {
  return new Map((entities ?? []).map((entity) => [entity.id, entity.name]));
}

/**
 * The display name for an entity id. A display name is only a label: an id the
 * config no longer lists (deleted, or only known from stats) falls back to the
 * raw id so nothing disappears from the console. An absent id renders empty.
 */
export function entityLabel(id: string | undefined, names: LabelMap): string {
  if (!id) return '';
  return names.get(id) || id;
}

/** Every id to name map the console resolves display labels from. */
export interface ConfigLabels {
  providers: LabelMap;
  nodes: LabelMap;
  routes: LabelMap;
  entrypoints: LabelMap;
  providerKeys: LabelMap;
}

/** Build every label map once per config load. */
export function configLabels(config: ConfigResponse): ConfigLabels {
  return {
    providers: labelMap(config.providers),
    nodes: labelMap(config.nodes),
    routes: labelMap(config.routes),
    entrypoints: labelMap(config.entrypoints),
    providerKeys: labelMap(config.providers.flatMap((provider) => provider.keys ?? [])),
  };
}

/**
 * The display name of a graph node id (`kind:id`). The topology keeps ids in
 * its node keys and takes the title from the config, so an unknown kind or id
 * stays readable as the raw id.
 */
export function graphLabel(id: string, labels: ConfigLabels): string {
  const [kind, entityId] = splitId(id);
  switch (kind) {
    case 'provider':
      return entityLabel(entityId, labels.providers);
    case 'node':
      return entityLabel(entityId, labels.nodes);
    case 'route':
      return entityLabel(entityId, labels.routes);
    case 'entrypoint':
      return entityLabel(entityId, labels.entrypoints);
    default:
      return id;
  }
}
