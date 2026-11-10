import { renderToStaticMarkup } from 'react-dom/server';
import { beforeEach, describe, expect, it } from 'vitest';
import { NodePanel, SessionCard, sessionQuery } from './App';
import { setActiveLang } from './i18n';

beforeEach(() => setActiveLang('zh-CN'));

const metrics = {
  requests: 10, errors: 0, p50Ms: 3, p95Ms: 5,
  inputTokens: 0, outputTokens: 80, cachedTokens: 0,
  outputTokensPerSecond: 42.34,
};

describe('NodePanel', () => {
  it('shows output speed for a binding node', () => {
    const html = renderToStaticMarkup(
      <NodePanel id="node:fast" metrics={metrics} since="" rangeMs={3_600_000} />,
    );
    expect(html).toContain('输出速度');
    expect(html).toContain('42.3 tok/s');
  });

  it('omits output speed for non-binding nodes', () => {
    const html = renderToStaticMarkup(
      <NodePanel id="provider:p" metrics={metrics} since="" rangeMs={3_600_000} />,
    );
    expect(html).not.toContain('输出速度');
  });

  it('shows recent sessions with an errors-only filter', () => {
    const html = renderToStaticMarkup(
      <NodePanel id="node:fast" metrics={metrics} since="" rangeMs={3_600_000} />,
    );

    expect(html).toContain('最近会话');
    expect(html).toContain('仅错误');
  });

  it('marks a dead node in the panel with a grey icon', () => {
    const html = renderToStaticMarkup(
      <NodePanel id="node:fast" metrics={metrics} since="" rangeMs={3_600_000} dead />,
    );

    expect(html).toContain('anticon-api');
    expect(html).toContain('var(--gw-text-muted)');
    expect(html).toContain('title="dead"');
    expect(html).not.toContain('ant-badge-status-error');
  });

  it('lists the routes where a partially dead node is dead', () => {
    const html = renderToStaticMarkup(
      <NodePanel
        id="node:fast"
        metrics={metrics}
        since=""
        rangeMs={3_600_000}
        deadRoutes={['r1', 'r3']}
      />,
    );

    expect(html).toContain('dead 路由');
    expect(html).toContain('r1, r3');
  });
});

describe('sessionQuery', () => {
  const from = new Date('2026-11-07T00:00:00Z');
  const to = new Date('2026-11-07T01:00:00Z');

  it('filters a binding node and keeps the 100-row cap', () => {
    expect(sessionQuery('node:fast', from, to, false)).toEqual({
      from, to, limit: 100, errorsOnly: false, node: 'fast',
    });
  });

  it('filters every other graph node kind', () => {
    expect(sessionQuery('route:r', from, to, true).route).toBe('r');
    expect(sessionQuery('provider:p', from, to, false).provider).toBe('p');
    expect(sessionQuery('entrypoint:e', from, to, false).entrypoint).toBe('e');
  });
});

describe('SessionCard', () => {
  it('renders the status, model, tokens and error', () => {
    const html = renderToStaticMarkup(
      <SessionCard
        row={{
          requestId: 'r1',
          sid: 's1',
          ts: '2026-11-07T10:20:30Z',
          model: 'deepseek-v4-flash',
          clientModel: 'gpt-4.5',
          provider: 'p_0000000001',
          providerName: 'p',
          route: 'r_0000000001',
          routeName: 'fast',
          node: 'n_0000000001',
          nodeName: 'fast',
          gatewayKey: 'gk_0000000001',
          gatewayKeyName: 'claude-code',
          protocol: 'chat',
          effort: '',
          status: 502,
          error: 'upstream connect failed',
          durationMs: 1200,
          inputTokens: 10,
          outputTokens: 20,
          cachedTokens: 0,
          stream: true,
          bytesIn: 100,
          bytesOut: 200,
        }}
      />,
    );

    expect(html).toContain('ant-card');
    expect(html).toContain('502');
    expect(html).toContain('gpt-4.5');
    expect(html).toContain('deepseek-v4-flash');
    expect(html).toContain('10/20 tok');
    expect(html).toContain('upstream connect failed');
  });

  it('shows the effort when the request specifies one', () => {
    const html = renderToStaticMarkup(
      <SessionCard
        row={{
          requestId: 'r2',
          sid: 's2',
          ts: '2026-11-08T08:30:00Z',
          model: 'deepseek-v4.1-flash',
          clientModel: 'claude-opus-5-5',
          provider: 'p_0000000002',
          providerName: 'opencode-go',
          route: 'r_0000000002',
          routeName: 'deepseek-flash',
          node: 'n_0000000002',
          nodeName: 'oc-go-dsf',
          gatewayKey: '',
          gatewayKeyName: '',
          protocol: 'chat',
          effort: 'high',
          status: 200,
          durationMs: 10,
          inputTokens: 0,
          outputTokens: 0,
          cachedTokens: 0,
          stream: true,
          bytesIn: 0,
          bytesOut: 0,
        }}
      />,
    );

    expect(html).toContain('effort high');
  });
});
