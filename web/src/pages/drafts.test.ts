import { describe, expect, it } from 'vitest';
import {
  connectEntrypointRoute,
  connectNodeProvider,
  disconnectEntrypointBinding,
  disconnectNodeProvider,
  planNodeProviderConnect,
} from './drafts';
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

const entrypoint: Entrypoint = {
  id: 'e_0000000001',
  name: 'e',
  match: ['a*', 'b*'],
  routes: [{ route: 'r_0000000001', probe: { command: 'true', interval: '30s', timeout: '5s' } }],
};

describe('connectEntrypointRoute', () => {
  it('adds a probe-less binding after a probed one and keeps the configured order', () => {
    const next = connectEntrypointRoute(entrypoint, 'r_0000000002');
    expect(next.routes.map((b) => b.route)).toEqual(['r_0000000001', 'r_0000000002']);
    // The new binding is the last one, so it is the only one that may omit a probe.
    expect(next.routes[1].probe).toBeUndefined();
    expect(next.routes[0].probe?.command).toBe('true');
    expect(entrypoint.routes).toHaveLength(1);
  });

  it('inserts before a trailing probe-less binding so the new one is reachable', () => {
    const bound: Entrypoint = {
      ...entrypoint,
      routes: [{ route: 'r_0000000001' }, { route: 'r_0000000002' }],
    };
    const next = connectEntrypointRoute(bound, 'r_0000000003');
    expect(next.routes.map((b) => b.route)).toEqual(['r_0000000001', 'r_0000000003', 'r_0000000002']);
    expect(next.routes[1].probe).toBeUndefined();
    expect(bound.routes.map((b) => b.route)).toEqual(['r_0000000001', 'r_0000000002']);
  });

  it('is a no-op for a route the entrypoint already binds', () => {
    const next = connectEntrypointRoute(entrypoint, 'r_0000000001');
    expect(next).toBe(entrypoint);
  });

  it('connects the first route of a draft without touching the globs', () => {
    const next = connectEntrypointRoute({ ...entrypoint, routes: [] }, 'r_0000000002');
    expect(next.routes).toEqual([{ route: 'r_0000000002' }]);
    expect(next.match).toEqual(['a*', 'b*']);
  });
});

describe('disconnectEntrypointBinding', () => {
  it('removes only the named binding', () => {
    const bound: Entrypoint = {
      ...entrypoint,
      routes: [{ route: 'r_0000000001' }, { route: 'r_0000000002' }],
    };
    const next = disconnectEntrypointBinding(bound, 'r_0000000001');
    expect(next.routes).toEqual([{ route: 'r_0000000002' }]);
  });

  it('keeps the entrypoint untouched when the route is not bound', () => {
    expect(disconnectEntrypointBinding(entrypoint, 'r_0000000009')).toBe(entrypoint);
  });
});
