export interface AuthConfig {
  header?: string;
  prefix?: string;
  keyHeaders?: { header: string; prefix?: string }[];
  extra?: Record<string, string>;
}

export interface HealthConfig {
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

export interface Provider {
  name: string;
  baseURL: string;
  protocol: string;
  keysSet: number;
  keyPreviews: string[];
  auth?: AuthConfig;
  pathMap?: Record<string, string>;
  health?: HealthConfig;
  tls?: Record<string, unknown>;
  keys?: string[];
  enabled?: boolean;
  probe?: ProbeResult;
}

export interface Node {
  name: string;
  provider: string;
  models?: Record<string, string>;
  effortModels?: Record<string, Record<string, string>>;
  tier?: string;
  backup?: boolean;
  weight?: number;
  enabled?: boolean;
}

export interface Route {
  name: string;
  nodes: string[];
  strategy?: string;
  match: { model?: string; path?: string; host?: string };
}

export interface Alias {
  match: string;
  model: string;
}

export interface ConfigResponse {
  gateway: { listen: string };
  admin: { listen: string; sessionTTL: string };
  aliases: Alias[];
  providers: Provider[];
  nodes: Node[];
  routes: Route[];
  stats?: { path?: string; retainRequests?: string; retainRollups?: string; storeBody?: boolean };
  effort?: { lowThreshold?: number; mediumThreshold?: number; highThreshold?: number };
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

export interface Preset {
  name: string;
  wire: string;
  baseURL: string;
  protocol: string;
  pathMap: Record<string, string>;
  auth?: AuthConfig;
  healthPath?: string;
}

export interface RequestRow {
  requestId: string;
  sid: string;
  ts: string;
  model: string;
  clientModel?: string;
  logicalModel?: string;
  provider: string;
  route?: string;
  tier?: string;
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
