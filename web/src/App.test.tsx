import { renderToStaticMarkup } from 'react-dom/server';
import { beforeEach, describe, expect, it } from 'vitest';
import { AttemptCard, EdgePanel, mergeNodeActivity, NodePanel, SessionCard, sessionQuery } from './App';
import { setActiveLang } from './i18n';
import type { AttemptRow, ConfigResponse, RequestRow } from './api/types';

beforeEach(() => setActiveLang('zh-CN'));

const metrics = {
  requests: 10, errors: 0, attempts: 0, retriedRequests: 0, p50Ms: 3, p95Ms: 5,
  inputTokens: 0, outputTokens: 80, cachedTokens: 0,
  outputTokensPerSecond: 42.34, lastFailureStatus: 0, lastAttemptStatus: 0,
};

describe('EdgePanel', () => {
  it('reports the discarded attempts of a binding that served no requests', () => {
    const html = renderToStaticMarkup(
      <EdgePanel
        id="route:r_0000000001|node:n_0000000001"
        metrics={{ requests: 0, errors: 0, attempts: 2, retriedRequests: 2 }}
      />,
    );

    expect(html).toContain('重试尝试');
    expect(html).toContain('2');
    expect(html).toContain('重试请求');
  });

  it('adds no attempt row when nothing was discarded', () => {
    const html = renderToStaticMarkup(
      <EdgePanel id="route:r_0000000001|node:n_0000000001" metrics={{ requests: 5, errors: 0, attempts: 0, retriedRequests: 0 }} />,
    );

    expect(html).not.toContain('重试尝试');
  });
});

// A node in a 1h window: one final 429 and two discarded attempts behind two
// client requests.
const retryMetrics = {
  ...metrics,
  requests: 12,
  errors: 3,
  attempts: 2,
  retriedRequests: 1,
  attemptStatus: { '429': 2 },
  failureStatus: { '429': 1 },
};

const cfg = {
  gateway: { listen: ':8787' },
  admin: { listen: ':8788', sessionTTL: '12h' },
  providers: [{ id: 'p_0000000001', name: 'DeepSeek 官方', type: 'deepseek', keys: [] }],
  nodes: [{ id: 'n_0000000001', name: 'fast', provider: 'p_0000000001', model: 'deepseek-v4-flash' }],
  routes: [{ id: 'r_0000000001', name: 'strong', nodes: ['n_0000000001'] }],
  entrypoints: [{ id: 'e_0000000001', name: 'all', match: ['*'], route: 'r_0000000001' }],
} as unknown as ConfigResponse;

// ASCII labels only: the English panel test forbids CJK characters, and the
// display names are config data rather than console strings.
const enCfg = {
  ...cfg,
  providers: [{ id: 'p_0000000001', name: 'deepseek', type: 'deepseek', keys: [] }],
  nodes: [{ id: 'n_0000000001', name: 'fast', provider: 'p_0000000001', model: 'deepseek-v4-flash', api: 'chat' }],
} as unknown as ConfigResponse;

const base: RequestRow = {
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
  status: 200,
  durationMs: 10,
  inputTokens: 10,
  outputTokens: 20,
  cachedTokens: 0,
  stream: true,
  bytesIn: 100,
  bytesOut: 200,
};

const attempt: AttemptRow = {
  requestId: 'r1',
  attempt: 0,
  ts: '2026-11-07T10:20:30Z',
  route: 'r_0000000001',
  routeName: 'fast',
  node: 'n_0000000001',
  nodeName: 'fast',
  provider: 'p_0000000001',
  providerName: 'deepseek',
  status: 429,
  error: 'rate limit exceeded',
  errorClass: 'quota',
  durationMs: 40,
  bytesIn: 120,
  bytesOut: 60,
};

// SSR renders the first pass only: the panel's hooks never run, so the fetch-loaded
// session and attempt cards are asserted in App.dom.test.tsx. The metrics-derived
// summary and the dead-badge tooltip are visible here.
describe('NodePanel retry attempts', () => {
  it('summarises the discarded attempts of the window', () => {
    const html = renderToStaticMarkup(
      <NodePanel config={cfg} id="node:n_0000000001" label="fast" metrics={retryMetrics} since="" rangeMs={3_600_000} />,
    );

    expect(html).toContain('重试尝试');
    expect(html).toContain('429×2');
    expect(html).toContain('重试请求');
  });

  it('omits the attempt summary when the window has none', () => {
    const html = renderToStaticMarkup(
      <NodePanel config={cfg} id="node:n_0000000001" metrics={metrics} since="" rangeMs={3_600_000} />,
    );

    expect(html).not.toContain('重试尝试');
  });

  it('explains the dead badge with the failures and the discarded attempts', () => {
    const html = renderToStaticMarkup(
      <NodePanel config={cfg} id="node:n_0000000001" metrics={retryMetrics} since="" rangeMs={3_600_000} dead />,
    );

    expect(html).toContain('aria-label="dead"');
    expect(html).toContain('title="失败 1（429×1）· 重试丢弃 2"');
  });
});

describe('AttemptCard', () => {
  it('marks the card as a retry attempt with the upstream status and message', () => {
    const html = renderToStaticMarkup(<AttemptCard row={attempt} />);

    expect(html).toContain('ant-card');
    expect(html).toContain('重试尝试');
    expect(html).toContain('429');
    expect(html).toContain('上游尝试');
    expect(html).toContain('rate limit exceeded');
    expect(html).toContain('deepseek');
  });

  it('shows a failed dial without a status', () => {
    const html = renderToStaticMarkup(
      <AttemptCard row={{ ...attempt, status: 0, error: 'dial tcp: connection refused', errorClass: 'network' }} />,
    );

    expect(html).toContain('连接失败');
    expect(html).toContain('dial tcp: connection refused');
  });
});

describe('mergeNodeActivity', () => {
  it('lists requests and attempts newest first and keeps a request before its own attempts', () => {
    const older = { ...base, requestId: 'r0', ts: '2026-11-07T10:00:00Z' };
    const newer = { ...base, requestId: 'r2', ts: '2026-11-07T11:00:00Z' };
    const merged = mergeNodeActivity([base, older, newer], [attempt]);

    expect(merged.map((item) => (item.kind === 'request' ? item.request.requestId : `attempt:${item.attempt.requestId}`))).toEqual([
      'r2',
      'r1',
      'attempt:r1',
      'r0',
    ]);
  });
});

describe('NodePanel', () => {
  it('shows output speed for a binding node', () => {
    const html = renderToStaticMarkup(
      <NodePanel config={cfg} id="node:n_0000000001" label="fast" metrics={metrics} since="" rangeMs={3_600_000} />,
    );
    expect(html).toContain('输出速度');
    expect(html).toContain('42.3 tok/s');
  });

  it('omits output speed for non-binding nodes', () => {
    const html = renderToStaticMarkup(
      <NodePanel config={cfg} id="provider:p_0000000001" label="p" metrics={metrics} since="" rangeMs={3_600_000} />,
    );
    expect(html).not.toContain('输出速度');
  });

  it('titles the panel with the entity label', () => {
    const html = renderToStaticMarkup(
      <NodePanel config={cfg} id="node:n_0000000001" label="fast" metrics={metrics} since="" rangeMs={3_600_000} />,
    );
    expect(html).toContain('fast');
    expect(html).not.toContain('node:n_0000000001');
  });

  it('falls back to the raw graph id without a label', () => {
    const html = renderToStaticMarkup(
      <NodePanel config={cfg} id="node:n_0000000001" metrics={metrics} since="" rangeMs={3_600_000} />,
    );
    expect(html).toContain('node:n_0000000001');
  });

  it('shows recent sessions with an errors-only filter', () => {
    const html = renderToStaticMarkup(
      <NodePanel config={cfg} id="node:n_0000000001" metrics={metrics} since="" rangeMs={3_600_000} />,
    );

    expect(html).toContain('最近会话');
    expect(html).toContain('仅错误');
  });

  it('marks a dead node in the panel with a grey icon', () => {
    const html = renderToStaticMarkup(
      <NodePanel config={cfg} id="node:n_0000000001" metrics={metrics} since="" rangeMs={3_600_000} dead />,
    );

    expect(html).toContain('anticon-api');
    expect(html).toContain('var(--gw-text-muted)');
    expect(html).toContain('aria-label="dead"');
    expect(html).toContain('title="窗口内没有失败记录"');
    expect(html).not.toContain('ant-badge-status-error');
  });

  it('lists the routes where a partially dead node is dead by their labels', () => {
    const html = renderToStaticMarkup(
      <NodePanel
        config={cfg}
        id="node:n_0000000001"
        label="fast"
        metrics={metrics}
        since=""
        rangeMs={3_600_000}
        deadRoutes={['fast', 'backup']}
      />,
    );

    expect(html).toContain('dead 路由');
    expect(html).toContain('fast, backup');
  });

  it('shows the node parameters above the metrics', () => {
    const html = renderToStaticMarkup(
      <NodePanel config={cfg} id="node:n_0000000001" label="fast" metrics={metrics} since="" rangeMs={3_600_000} />,
    );
    expect(html).toContain('参数');
    expect(html).toContain('deepseek-v4-flash');
    expect(html).toContain('全部 key（轮询）');
    expect(html.indexOf('参数')).toBeLessThan(html.indexOf('P50 / P95'));
  });

  it('shows the provider parameters', () => {
    const html = renderToStaticMarkup(
      <NodePanel config={cfg} id="provider:p_0000000001" label="p" metrics={metrics} since="" rangeMs={3_600_000} />,
    );
    expect(html).toContain('类型');
    expect(html).toContain('deepseek');
    expect(html).toContain('0 个 key');
  });

  it('renders no parameter block for unrouted or unknown ids', () => {
    const unrouted = renderToStaticMarkup(
      <NodePanel config={cfg} id="unrouted" metrics={metrics} since="" rangeMs={3_600_000} />,
    );
    const gone = renderToStaticMarkup(
      <NodePanel config={cfg} id="node:n_gone000001" metrics={metrics} since="" rangeMs={3_600_000} />,
    );
    expect(unrouted).not.toContain('参数');
    expect(gone).not.toContain('参数');
  });

  it('renders the parameters in English', () => {
    setActiveLang('en-US');
    const html = renderToStaticMarkup(
      <NodePanel config={enCfg} id="node:n_0000000001" metrics={metrics} since="" rangeMs={3_600_000} />,
    );
    expect(html).toContain('Parameters');
    expect(html).toContain('Target API (protocol)');
    expect(html).toContain('Pinned key');
    expect(html).not.toMatch(/[\u4e00-\u9fff]/);
  });
});

describe('sessionQuery', () => {
  const from = new Date('2026-11-07T00:00:00Z');
  const to = new Date('2026-11-07T01:00:00Z');

  it('filters a binding node by id and keeps the 100-row cap', () => {
    expect(sessionQuery('node:n_3k9x2m4q7a', from, to, false)).toEqual({
      from, to, limit: 100, errorsOnly: false, node: 'n_3k9x2m4q7a',
    });
  });

  it('filters every other graph node kind by id', () => {
    expect(sessionQuery('route:r_3k9x2m4q7a', from, to, true).route).toBe('r_3k9x2m4q7a');
    expect(sessionQuery('provider:p_3k9x2m4q7a', from, to, false).provider).toBe('p_3k9x2m4q7a');
    expect(sessionQuery('entrypoint:e_3k9x2m4q7a', from, to, false).entrypoint).toBe('e_3k9x2m4q7a');
  });

  it('never sends a display name as a filter', () => {
    const query = sessionQuery('provider:p_3k9x2m4q7a', from, to, false);
    expect(Object.values(query)).not.toContain('DeepSeek 官方');
  });
});

describe('SessionCard', () => {
  it('renders the status, model, tokens and error', () => {
    const html = renderToStaticMarkup(
      <SessionCard
        row={{
          ...base,
          status: 502,
          error: 'upstream connect failed',
          durationMs: 1200,
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
      <SessionCard row={{ ...base, effort: 'high' }} />,
    );

    expect(html).toContain('effort high');
  });

  it('renders request row labels from the backend', () => {
    const html = renderToStaticMarkup(
      <SessionCard
        row={{
          ...base,
          provider: 'p_deleted0001',
          providerName: 'DeepSeek 官方',
          route: 'r_deleted001',
          routeName: 'strong',
          node: 'n_deleted0001',
          nodeName: 'ds-strong',
          gatewayKey: 'gk_deleted001',
          gatewayKeyName: 'claude-code',
        }}
      />,
    );

    expect(html).toContain('strong');
    expect(html).toContain('DeepSeek 官方');
    expect(html).toContain('/ds-strong');
    expect(html).toContain('key:claude-code');
    expect(html).not.toContain('p_deleted0001');
    expect(html).not.toContain('r_deleted001');
    expect(html).not.toContain('n_deleted0001');
    expect(html).not.toContain('gk_deleted001');
  });

  it('falls back to the raw id when the backend has no label', () => {
    const html = renderToStaticMarkup(
      <SessionCard
        row={{
          ...base,
          provider: 'p_unknown001',
          providerName: '',
          route: '',
          routeName: '',
          node: '',
          nodeName: '',
          gatewayKey: '',
          gatewayKeyName: '',
        }}
      />,
    );

    expect(html).toContain('unrouted');
    expect(html).toContain('p_unknown001');
  });
});
