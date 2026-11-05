import { afterEach, describe, expect, it, vi } from 'vitest';
import { createProvider } from './client';

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
