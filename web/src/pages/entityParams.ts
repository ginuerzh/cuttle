import type { ConfigResponse } from '../api/types';
import { translate } from '../i18n';
import { configLabels, entityLabel } from '../labels';
import { configuredAPIs } from './presets';
import { splitId } from './selection';

/** One read-only parameter row; both sides are already translated. */
export interface ParamRow {
  label: string;
  value: string;
}

/**
 * The read-only parameters of a graph entity for the monitor panel. The rows
 * mirror what the edit forms expose and omit values that are still at their
 * default, so the panel stays short. An id the config no longer lists — an
 * `unrouted` row, a deleted entity — has no parameters at all.
 */
export function entityParams(config: ConfigResponse, graphId: string): ParamRow[] {
  const [kind, id] = splitId(graphId);
  const labels = configLabels(config);

  switch (kind) {
    case 'node': {
      const node = config.nodes.find((n) => n.id === id);
      if (!node) return [];
      const rows: ParamRow[] = [
        { label: translate('provider'), value: entityLabel(node.provider, labels.providers) },
        { label: translate('模型'), value: node.model },
        { label: translate('目标 API（协议）'), value: node.api || 'auto' },
        {
          label: translate('固定 key'),
          value: node.key ? entityLabel(node.key, labels.providerKeys) : translate('全部 key（轮询）'),
        },
      ];
      if (node.backup) rows.push({ label: translate('backup'), value: translate('是') });
      if ((node.weight ?? 1) !== 1) rows.push({ label: translate('weight'), value: String(node.weight) });
      if (node.enabled === false) rows.push({ label: translate('启用状态'), value: translate('已禁用') });
      return rows;
    }
    case 'route': {
      const route = config.routes.find((r) => r.id === id);
      if (!route) return [];
      const rows: ParamRow[] = [
        { label: translate('strategy'), value: route.strategy || 'round' },
        {
          label: translate('节点'),
          value: route.nodes.map((n) => entityLabel(n, labels.nodes)).join(', ') || '—',
        },
        { label: translate('maxFails'), value: String(route.maxFails ?? 1) },
        { label: translate('failTimeout'), value: route.failTimeout || '10s' },
      ];
      if ((route.retries ?? 0) > 0) {
        rows.push({ label: translate('retries'), value: String(route.retries) });
        rows.push({ label: translate('retryCodes'), value: route.retryCodes || '429,5xx' });
      }
      return rows;
    }
    case 'entrypoint': {
      const entrypoint = config.entrypoints.find((e) => e.id === id);
      if (!entrypoint) return [];
      const index = config.entrypoints.findIndex((e) => e.id === entrypoint.id);
      return [
        { label: translate('模型匹配'), value: entrypoint.match.join(', ') },
        { label: translate('路由'), value: entityLabel(entrypoint.route, labels.routes) },
        { label: translate('优先级'), value: String(index + 1) },
      ];
    }
    case 'provider': {
      const provider = config.providers.find((p) => p.id === id);
      if (!provider) return [];
      const keys = provider.keys ?? [];
      const disabled = keys.filter((k) => k.enabled === false).length;
      const rows: ParamRow[] = [
        { label: translate('类型'), value: provider.type },
        ...configuredAPIs(provider).map((api) => ({ label: api, value: provider[api] ?? '' })),
        {
          label: translate('key'),
          value:
            translate('{count} 个 key', { count: keys.length }) +
            (disabled > 0 ? translate('（{count} 个禁用）', { count: disabled }) : ''),
        },
      ];
      if (provider.enabled === false) rows.push({ label: translate('启用状态'), value: translate('已禁用') });
      return rows;
    }
    default:
      return [];
  }
}
