export type Page =
  | 'topology'
  | 'usage'
  | 'providers'
  | 'nodes'
  | 'routes'
  | 'entrypoints'
  | 'gateway-keys'
  | 'requests'
  | 'settings';

/** Left navigation order. Usage sits directly below Sessions. */
export const NAV_ORDER: readonly Page[] = [
  'topology',
  'providers',
  'nodes',
  'routes',
  'entrypoints',
  'gateway-keys',
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
  'gateway-keys': '网关密钥',
  requests: '会话',
  settings: '设置',
};
