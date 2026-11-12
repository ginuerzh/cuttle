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

/** One configured provider key as the console sees it; the value is masked. */
export interface ProviderKeyView {
  /** Immutable id; the handle nodes pin and key updates carry. */
  id: string;
  /** Display label. */
  name: string;
  keyPreview: string;
  enabled: boolean;
}

/** One write-only provider key in a create or full-replace payload. */
export interface ProviderKeyInput {
  /** Stored key id; absent for a key that is being added. */
  id?: string;
  name: string;
  key: string;
  enabled?: boolean;
}

export interface Provider {
  /** Immutable id; the handle every API path and association uses. */
  id: string;
  /** Display label. */
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
  keys: ProviderKeyView[];
  auth?: AuthConfig;
  health?: HealthConfig;
  tls?: Record<string, unknown>;
  /** Overrides gateway.userAgent for this provider. */
  userAgent?: string;
  /** Extra upstream request headers; auth.extra is applied after these. */
  headers?: Record<string, string>;
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
  /** Immutable id; the handle every API path and association uses. */
  id: string;
  /** Display label. */
  name: string;
  /** Provider id this node binds; empty while drafting (it serves no traffic). */
  provider: string;
  model: string;
  /** Upstream API; 'auto' or absent matches the client protocol. */
  api?: NodeAPI;
  /** Optional provider key id this node pins; absent spreads across every enabled key. */
  key?: string;
  backup?: boolean;
  weight?: number;
  enabled?: boolean;
}

export interface Route {
  /** Immutable id; the handle every API path and association uses. */
  id: string;
  /** Display label. */
  name: string;
  /** Node ids in selection order. */
  nodes: string[];
  strategy?: string;
  maxFails?: number;
  failTimeout?: string;
  /** Extra attempts for retryable responses; absent means no retries. */
  retries?: number;
  /** Comma-separated status codes, e.g. `429,5xx`. */
  retryCodes?: string;
}

export interface RouteNodeHealth {
  /** Node id and provider id; the names are display labels only. */
  id: string;
  name: string;
  providerId?: string;
  failCount: number;
  lastFailure?: string | null;
  dead: boolean;
  deadUntil?: string | null;
}

export interface RouteHealth {
  /** Route id; the name is a display label. */
  id: string;
  name: string;
  maxFails: number;
  failTimeout: string;
  nodes: RouteNodeHealth[];
}

/** One command probe on a binding: exit 0 makes the binding eligible. */
export interface RouteBindingProbe {
  /** Shell command; the runner uses `sh -c` (`cmd /C` on Windows). Never env-expanded. */
  command: string;
  /** Go duration string between runs; the server default is 30s. */
  interval?: string;
  /** Go duration string for one run; the server default is 5s. */
  timeout?: string;
}

/** One ordered route binding of an entrypoint. */
export interface EntrypointRoute {
  /** Route id this binding points at; it is never a display name. */
  route: string;
  /** Optional eligibility probe; absent means always eligible, so only the last binding may omit it. */
  probe?: RouteBindingProbe;
}

export interface Entrypoint {
  /** Immutable id; the handle every API path and association uses. */
  id: string;
  /** Display label. */
  name: string;
  match: string[];
  /** Ordered route bindings; the first eligible one serves. Empty means a draft. */
  routes: EntrypointRoute[];
}

/** One probe result of a binding; `last` is absent before the first run. */
export interface EntrypointHealthProbe {
  at: string;
  ok: boolean;
  /** -1 when the command could not be started or timed out. */
  exitCode: number;
  latencyMs: number;
  /** Stderr tail of the failed run. */
  error?: string;
}

/** One binding's health; `routeId`/`routeName` identify the bound route. */
export interface EntrypointHealthBinding {
  /** Position in the configured binding list. */
  index: number;
  routeId: string;
  routeName: string;
  /** Marker state read with the same rule the data plane uses. */
  eligible: boolean;
  last?: EntrypointHealthProbe;
}

/** The binding health of one entrypoint, in configuration order. */
export interface EntrypointHealth {
  entrypointId: string;
  entrypointName: string;
  /** The binding that serves the next request; `fallback` means no binding is eligible. */
  active: { routeId: string; fallback: boolean };
  bindings: EntrypointHealthBinding[];
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
  /** Discarded upstream attempts in the window; one per retried response. */
  attempts: number;
  /** Distinct client requests that had at least one discarded attempt. */
  retriedRequests: number;
  /** Discarded attempts by upstream status; `"0"` is an attempt that failed before a response. */
  attemptStatus?: Record<string, number>;
  /** Final failed requests by status; `"0"` is a failed dial, so a node with retries disabled still has a number. */
  failureStatus?: Record<string, number>;
  /** Newest failed request of the window (unix ms); absent when none failed. */
  lastFailureTs?: number;
  /** Status of that failure; `0` is a failed dial. */
  lastFailureStatus: number;
  /** Newest discarded attempt of the window (unix ms); absent when there was none. */
  lastAttemptTs?: number;
  /** Status of that attempt; `0` is a failed dial. */
  lastAttemptStatus: number;
  p50Ms: number;
  p95Ms: number;
  inputTokens: number;
  outputTokens: number;
  cachedTokens: number;
  outputTokensPerSecond: number;
}

/** One bucket of GET /api/stats/series; `bucket` is the bucket start. */
export interface SeriesPoint {
  bucket: string;
  requests: number;
  errors: number;
}

/** GET /api/stats/series: `bucket` is the width (a Go duration string). */
export interface SeriesResponse {
  bucket: string;
  points: SeriesPoint[];
}

export interface EdgeMetrics {
  requests: number;
  errors: number;
  /** Discarded upstream attempts that crossed this edge. */
  attempts: number;
  /** Distinct client requests behind those attempts. */
  retriedRequests: number;
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
  truncatedFields?: string[];
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
  /** Provider id recorded with the request; `providerName` is its label. */
  provider: string;
  /** Route id recorded with the request; `routeName` is its label. */
  route?: string;
  /** Gateway key id that authenticated the request, when keys are enabled. */
  gatewayKey?: string;
  /** Node id recorded with the request; `nodeName` is its label. */
  node?: string;
  /** Provider display label resolved by the admin API. */
  providerName: string;
  /** Node display label resolved by the admin API. */
  nodeName: string;
  /** Route display label resolved by the admin API. */
  routeName: string;
  /** Gateway key display label resolved by the admin API. */
  gatewayKeyName: string;
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
  /** Retry attempts made before the recorded final attempt. */
  retries?: number;
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
  /** Discarded attempts of this request, present on the detail only. */
  attempts?: AttemptRow[];
}

/** One discarded upstream attempt: it opted into a retry, so the client never saw it. */
export interface AttemptRow {
  /** Parent request id; the console keys by it and opens the request detail. */
  requestId: string;
  /** Upstream attempt index of the parent request; the served attempt is not listed. */
  attempt: number;
  /** The parent request's start time; an attempt is never timed on its own. */
  ts: string;
  /** Route id the attempt ran under; `routeName` is its label. */
  route?: string;
  routeName: string;
  /** Node id that served the attempt; `nodeName` is its label. */
  node?: string;
  nodeName: string;
  /** Provider id of that node; `providerName` is its label. */
  provider?: string;
  providerName: string;
  /** Gateway key id that authenticated the parent request. */
  gatewayKey?: string;
  /** Upstream status; 0 means the attempt failed before a response. */
  status: number;
  error?: string;
  errorClass?: string;
  durationMs: number;
  bytesIn: number;
  bytesOut: number;
  upstream?: string;
  bodyPreview?: string;
  responseHeaders?: Record<string, string>;
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
  name?: string;
  keyPreview?: string;
  enabled?: boolean;
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
  /** Provider id; `name` is its current display label. */
  id: string;
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
  /** Immutable id; the handle every API path and filter uses. */
  id: string;
  /** Display label. */
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

/** The one-time create/rotate response; the raw value appears only here. */
export interface GatewayKeySecret {
  id: string;
  name: string;
  key: string;
}

/** Per-key usage row, including keys that were deleted but still have history. */
export interface GatewayKeyUsage extends KeyUsageCounts {
  /** Key id the rollups are joined by; `name` is the display label. */
  id: string;
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
