import { describe, expect, it } from 'vitest';
import { connectNodeProvider, disconnectEntrypointRoute, disconnectNodeProvider, planNodeProviderConnect } from './drafts';
import type { Entrypoint, Node, Provider } from '../api/types';

const provider: Provider = {
  id: 'p_0000000001',
  name: 'p',
  type: 'generic',
  chat: 'https://a.example/v1',
  keys: [{ id: 'pk_0000000001', name: 'k', keyPreview: 'sk-...a', enabled: true }],
};

const draft: Node = { id: 'n_0000000001', name: 'draft', provider: '', model: '', key: '' };

describe('planNodeProviderConnect', () => {
  it('asks for a model instead of saving when the node has none', () => {
    const { next, needsModel } = planNodeProviderConnect({ ...draft, model: '' }, provider);

    expect(next.provider).toBe('p_0000000001');
    expect(needsModel).toBe(true);
  });

  it('saves directly when the node already has a model', () => {
    const { needsModel } = planNodeProviderConnect({ ...draft, model: 'm1' }, provider);

    expect(needsModel).toBe(false);
  });
});

describe('connectNodeProvider', () => {
  it('binds the provider and drops a key that does not belong to it', () => {
    const pinned: Node = { ...draft, key: 'pk_0000000002' };
    const next = connectNodeProvider(pinned, provider);
    expect(next.provider).toBe('p_0000000001');
    expect(next.key).toBe('');
  });

  it('keeps a key the new provider owns and keeps the model hint', () => {
    const kept: Node = { ...draft, key: 'pk_0000000001', model: 'm1' };
    const next = connectNodeProvider(kept, provider);
    expect(next.key).toBe('pk_0000000001');
    expect(next.model).toBe('m1');
  });
});

describe('disconnectNodeProvider', () => {
  it('clears provider, model, api and key and keeps policy fields', () => {
    const node: Node = {
      id: 'n_0000000001',
      name: 'n',
      provider: 'p_0000000001',
      model: 'm1',
      api: 'chat',
      key: 'pk_0000000001',
      backup: true,
      weight: 3,
    };
    const next = disconnectNodeProvider(node);
    expect(next).toMatchObject({ id: 'n_0000000001', name: 'n', provider: '', model: '', key: '', backup: true, weight: 3 });
    expect(next.api).toBeUndefined();
  });
});

describe('disconnectEntrypointRoute', () => {
  it('clears the route and keeps match order', () => {
    const e: Entrypoint = { id: 'e_0000000001', name: 'e', match: ['a*', 'b*'], route: 'r_0000000001' };
    expect(disconnectEntrypointRoute(e)).toMatchObject({ id: 'e_0000000001', match: ['a*', 'b*'], route: '' });
  });
});
