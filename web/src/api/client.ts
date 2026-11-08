import type { Alert, CaptureRule, ConfigResponse, Entrypoint, ErrorSummary, GatewayKey, GatewayKeyUsageResponse, GraphMetrics, Health, Node, Preset, ProbeResult, Provider, ProviderModel, ProviderUsageResponse, RequestRow, Route, RouteHealth, TopologyPosition } from './types';

export class ApiError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}

export class UnauthorizedError extends ApiError {
  constructor(message = 'unauthorized') {
    super(401, message);
  }
}

function csrfToken(): string {
  const match = document.cookie.match(/(?:^|;\s*)cuttle_csrf=([^;]+)/);
  return match ? decodeURIComponent(match[1]) : '';
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = {};
  if (body !== undefined) {
    headers['Content-Type'] = 'application/json';
  }
  if (method !== 'GET' && method !== 'HEAD') {
    headers['X-CSRF-Token'] = csrfToken();
  }

  const res = await fetch(path, {
    method,
    credentials: 'same-origin',
    headers,
    body: body === undefined ? undefined : JSON.stringify(body),
  });

  if (res.status === 401) {
    throw new UnauthorizedError();
  }
  if (!res.ok) {
    let message = `HTTP ${res.status}`;
    try {
      const data = await res.json();
      if (data && typeof data.error === 'string') {
        message = data.error;
      }
    } catch {
      /* keep the status message */
    }
    throw new ApiError(res.status, message);
  }
  if (res.status === 204) {
    return undefined as T;
  }
  const text = await res.text();
  if (!text.trim()) {
    return undefined as T;
  }
  return JSON.parse(text) as T;
}

export function getHealth(): Promise<Health> {
  return request<Health>('GET', '/api/health');
}

export async function getConfig(): Promise<ConfigResponse> {
  const cfg = await request<ConfigResponse>('GET', '/api/config');
  return {
    ...cfg,
    entrypoints: cfg.entrypoints ?? [],
    providers: cfg.providers ?? [],
    nodes: cfg.nodes ?? [],
    routes: cfg.routes ?? [],
  };
}

export function getGraph(from: Date, to: Date): Promise<GraphMetrics> {
  const q = new URLSearchParams({
    from: from.toISOString(),
    to: to.toISOString(),
  });
  return request<GraphMetrics>('GET', `/api/stats/graph?${q.toString()}`);
}

export function getTopologyPositions(): Promise<Record<string, TopologyPosition>> {
  return request<{ positions?: Record<string, TopologyPosition> }>(
    'GET',
    '/api/topology/positions',
  ).then((data) => data.positions ?? {});
}

export function putTopologyPositions(
  positions: Record<string, TopologyPosition>,
): Promise<void> {
  return request<void>('PUT', '/api/topology/positions', { positions });
}

export async function login(password: string, remember = false): Promise<void> {
  await request<void>('POST', '/api/login', { password, remember });
}

export async function logout(): Promise<void> {
  await request<void>('POST', '/api/logout');
}

export function getPresets(): Promise<Preset[]> {
  return request<Preset[]>('GET', '/api/presets');
}

export function createProvider(p: Partial<Provider>): Promise<void> {
  return request<void>('POST', '/api/providers', p);
}

export function updateProvider(name: string, p: Partial<Provider>): Promise<void> {
  return request<void>('PUT', `/api/providers/${encodeURIComponent(name)}`, p);
}

export function deleteProvider(name: string): Promise<void> {
  return request<void>('DELETE', `/api/providers/${encodeURIComponent(name)}`);
}

export function listNodes(): Promise<Node[]> {
  return request<Node[]>('GET', '/api/nodes');
}

export function createNode(n: Partial<Node>): Promise<void> {
  return request<void>('POST', '/api/nodes', n);
}

export function updateNode(name: string, n: Partial<Node>): Promise<void> {
  return request<void>('PUT', `/api/nodes/${encodeURIComponent(name)}`, n);
}

export function deleteNode(name: string): Promise<void> {
  return request<void>('DELETE', `/api/nodes/${encodeURIComponent(name)}`);
}

export function createRoute(r: Partial<Route>): Promise<void> {
  return request<void>('POST', '/api/routes', r);
}

export function updateRoute(name: string, r: Partial<Route>): Promise<void> {
  return request<void>('PUT', `/api/routes/${encodeURIComponent(name)}`, r);
}

export function deleteRoute(name: string): Promise<void> {
  return request<void>('DELETE', `/api/routes/${encodeURIComponent(name)}`);
}

export function getRouteHealth(): Promise<RouteHealth[]> {
  return request<RouteHealth[]>('GET', '/api/routes/health');
}

export function putEntrypoints(entrypoints: Entrypoint[]): Promise<void> {
  return request<void>('PUT', '/api/entrypoints', entrypoints);
}

export function testProvider(name: string): Promise<ProbeResult> {
  return request<ProbeResult>('POST', `/api/providers/${encodeURIComponent(name)}/test`);
}

export function getProviderModels(name: string, refresh = false): Promise<ProviderModel[]> {
  const query = refresh ? '?refresh=1' : '';
  return request<ProviderModel[] | null>('GET', `/api/providers/${encodeURIComponent(name)}/models${query}`).then(
    (models) => models ?? [],
  );
}

export interface RequestQuery {
  from: Date;
  to: Date;
  provider?: string;
  route?: string;
  node?: string;
  entrypoint?: string;
  clientModel?: string;
  gatewayKey?: string;
  errorsOnly?: boolean;
  capturedOnly?: boolean;
  errorClass?: string;
  limit?: number;
}

export function getRequests(q: RequestQuery): Promise<RequestRow[]> {
  const params = new URLSearchParams({
    from: q.from.toISOString(),
    to: q.to.toISOString(),
    limit: String(q.limit ?? 100),
  });
  if (q.provider) params.set('provider', q.provider);
  if (q.route) params.set('route', q.route);
  if (q.node) params.set('node', q.node);
  if (q.entrypoint) params.set('entrypoint', q.entrypoint);
  if (q.clientModel) params.set('clientModel', q.clientModel);
  if (q.gatewayKey) params.set('key', q.gatewayKey);
  if (q.errorsOnly) params.set('errors', '1');
  if (q.capturedOnly) params.set('captured', '1');
  if (q.errorClass) params.set('errorClass', q.errorClass);
  return request<RequestRow[] | null>('GET', `/api/requests?${params.toString()}`).then((rows) => rows ?? []);
}

export function getErrorSummary(q: { from: Date; to: Date }): Promise<ErrorSummary> {
  const params = new URLSearchParams({
    from: q.from.toISOString(),
    to: q.to.toISOString(),
  });
  return request<ErrorSummary>('GET', `/api/stats/errors?${params.toString()}`);
}

export function getAlerts(): Promise<Alert[]> {
  return request<Alert[] | null>('GET', '/api/alerts').then((alerts) => alerts ?? []);
}

export function getRequest(id: string): Promise<RequestRow> {
  return request<RequestRow>('GET', `/api/requests/${encodeURIComponent(id)}`);
}

export function getProviderUsage(q: { from: Date; to: Date; refresh?: boolean }): Promise<ProviderUsageResponse> {
  const params = new URLSearchParams({
    from: q.from.toISOString(),
    to: q.to.toISOString(),
  });
  if (q.refresh) params.set('refresh', '1');
  return request<ProviderUsageResponse>('GET', `/api/provider-usage?${params.toString()}`).then((r) => ({
    ...r,
    providers: (r.providers ?? []).map((p) => ({ ...p, accounts: p.accounts ?? [] })),
  }));
}

export function listGatewayKeys(): Promise<GatewayKey[]> {
  return request<GatewayKey[] | null>('GET', '/api/gateway-keys').then((keys) => keys ?? []);
}

export function createGatewayKey(input: {
  name: string;
  enabled?: boolean;
}): Promise<{ name: string; key: string }> {
  return request<{ name: string; key: string }>('POST', '/api/gateway-keys', input);
}

export function updateGatewayKey(name: string, input: { enabled: boolean }): Promise<void> {
  return request<void>('PUT', `/api/gateway-keys/${encodeURIComponent(name)}`, input);
}

export function rotateGatewayKey(name: string): Promise<{ name: string; key: string }> {
  return request<{ name: string; key: string }>(
    'POST',
    `/api/gateway-keys/${encodeURIComponent(name)}/rotate`,
  );
}

export function deleteGatewayKey(name: string): Promise<void> {
  return request<void>('DELETE', `/api/gateway-keys/${encodeURIComponent(name)}`);
}

export function getGatewayKeyUsage(q: { from: Date; to: Date }): Promise<GatewayKeyUsageResponse> {
  const params = new URLSearchParams({
    from: q.from.toISOString(),
    to: q.to.toISOString(),
  });
  return request<GatewayKeyUsageResponse>('GET', `/api/gateway-key-usage?${params.toString()}`).then(
    (r) => ({
      ...r,
      totals: r.totals ?? {
        requests: 0,
        errors: 0,
        inputTokens: 0,
        outputTokens: 0,
        cachedTokens: 0,
      },
      keys: r.keys ?? [],
    }),
  );
}

export function listCaptureRules(): Promise<CaptureRule[]> {
  return request<CaptureRule[] | null>('GET', '/api/captures').then((rules) => rules ?? []);
}

export function createCaptureRule(input: {
  route?: string;
  clientModel?: string;
  reason: string;
  limit: number;
  ttlSeconds: number;
}): Promise<CaptureRule> {
  return request<CaptureRule>('POST', '/api/captures', input);
}

export function deleteCaptureRule(id: string): Promise<void> {
  return request<void>('DELETE', `/api/captures/${encodeURIComponent(id)}`);
}

export function changePassword(oldPassword: string, newPassword: string): Promise<void> {
  return request<void>('PUT', '/api/admin/password', { oldPassword, newPassword });
}
