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
  keys?: string[];
  enabled?: boolean;
  /** Per-model local overrides, keyed by model ID; preserved on every save. */
  models?: Record<string, ModelOverride>;
  probe?: ProbeResult;
}

export type ProviderApi = 'chat' | 'responses' | 'anthropic' | 'gemini';

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
  backup?: boolean;
  weight?: number;
  enabled?: boolean;
}

export interface Route {
  name: string;
  nodes: string[];
  strategy?: string;
}

export interface Entrypoint {
  name: string;
  match: string[];
  route: string;
}

export interface ConfigResponse {
  gateway: { listen: string };
  admin: { listen: string; sessionTTL: string };
  entrypoints: Entrypoint[];
  providers: Provider[];
  nodes: Node[];
  routes: Route[];
  stats?: { path?: string; retainRequests?: string; retainRollups?: string; storeBody?: boolean };
}

export interface NodeMetrics {
  requests: number;
  errors: number;
  p50Ms: number;
  p95Ms: number;
  inputTokens: number;
  outputTokens: number;
  cachedTokens: number;
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

/** One preset per upstream endpoint; it fills the type and every API base URL. */
export interface Preset {
  name: string;
  type: string;
  chat?: string;
  responses?: string;
  anthropic?: string;
  gemini?: string;
}

export interface RequestRow {
  requestId: string;
  sid: string;
  ts: string;
  model: string;
  clientModel?: string;
  provider: string;
  route?: string;
  node?: string;
  protocol: string;
  effort: string;
  status: number;
  error?: string;
  durationMs: number;
  inputTokens: number;
  outputTokens: number;
  cachedTokens: number;
  stream: boolean;
  bytesIn: number;
  bytesOut: number;
}
