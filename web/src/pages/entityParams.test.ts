import { beforeEach, describe, expect, it } from 'vitest';
import type { ConfigResponse } from '../api/types';
import { setActiveLang } from '../i18n';
import { entityParams } from './entityParams';

beforeEach(() => setActiveLang('zh-CN'));

const config = {
  gateway: { listen: ':8787' },
  admin: { listen: ':8788', sessionTTL: '12h' },
  providers: [
    {
      id: 'p_0000000001',
      name: 'DeepSeek 官方',
      type: 'deepseek',
      chat: 'https://api.deepseek.com/v1',
      responses: 'https://api.deepseek.com/v1/responses',
      keys: [
        { id: 'pk_0000000001', name: 'account-a', keyPreview: 'sk-…a', enabled: true },
        { id: 'pk_0000000002', name: 'account-b', keyPreview: 'sk-…b', enabled: false },
      ],
    },
  ],
  nodes: [
    {
      id: 'n_0000000001',
      name: 'ds-fast',
      provider: 'p_0000000001',
      model: 'deepseek-v4-flash',
      api: 'chat',
      key: 'pk_0000000002',
      backup: true,
      weight: 2,
      enabled: false,
    },
    { id: 'n_0000000002', name: 'ds-plain', provider: 'p_0000000001', model: 'deepseek-v4' },
    { id: 'n_0000000003', name: 'ds-stale', provider: 'p_0000000001', model: 'deepseek-v4', api: 'gemini' },
  ],
  routes: [
    {
      id: 'r_0000000001',
      name: 'strong',
      nodes: ['n_0000000001', 'n_0000000002'],
      strategy: 'sticky',
      maxFails: 2,
      failTimeout: '30s',
      retries: 2,
      retryCodes: '429,5xx',
    },
    { id: 'r_0000000002', name: 'bare', nodes: [] },
  ],
  entrypoints: [
    { id: 'e_0000000001', name: 'fast', match: ['*'], route: 'r_0000000001' },
    { id: 'e_0000000002', name: 'claude', match: ['gpt-*', 'claude-*'], route: 'r_0000000002' },
  ],
} as unknown as ConfigResponse;

describe('entityParams', () => {
  it('lists a fully configured node', () => {
    expect(entityParams(config, 'node:n_0000000001')).toEqual([
      { label: 'provider', value: 'DeepSeek 官方' },
      { label: '模型', value: 'deepseek-v4-flash' },
      { label: '目标 API（协议）', value: 'chat' },
      { label: '固定 key', value: 'account-b' },
      { label: 'backup', value: '是' },
      { label: 'weight', value: '2' },
      { label: '启用状态', value: '已禁用' },
    ]);
  });

  it('omits default node rows and names the round-robin fallback', () => {
    expect(entityParams(config, 'node:n_0000000002')).toEqual([
      { label: 'provider', value: 'DeepSeek 官方' },
      { label: '模型', value: 'deepseek-v4' },
      { label: '目标 API（协议）', value: 'auto' },
      { label: '固定 key', value: '全部 key（轮询）' },
    ]);
  });

  it('shows a pinned key id the provider no longer lists', () => {
    const stale = entityParams(
      { ...config, nodes: [{ ...config.nodes[1], key: 'pk_gone0000001' }] },
      'node:n_0000000002',
    );
    expect(stale.find((r) => r.label === '固定 key')).toEqual({ label: '固定 key', value: 'pk_gone0000001' });
  });

  it('keeps a stale target API verbatim', () => {
    expect(entityParams(config, 'node:n_0000000003')).toContainEqual({
      label: '目标 API（协议）',
      value: 'gemini',
    });
  });

  it('lists a route with retries in selection order', () => {
    expect(entityParams(config, 'route:r_0000000001')).toEqual([
      { label: 'strategy', value: 'sticky' },
      { label: '节点', value: 'ds-fast, ds-plain' },
      { label: 'maxFails', value: '2' },
      { label: 'failTimeout', value: '30s' },
      { label: 'retries', value: '2' },
      { label: 'retryCodes', value: '429,5xx' },
    ]);
  });

  it('falls back to route defaults, drops retry rows and marks an empty node list', () => {
    expect(entityParams(config, 'route:r_0000000002')).toEqual([
      { label: 'strategy', value: 'round' },
      { label: '节点', value: '—' },
      { label: 'maxFails', value: '1' },
      { label: 'failTimeout', value: '10s' },
    ]);
  });

  it('lists an entrypoint with its route name and position', () => {
    expect(entityParams(config, 'entrypoint:e_0000000002')).toEqual([
      { label: '模型匹配', value: 'gpt-*, claude-*' },
      { label: '路由', value: 'bare' },
      { label: '优先级', value: '2' },
    ]);
  });

  it('lists provider type, base URLs, key count and disabled state', () => {
    expect(
      entityParams({ ...config, providers: [{ ...config.providers[0], enabled: false }] }, 'provider:p_0000000001'),
    ).toEqual([
      { label: '类型', value: 'deepseek' },
      { label: 'chat', value: 'https://api.deepseek.com/v1' },
      { label: 'responses', value: 'https://api.deepseek.com/v1/responses' },
      { label: 'key', value: '2 个 key（1 个禁用）' },
      { label: '启用状态', value: '已禁用' },
    ]);
  });

  it('returns nothing for unrouted, unknown ids and unknown kinds', () => {
    expect(entityParams(config, 'unrouted')).toEqual([]);
    expect(entityParams(config, 'node:n_gone000001')).toEqual([]);
    expect(entityParams(config, 'route:r_gone000001')).toEqual([]);
    expect(entityParams(config, 'edge:whatever')).toEqual([]);
  });
});
