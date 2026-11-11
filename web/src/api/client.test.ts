import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  createCaptureRule,
  createGatewayKey,
  createProvider,
  deleteGatewayKey,
  deleteNode,
  deleteProvider,
  deleteRoute,
  getAttempts,
  getGatewayKeyUsage,
  getProviderModels,
  getProviderUsage,
  getRequests,
  getSeries,
  getTopologyPositions,
  listCaptureRules,
  putStatsCapture,
  rotateGatewayKey,
  testProvider,
  updateGatewayKey,
  updateNode,
  updateProvider,
  updateRoute,
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

it('requests the session series with the bucket and the list filters', async () => {
  vi.stubGlobal('document', { cookie: '' });
  const fetchMock = vi.fn(async () =>
    new Response(JSON.stringify({ bucket: '2m0s', points: [] }), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    }),
  );
  vi.stubGlobal('fetch', fetchMock);
  const from = new Date(Date.UTC(2026, 10, 11, 10, 0, 0));
  const to = new Date(from.getTime() + 60 * 60_000);

  await getSeries({ from, to, bucket: 120_000, node: 'n1', errorsOnly: true });

  const url = String((fetchMock.mock.calls[0] as unknown as [string])[0]);
  expect(url).toContain('/api/stats/series?');
  expect(url).toContain('bucket=120000ms');
  expect(url).toContain('node=n1');
  expect(url).toContain('errors=1');
  expect(url).toContain(`from=${encodeURIComponent(from.toISOString())}`);
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

it('addresses every entity mutation and lookup by id', async () => {
  vi.stubGlobal('document', { cookie: '' });
  const fetchMock = vi.fn(async () => new Response(null, { status: 204 }));
  vi.stubGlobal('fetch', fetchMock);

  await updateProvider('p_3k9x2m4q7a', { name: 'DeepSeek' });
  await deleteProvider('p_3k9x2m4q7a');
  await testProvider('p_3k9x2m4q7a');
  await getProviderModels('p_3k9x2m4q7a', true);
  await updateNode('n_3k9x2m4q7a', { name: 'fast' });
  await deleteNode('n_3k9x2m4q7a');
  await updateRoute('r_3k9x2m4q7a', { name: 'fast' });
  await deleteRoute('r_3k9x2m4q7a');
  await updateGatewayKey('gk_3k9x2m4q7a', { enabled: false });
  await rotateGatewayKey('gk_3k9x2m4q7a');
  await deleteGatewayKey('gk_3k9x2m4q7a');

  const calls = fetchMock.mock.calls.map((call) => {
    const [path, init] = call as unknown as [string, RequestInit];
    return `${init.method} ${path}`;
  });
  expect(calls).toEqual([
    'PUT /api/providers/p_3k9x2m4q7a',
    'DELETE /api/providers/p_3k9x2m4q7a',
    'POST /api/providers/p_3k9x2m4q7a/test',
    'GET /api/providers/p_3k9x2m4q7a/models?refresh=1',
    'PUT /api/nodes/n_3k9x2m4q7a',
    'DELETE /api/nodes/n_3k9x2m4q7a',
    'PUT /api/routes/r_3k9x2m4q7a',
    'DELETE /api/routes/r_3k9x2m4q7a',
    'PUT /api/gateway-keys/gk_3k9x2m4q7a',
    'POST /api/gateway-keys/gk_3k9x2m4q7a/rotate',
    'DELETE /api/gateway-keys/gk_3k9x2m4q7a',
  ]);
});

it('builds the attempts query from the present filters only', async () => {
  vi.stubGlobal('document', { cookie: '' });
  const fetchMock = vi.fn(async () => new Response('[]', { status: 200, headers: { 'Content-Type': 'application/json' } }));
  vi.stubGlobal('fetch', fetchMock);

  await getAttempts({
    from: new Date('2026-11-11T00:00:00Z'),
    to: new Date('2026-11-11T01:00:00Z'),
    limit: 50,
    node: 'n_0000000001',
    status: 429,
  });

  const url = String((fetchMock.mock.calls[0] as unknown[])[0]);
  expect(url.startsWith('/api/attempts?')).toBe(true);
  const params = new URL(url, 'http://localhost').searchParams;
  expect(params.get('from')).toBe('2026-11-11T00:00:00.000Z');
  expect(params.get('to')).toBe('2026-11-11T01:00:00.000Z');
  expect(params.get('limit')).toBe('50');
  expect(params.get('node')).toBe('n_0000000001');
  expect(params.get('status')).toBe('429');
  expect(params.get('route')).toBeNull();
  expect(params.get('provider')).toBeNull();
  expect(params.get('errorClass')).toBeNull();
  expect(params.get('offset')).toBeNull();
});

it('omits the attempt status filter for 0, which means any status', async () => {
  vi.stubGlobal('document', { cookie: '' });
  const fetchMock = vi.fn(async () => new Response('[]', { status: 200, headers: { 'Content-Type': 'application/json' } }));
  vi.stubGlobal('fetch', fetchMock);

  await getAttempts({ from: new Date(0), to: new Date(0), status: 0 });

  const url = String((fetchMock.mock.calls[0] as unknown[])[0]);
  const params = new URL(url, 'http://localhost').searchParams;
  expect(params.get('status')).toBeNull();
  expect(params.get('limit')).toBe('100');
});

it('sends entity ids as request filters', async () => {
  vi.stubGlobal('document', { cookie: '' });
  const fetchMock = vi.fn(async () => new Response('[]', { status: 200, headers: { 'Content-Type': 'application/json' } }));
  vi.stubGlobal('fetch', fetchMock);

  await getRequests({
    from: new Date(0),
    to: new Date(0),
    provider: 'p_3k9x2m4q7a',
    route: 'r_3k9x2m4q7a',
    node: 'n_3k9x2m4q7a',
    entrypoint: 'e_3k9x2m4q7a',
    gatewayKey: 'gk_3k9x2m4q7a',
  });

  const url = String((fetchMock.mock.calls[0] as unknown[])[0]);
  const params = new URL(url, 'http://localhost').searchParams;
  expect(params.get('provider')).toBe('p_3k9x2m4q7a');
  expect(params.get('route')).toBe('r_3k9x2m4q7a');
  expect(params.get('node')).toBe('n_3k9x2m4q7a');
  expect(params.get('entrypoint')).toBe('e_3k9x2m4q7a');
  expect(params.get('key')).toBe('gk_3k9x2m4q7a');
});
