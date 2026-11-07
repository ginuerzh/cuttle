import type { GraphMetrics, RouteHealth } from './types';

export type SnapshotMessage = {
  type: 'snapshot';
  since: string;
  to: string;
  metrics: GraphMetrics;
  health?: RouteHealth[];
};

export type LiveMessage = SnapshotMessage | { type: 'error'; error: string };

/** Build the /api/events path. The hook adds protocol and host. */
export function eventsPath(params: {
  range?: string;
  since?: string;
  reset?: boolean;
}): string {
  const q = new URLSearchParams();
  if (params.reset) {
    q.set('reset', '1');
  } else if (params.since) {
    q.set('since', params.since);
  } else if (params.range) {
    q.set('range', params.range);
  }
  const query = q.toString();
  return query ? `/api/events?${query}` : '/api/events';
}

/** Parse one server message and reject anything outside the protocol. */
export function parseLiveMessage(data: string): LiveMessage | null {
  let value: unknown;
  try {
    value = JSON.parse(data);
  } catch {
    return null;
  }
  if (typeof value !== 'object' || value === null) {
    return null;
  }
  const msg = value as Record<string, unknown>;
  if (msg.type === 'snapshot') {
    if (typeof msg.since !== 'string' || typeof msg.to !== 'string') {
      return null;
    }
    if (typeof msg.metrics !== 'object' || msg.metrics === null || Array.isArray(msg.metrics)) {
      return null;
    }
    if (msg.health !== undefined && !Array.isArray(msg.health)) {
      return null;
    }
    return {
      type: 'snapshot',
      since: msg.since,
      to: msg.to,
      metrics: msg.metrics as GraphMetrics,
      ...(Array.isArray(msg.health) ? { health: msg.health as RouteHealth[] } : {}),
    };
  }
  if (msg.type === 'error') {
    if (typeof msg.error !== 'string') {
      return null;
    }
    return { type: 'error', error: msg.error };
  }
  return null;
}
