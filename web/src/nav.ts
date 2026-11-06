export type Page =
  | 'topology'
  | 'usage'
  | 'providers'
  | 'nodes'
  | 'routes'
  | 'entrypoints'
  | 'requests'
  | 'settings';

/** Left navigation order. Usage sits directly below Requests. */
export const NAV_ORDER: readonly Page[] = [
  'topology',
  'providers',
  'nodes',
  'routes',
  'entrypoints',
  'requests',
  'usage',
  'settings',
];

export const NAV_LABELS: Record<Page, string> = {
  topology: '总览',
  usage: '用量',
  providers: '提供商',
  nodes: '节点',
  routes: '路由',
  entrypoints: '入口',
  requests: '请求',
  settings: '设置',
};
