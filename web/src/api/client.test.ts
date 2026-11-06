import { afterEach, describe, expect, it, vi } from 'vitest';
import { createCaptureRule, createProvider, getProviderUsage, getRequests, getTopologyPositions, listCaptureRules } from './client';

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
