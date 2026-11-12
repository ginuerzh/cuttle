import type { ConfigResponse, EntrypointHealth } from '../api/types';
import { translate } from '../i18n';
import { configLabels, entityLabel } from '../labels';
import { formatDateTime } from '../time';
import { configuredAPIs } from './presets';
import { splitEdgeId, splitId } from './selection';

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
export function entityParams(
  config: ConfigResponse,
  graphId: string,
  entrypointHealth?: readonly EntrypointHealth[],
): ParamRow[] {
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
      const rows: ParamRow[] = [{ label: translate('模型匹配'), value: entrypoint.match.join(', ') }];
      const bindings = entrypoint.routes ?? [];
      const health = entrypointHealth?.find((h) => h.entrypointId === entrypoint.id);
      const reported = new Map((health?.bindings ?? []).map((b) => [b.routeId, b]));
      if (bindings.length === 0) {
        rows.push({ label: translate('路由'), value: translate('未连接') });
      }
      bindings.forEach((binding, i) => {
        const name = entityLabel(binding.route, labels.routes) || binding.route;
        const parts = [binding.probe ? `${name} · ${binding.probe.command}` : name];
        const row = reported.get(binding.route);
        if (row) {
          parts.push(row.eligible ? translate('可用') : translate('未通过'));
          // The server sets the fallback flag when at least one binding has a
          // probe and none of the probed bindings passed. The row that serves
          // then is the first probe-less binding, which stays eligible, so the
          // fallback row is not necessarily an ineligible one.
          if (health?.active.fallback && health.active.routeId === binding.route) {
            parts.push(translate('回退'));
          }
          if (row.last) {
            parts.push(
              translate('退出码 {code}', { code: row.last.exitCode }),
              `${row.last.latencyMs}ms`,
              formatDateTime(row.last.at),
            );
            // The runner keeps the stderr head (first 1 KiB), so a failing
            // command explains itself here without a log dive.
            if (row.last.error) parts.push(row.last.error);
          }
        }
        rows.push({
          label: translate('路由 {index}', { index: i + 1 }),
          value: parts.join(' · '),
        });
      });
      rows.push({ label: translate('优先级'), value: String(index + 1) });
      return rows;
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

/**
 * The read-only parameters of a topology edge. Only an entrypoint binding edge
 * has configuration of its own (the probe); the other edges carry the node
 * mapping, which lives on the node panel. The rows mirror the edge form and
 * omit the state row until the binding has been probed at least once.
 */
export function edgeParams(
  config: ConfigResponse,
  graphId: string,
  entrypointHealth?: readonly EntrypointHealth[],
): ParamRow[] {
  const { source, target } = splitEdgeId(graphId);
  const [sourceKind, entrypointID] = splitId(source);
  const [targetKind, routeID] = splitId(target);
  if (sourceKind !== 'entrypoint' || targetKind !== 'route') return [];

  const entrypoint = config.entrypoints.find((e) => e.id === entrypointID);
  if (!entrypoint) return [];
  const bindings = entrypoint.routes ?? [];
  const index = bindings.findIndex((binding) => binding.route === routeID);
  if (index < 0) return [];

  const binding = bindings[index];
  const rows: ParamRow[] = binding.probe
    ? [
        { label: translate('探测'), value: binding.probe.command },
        { label: translate('探测间隔'), value: binding.probe.interval || '30s' },
        { label: translate('超时'), value: binding.probe.timeout || '5s' },
      ]
    : [{ label: translate('探测'), value: translate('无探测') }];

  const health = entrypointHealth?.find((h) => h.entrypointId === entrypoint.id);
  const reported = health?.bindings?.find((b) => b.routeId === routeID);
  if (reported) {
    const parts = [reported.eligible ? translate('可用') : translate('未通过')];
    if (health?.active.fallback && health.active.routeId === routeID) {
      parts.push(translate('回退'));
    }
    if (reported.last) {
      parts.push(
        translate('退出码 {code}', { code: reported.last.exitCode }),
        `${reported.last.latencyMs}ms`,
        formatDateTime(reported.last.at),
      );
      if (reported.last.error) parts.push(reported.last.error);
    }
    rows.push({ label: translate('状态'), value: parts.join(' · ') });
  }

  rows.push({ label: translate('生效顺序'), value: String(index + 1) });
  return rows;
}
