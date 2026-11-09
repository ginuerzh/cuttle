import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  createCaptureRule,
  createGatewayKey,
  createProvider,
  getGatewayKeyUsage,
  getProviderUsage,
  getRequests,
  getTopologyPositions,
  listCaptureRules,
  putStatsCapture,
} from './client';

describe('request', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('accepts an empty 201 response', async () => {
    vi.stubGlobal('document', { cookie: '' });
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => new Response(null, { status: 201 })),
    );

    await expect(createProvider({ name: 'p' })).resolves.toBeUndefined();
  });
});

it('normalizes topology positions', async () => {
  vi.stubGlobal('document', { cookie: '' });
  vi.stubGlobal(
    'fetch',
    vi.fn(async () =>
      new Response(JSON.stringify({ positions: { a: { x: 1, y: 2 } } }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    ),
  );
  await expect(getTopologyPositions()).resolves.toEqual({ a: { x: 1, y: 2 } });
});

it('loads capture rules as an array', async () => {
  vi.stubGlobal('document', { cookie: '' });
  vi.stubGlobal(
    'fetch',
    vi.fn(async () => new Response('[]', { status: 200, headers: { 'Content-Type': 'application/json' } })),
  );
  await expect(listCaptureRules()).resolves.toEqual([]);
});

it('creates a capture rule', async () => {
  vi.stubGlobal('document', { cookie: '' });
  const fetchMock = vi.fn(async () =>
    new Response(JSON.stringify({ id: 'cap-1', reason: 'test', limit: 5, used: 0 }), {
      status: 201,
      headers: { 'Content-Type': 'application/json' },
    }),
  );
  vi.stubGlobal('fetch', fetchMock);
  await expect(
    createCaptureRule({ route: 'fast', reason: 'test', limit: 5, ttlSeconds: 60 }),
  ).resolves.toMatchObject({ id: 'cap-1' });
  expect(fetchMock).toHaveBeenCalledWith(
    '/api/captures',
    expect.objectContaining({ method: 'POST' }),
  );
});

it('filters captured requests', async () => {
  vi.stubGlobal('document', { cookie: '' });
  const fetchMock = vi.fn(async () => new Response('[]', { status: 200, headers: { 'Content-Type': 'application/json' } }));
  vi.stubGlobal('fetch', fetchMock);
  await getRequests({ from: new Date(0), to: new Date(), capturedOnly: true, limit: 10 });
  const url = String((fetchMock.mock.calls[0] as unknown[])[0]);
  expect(url).toContain('captured=1');
});

it('loads provider usage and normalizes null arrays', async () => {
  vi.stubGlobal('document', { cookie: '' });
  vi.stubGlobal(
    'fetch',
    vi.fn(async () =>
      new Response(JSON.stringify({ providers: null, totals: null }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    ),
  );
  await expect(getProviderUsage({ from: new Date(0), to: new Date() })).resolves.toMatchObject({
    providers: [],
  });
});

it('passes refresh to provider usage', async () => {
  vi.stubGlobal('document', { cookie: '' });
  const fetchMock = vi.fn(async () =>
    new Response(JSON.stringify({ providers: [] }), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    }),
  );
  vi.stubGlobal('fetch', fetchMock);
  await getProviderUsage({ from: new Date(0), to: new Date(), refresh: true });
  const url = String((fetchMock.mock.calls[0] as unknown[])[0]);
  expect(url).toContain('refresh=1');
});

it('creates a gateway key', async () => {
  vi.stubGlobal('document', { cookie: '' });
  const fetchMock = vi.fn(async () =>
    new Response(JSON.stringify({ name: 'claude-code', key: 'sk-gw-abc' }), {
      status: 201,
      headers: { 'Content-Type': 'application/json' },
    }),
  );
  vi.stubGlobal('fetch', fetchMock);

  await expect(createGatewayKey({ name: 'claude-code' })).resolves.toEqual({
    name: 'claude-code',
    key: 'sk-gw-abc',
  });
  expect(fetchMock).toHaveBeenCalledWith(
    '/api/gateway-keys',
    expect.objectContaining({
      method: 'POST',
      body: JSON.stringify({ name: 'claude-code' }),
    }),
  );
});

it('loads gateway key usage', async () => {
  vi.stubGlobal('document', { cookie: '' });
  const body = {
    from: '2026-11-06T00:00:00.000Z',
    to: '2026-11-07T00:00:00.000Z',
    totals: { requests: 3, errors: 1, inputTokens: 40, outputTokens: 8, cachedTokens: 2 },
    keys: [],
  };
  vi.stubGlobal(
    'fetch',
    vi.fn(async () =>
      new Response(JSON.stringify(body), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    ),
  );

  await expect(
    getGatewayKeyUsage({ from: new Date('2026-11-06T00:00:00Z'), to: new Date('2026-11-07T00:00:00Z') }),
  ).resolves.toEqual(body);
});

it('normalizes missing gateway key usage fields', async () => {
  vi.stubGlobal('document', { cookie: '' });
  vi.stubGlobal(
    'fetch',
    vi.fn(async () =>
      new Response('{}', { status: 200, headers: { 'Content-Type': 'application/json' } }),
    ),
  );

  await expect(
    getGatewayKeyUsage({ from: new Date(0), to: new Date() }),
  ).resolves.toMatchObject({
    totals: { requests: 0, errors: 0, inputTokens: 0, outputTokens: 0, cachedTokens: 0 },
    keys: [],
  });
});

it('filters requests by gateway key', async () => {
  vi.stubGlobal('document', { cookie: '' });
  const fetchMock = vi.fn(async () => new Response('[]', { status: 200, headers: { 'Content-Type': 'application/json' } }));
  vi.stubGlobal('fetch', fetchMock);
  await getRequests({ from: new Date(0), to: new Date(), gatewayKey: 'alpha' });
  const url = String((fetchMock.mock.calls[0] as unknown[])[0]);
  expect(url).toContain('key=alpha');
});

it('updates the capture policy', async () => {
  vi.stubGlobal('document', { cookie: '' });
  const fetchMock = vi.fn(async () => new Response(null, { status: 204 }));
  vi.stubGlobal('fetch', fetchMock);
  await putStatsCapture({ mode: 'errors', sampleRate: 0.5, maxBytes: 1048576, retain: '24h' });
  expect(fetchMock).toHaveBeenCalledWith(
    '/api/stats/capture',
    expect.objectContaining({
      method: 'PUT',
      body: JSON.stringify({ mode: 'errors', sampleRate: 0.5, maxBytes: 1048576, retain: '24h' }),
    }),
  );
});
