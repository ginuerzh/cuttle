export interface AuthConfig {
  header?: string;
  prefix?: string;
  keyHeaders?: { header: string; prefix?: string }[];
  extra?: Record<string, string>;
}

export interface HealthConfig {
  /** API whose base URL the probe targets; empty means the first configured API. */
  api?: ProviderApi;
  path?: string;
  interval?: string;
  timeout?: string;
}

export interface Health {
  ok: boolean;
  version: string;
  commit?: string;
  builtAt?: string;
}

export interface ProbeResult {
  ok: boolean;
  status?: number;
  latencyMs: number;
  error?: string;
  checkedAt?: string;
}

/** Local policy override for one model in a provider's catalog. */
export interface ModelOverride {
  disabled?: boolean;
}

export interface Provider {
  name: string;
  type: string;
  /** OpenAI Chat Completions base URL; empty when the provider does not serve it. */
  chat?: string;
  /** OpenAI Responses base URL; empty when the provider does not serve it. */
  responses?: string;
  /** Anthropic Messages base URL; empty when the provider does not serve it. */
  anthropic?: string;
  /** Gemini API base URL; empty when the provider does not serve it. */
  gemini?: string;
  keysSet: number;
  keyPreviews: string[];
  auth?: AuthConfig;
  health?: HealthConfig;
  tls?: Record<string, unknown>;
  /** Overrides gateway.userAgent for this provider. */
  userAgent?: string;
  /** Extra upstream request headers; auth.extra is applied after these. */
  headers?: Record<string, string>;
  keys?: string[];
  enabled?: boolean;
  /** Per-model local overrides, keyed by model ID; preserved on every save. */
  models?: Record<string, ModelOverride>;
  probe?: ProbeResult;
}

export type ProviderApi = 'chat' | 'responses' | 'anthropic' | 'gemini';

/** A node's upstream API choice; auto matches the inbound protocol. */
export type NodeAPI = ProviderApi | 'auto';

/** One entry in a provider's model catalog. */
export interface ProviderModel {
  id: string;
  name?: string;
  apis?: ProviderApi[];
  context?: number;
  input?: number;
  output?: number;
  inputs?: string[];
  outputs?: string[];
  reasoning?: boolean;
  efforts?: string[];
  free?: boolean;
  price?: { input?: number; output?: number };
  /** Locally disabled through the provider's model overrides. */
  disabled?: boolean;
}

export interface Node {
  name: string;
  provider: string;
  model: string;
  /** Upstream API; 'auto' or absent matches the client protocol. */
  api?: NodeAPI;
  backup?: boolean;
  weight?: number;
  enabled?: boolean;
}

export interface Route {
  name: string;
  nodes: string[];
  strategy?: string;
  maxFails?: number;
  failTimeout?: string;
}

export interface RouteNodeHealth {
  name: string;
  provider?: string;
  failCount: number;
  lastFailure?: string | null;
  dead: boolean;
  deadUntil?: string | null;
}

export interface RouteHealth {
  name: string;
  maxFails: number;
  failTimeout: string;
  nodes: RouteNodeHealth[];
}

export interface Entrypoint {
  name: string;
  match: string[];
  route: string;
}

export interface TopologyPosition {
  x: number;
  y: number;
}

export interface ConfigResponse {
  gateway: {
    listen: string;
    readTimeout?: string;
    userAgent?: string;
    keysSet?: number;
    authRequired?: boolean;
  };
  admin: { listen: string; sessionTTL: string };
  log?: { level?: string; format?: string };
  entrypoints: Entrypoint[];
  providers: Provider[];
  nodes: Node[];
  routes: Route[];
  stats?: { path?: string; retainRequests?: string; retainRollups?: string; capture?: { mode?: string; sampleRate?: number; maxBytes?: number; retain?: string } };
}

export interface NodeMetrics {
  requests: number;
  errors: number;
  p50Ms: number;
  p95Ms: number;
  inputTokens: number;
  outputTokens: number;
  cachedTokens: number;
  outputTokensPerSecond: number;
}

export interface EdgeMetrics {
  requests: number;
  errors: number;
}

export interface ErrorRow {
  requestId: string;
  ts: string;
  entrypoint?: string;
  route: string;
  node?: string;
  provider: string;
  status: number;
  message: string;
}

export interface GraphMetrics {
  nodes: Record<string, NodeMetrics>;
  edges: Record<string, EdgeMetrics>;
  errors: ErrorRow[];
}

/** One rejected data-plane gateway-key authentication attempt. */
export interface AuthFailure {
  ts: string;
  reason: string;
  method?: string;
  path?: string;
  clientIp?: string;
  userAgent?: string;
}

/** Bounded in-memory record streamed with the monitor snapshot. */
export interface AuthFailureSnapshot {
  recent: AuthFailure[];
  truncated?: boolean;
}

/** One preset per upstream endpoint; it fills the type and every API base URL. */
export interface Preset {
  name: string;
  type: string;
  chat?: string;
  responses?: string;
  anthropic?: string;
  gemini?: string;
}

export interface RequestCapture {
  requestOriginal?: string;
  requestRewritten?: string;
  responseOriginal?: string;
  responseRewritten?: string;
  sseFirstOriginal?: string;
  sseFirstRewritten?: string;
  sseLastOriginal?: string;
  sseLastRewritten?: string;
  requestSha256?: string;
  responseSha256?: string;
  requestHeaders?: Record<string, string>;
  responseHeaders?: Record<string, string>;
  sseEvents?: number;
  reason?: string;
  createdAt?: string;
}

export interface ErrorGroup {
  key: string;
  requests: number;
  errors: number;
}

export interface ErrorFingerprint {
  class: string;
  message: string;
  count: number;
  lastSeen: string;
}

export interface ErrorBucket {
  bucket: string;
  requests: number;
  errors: number;
}

export interface ErrorSummary {
  total: number;
  errors: number;
  errorRate: number;
  byClass: ErrorGroup[];
  byProvider: ErrorGroup[];
  byRoute: ErrorGroup[];
  byClientModel: ErrorGroup[];
  topMessages: ErrorFingerprint[];
  hourly: ErrorBucket[];
}

export interface Alert {
  id: string;
  severity: string;
  title: string;
  detail: string;
  count: number;
  since?: string;
}

export interface CaptureRule {
  id: string;
  route?: string;
  clientModel?: string;
  reason: string;
  limit: number;
  ttl: number;
  created: string;
  expires: string;
  used: number;
}

export interface RequestRow {
  requestId: string;
  sid: string;
  ts: string;
  model: string;
  clientModel?: string;
  provider: string;
  route?: string;
  /** Gateway key name that authenticated the request, when keys are enabled. */
  gatewayKey?: string;
  node?: string;
  protocol: string;
  inboundProtocol?: string;
  uri?: string;
  method?: string;
  clientIp?: string;
  userAgent?: string;
  upstream?: string;
  errorStage?: string;
  errorDirection?: string;
  errorClass?: string;
  ssePhase?: string;
  eventIndex?: number;
  effort: string;
  status: number;
  clientStatus?: number;
  error?: string;
  durationMs: number;
  inputTokens: number;
  outputTokens: number;
  cachedTokens: number;
  stream: boolean;
  finishReason?: string;
  sawDone?: boolean;
  truncated?: boolean;
  bytesIn: number;
  bytesOut: number;
  hasCapture?: boolean;
  capture?: RequestCapture;
}


export interface BalancePart {
  currency?: string;
  total: number;
  granted?: number;
  toppedUp?: number;
}

export interface QuotaWindow {
  name: string;
  usedPercent: number;
  resetsAt?: string;
}

export interface AccountUsage {
  keyPreview?: string;
  balance?: BalancePart[];
  windows?: QuotaWindow[];
  note?: string;
  error?: string;
  stale?: boolean;
  checkedAt?: string;
}

export interface ProviderUsageLocal {
  provider?: string;
  requests: number;
  errors: number;
  inputTokens: number;
  outputTokens: number;
  cachedTokens: number;
}

export interface ProviderUsage {
  name: string;
  type: string;
  enabled: boolean;
  supported: boolean;
  note?: string;
  local?: ProviderUsageLocal;
  accounts: AccountUsage[];
  checkedAt?: string;
  cached?: boolean;
}

export interface ProviderUsageResponse {
  from: string;
  to: string;
  checkedAt: string;
  totals?: ProviderUsageLocal;
  localError?: string;
  providers: ProviderUsage[];
}

/** One configured data-plane key; the raw value is never returned by the list API. */
export interface GatewayKey {
  name: string;
  keyPreview: string;
  enabled: boolean;
}

export interface KeyUsageCounts {
  requests: number;
  errors: number;
  inputTokens: number;
  outputTokens: number;
  cachedTokens: number;
}

/** Per-key usage row, including keys that were deleted but still have history. */
export interface GatewayKeyUsage extends KeyUsageCounts {
  name: string;
  configured: boolean;
  enabled: boolean;
  keyPreview?: string;
}

export interface GatewayKeyUsageResponse {
  from: string;
  to: string;
  totals: KeyUsageCounts;
  keys: GatewayKeyUsage[];
}
