import { describe, expect, it } from 'vitest';
import { buildGraph } from '../graph/build';
import type { ConfigResponse } from '../api/types';
import { aliasFromNodeId } from './selection';

const cfg: Pick<ConfigResponse, 'aliases' | 'routes' | 'providers' | 'nodes'> = {
  aliases: [
    { match: 'claude-opus*', model: 'strong' },
    { match: 'gpt-4.5*', model: 'fast' },
  ],
  providers: [{ name: 'p1', type: 'deepseek', chat: 'https://a/v1', keysSet: 1, keyPreviews: [] }],
  nodes: [{ name: 'p1', provider: 'p1', models: { strong: 'm1' } }],
  routes: [
    { name: 'strong', nodes: ['p1'], match: { model: 'strong' } },
    { name: 'fast', nodes: ['p1'], match: { model: 'fast' } },
  ],
};

describe('aliasFromNodeId', () => {
  it('resolves the alias node id drawn on the canvas', () => {
    const { nodes } = buildGraph(cfg);
    const aliasNode = nodes.find((n) => n.id === 'alias:claude-opus*');
    expect(aliasNode).toBeTruthy();

    const found = aliasFromNodeId(cfg.aliases, aliasNode!.id);
    expect(found?.index).toBe(0);
    expect(found?.alias.model).toBe('strong');
  });

  it('returns null for an unknown or non-alias id', () => {
    expect(aliasFromNodeId(cfg.aliases, 'alias:missing*')).toBeNull();
    expect(aliasFromNodeId(cfg.aliases, 'route:strong')).toBeNull();
  });
});
