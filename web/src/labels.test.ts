import { describe, expect, it } from 'vitest';
import type { ConfigResponse } from './api/types';
import { configLabels, entityLabel, labelMap } from './labels';

const config = {
  gateway: { listen: ':8787' },
  admin: { listen: ':8788', sessionTTL: '12h' },
  providers: [
    {
      id: 'p_0000000001',
      name: 'DeepSeek 官方',
      type: 'deepseek',
      keys: [{ id: 'pk_0000000001', name: 'account-a', keyPreview: 'sk-...a', enabled: true }],
    },
  ],
  nodes: [{ id: 'n_0000000001', name: 'ds-fast', provider: 'p_0000000001', model: 'm1' }],
  routes: [{ id: 'r_0000000001', name: 'strong', nodes: ['n_0000000001'] }],
  entrypoints: [{ id: 'e_0000000001', name: 'fast', match: ['*'], route: 'r_0000000001' }],
} as unknown as ConfigResponse;

describe('entityLabel', () => {
  const labels = configLabels(config);

  it('resolves a known id to its display name', () => {
    expect(entityLabel('p_0000000001', labels.providers)).toBe('DeepSeek 官方');
    expect(entityLabel('n_0000000001', labels.nodes)).toBe('ds-fast');
    expect(entityLabel('r_0000000001', labels.routes)).toBe('strong');
    expect(entityLabel('e_0000000001', labels.entrypoints)).toBe('fast');
    expect(entityLabel('pk_0000000001', labels.providerKeys)).toBe('account-a');
  });

  it('falls back to the raw id for an entity the config no longer has', () => {
    expect(entityLabel('p_deleted0001', labels.providers)).toBe('p_deleted0001');
  });

  it('renders an absent id as an empty string', () => {
    expect(entityLabel('', labels.providers)).toBe('');
    expect(entityLabel(undefined, labels.providers)).toBe('');
  });

  it('builds a map from any id/name pair list', () => {
    expect(labelMap([{ id: 'a', name: 'A' }]).get('a')).toBe('A');
    expect(labelMap().size).toBe(0);
  });
});
