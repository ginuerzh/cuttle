import { describe, expect, it } from 'vitest';
import { eventsPath, parseLiveMessage } from './events';

describe('eventsPath', () => {
  it('chooses reset, since or range', () => {
    expect(eventsPath({ reset: true })).toBe('/api/events?reset=1');
    expect(eventsPath({ range: '1h' })).toBe('/api/events?range=1h');

    const path = eventsPath({ since: '2026-11-06T11:00:00Z' });
    expect(new URLSearchParams(path.split('?')[1]).get('since')).toBe('2026-11-06T11:00:00Z');
  });

  it('lets reset win over range and since', () => {
    expect(eventsPath({ reset: true, range: '1h', since: '2026-11-06T11:00:00Z' })).toBe(
      '/api/events?reset=1',
    );
  });
});

describe('parseLiveMessage', () => {
  it('accepts snapshots and errors', () => {
    const snapshot = parseLiveMessage(
      JSON.stringify({
        type: 'snapshot',
        since: '2026-11-06T11:00:00Z',
        to: '2026-11-06T12:00:00Z',
        metrics: { nodes: {}, edges: {}, errors: [] },
        health: [
          {
            id: 'r_0000000001',
            name: 'r',
            maxFails: 1,
            failTimeout: '10s',
            nodes: [{ id: 'n_0000000001', name: 'n', providerId: 'p_0000000001', failCount: 1, dead: true }],
          },
        ],
      }),
    );
    expect(snapshot?.type).toBe('snapshot');
    if (snapshot?.type === 'snapshot') {
      expect(snapshot.since).toBe('2026-11-06T11:00:00Z');
      expect(snapshot.metrics.nodes).toEqual({});
      expect(snapshot.health?.[0].nodes[0].dead).toBe(true);
    }

    const error = parseLiveMessage(JSON.stringify({ type: 'error', error: 'boom' }));
    expect(error).toEqual({ type: 'error', error: 'boom' });
  });

  it('passes recent auth failures through', () => {
    const snapshot = parseLiveMessage(
      JSON.stringify({
        type: 'snapshot',
        since: '2026-11-08T11:00:00Z',
        to: '2026-11-08T12:00:00Z',
        metrics: { nodes: {}, edges: {}, errors: [] },
        authFailures: {
          recent: [
            {
              ts: '2026-11-08T11:59:00Z',
              reason: 'invalid',
              method: 'POST',
              path: '/v1/messages',
              clientIp: '192.0.2.7',
            },
          ],
          truncated: true,
        },
      }),
    );
    expect(snapshot?.type).toBe('snapshot');
    if (snapshot?.type === 'snapshot') {
      expect(snapshot.authFailures?.recent[0].reason).toBe('invalid');
      expect(snapshot.authFailures?.truncated).toBe(true);
    }

    const absent = parseLiveMessage(
      JSON.stringify({
        type: 'snapshot',
        since: 'x',
        to: 'y',
        metrics: { nodes: {}, edges: {}, errors: [] },
      }),
    );
    expect(absent?.type === 'snapshot' && absent.authFailures).toBeUndefined();
  });

  it('rejects malformed messages', () => {
    expect(parseLiveMessage('not json')).toBeNull();
    expect(parseLiveMessage(JSON.stringify({ type: 'other' }))).toBeNull();
    expect(
      parseLiveMessage(JSON.stringify({ type: 'snapshot', since: 'x', to: 'y' })),
    ).toBeNull();
    expect(
      parseLiveMessage(
        JSON.stringify({ type: 'snapshot', since: 'x', to: 'y', metrics: null }),
      ),
    ).toBeNull();
    expect(
      parseLiveMessage(
        JSON.stringify({ type: 'snapshot', since: 'x', to: 'y', metrics: {}, health: {} }),
      ),
    ).toBeNull();
    expect(
      parseLiveMessage(
        JSON.stringify({ type: 'snapshot', since: 'x', to: 'y', metrics: {}, authFailures: {} }),
      ),
    ).toBeNull();
    expect(
      parseLiveMessage(
        JSON.stringify({
          type: 'snapshot',
          since: 'x',
          to: 'y',
          metrics: {},
          authFailures: { recent: 'nope' },
        }),
      ),
    ).toBeNull();
    expect(parseLiveMessage(JSON.stringify({ type: 'error' }))).toBeNull();
  });
});
