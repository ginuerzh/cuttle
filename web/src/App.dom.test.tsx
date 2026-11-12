// @vitest-environment jsdom
import { createRoot } from 'react-dom/client';
import { act } from 'react-dom/test-utils';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { AttemptRow, ConfigResponse, RequestRow } from './api/types';

const session = {
  requestId: 'r_0000000001',
  sid: 's1',
  ts: '2026-11-11T10:00:00Z',
  model: 'm1',
  provider: 'p_0000000001',
  providerName: 'deepseek',
  route: 'r_0000000001',
  routeName: 'strong',
  node: 'n_0000000001',
  nodeName: 'fast',
  gatewayKeyName: '',
  protocol: 'chat',
  effort: '',
  status: 429,
  error: 'rate limited',
  durationMs: 40,
  inputTokens: 1,
  outputTokens: 0,
  cachedTokens: 0,
  stream: false,
  bytesIn: 10,
  bytesOut: 0,
} as RequestRow;

const attempt = {
  requestId: session.requestId,
  attempt: 0,
  ts: session.ts,
  route: 'r_0000000001',
  routeName: 'strong',
  node: 'n_0000000001',
  nodeName: 'fast',
  provider: 'p_0000000001',
  providerName: 'deepseek',
  status: 429,
  error: 'quota exhausted for this key',
  errorClass: 'quota',
  durationMs: 12,
  bytesIn: 10,
  bytesOut: 0,
} as AttemptRow;

vi.mock('./api/client', () => ({
  getRequests: vi.fn(async () => [session]),
  getAttempts: vi.fn(async () => [attempt]),
  getRequest: vi.fn(async () => session),
  getConfig: vi.fn(async () => null),
  getHealth: vi.fn(async () => null),
  getRouteHealth: vi.fn(async () => []),
  clearAuthFailures: vi.fn(async () => undefined),
  logout: vi.fn(async () => undefined),
  UnauthorizedError: class UnauthorizedError extends Error {},
}));

import { getAttempts, getRequest, getRequests } from './api/client';
import { NodePanel } from './App';
import { setActiveLang } from './i18n';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const getRequestsMock = vi.mocked(getRequests);
const getAttemptsMock = vi.mocked(getAttempts);
const getRequestMock = vi.mocked(getRequest);

const config = {
  gateway: { listen: ':8787' },
  admin: { listen: ':8788', sessionTTL: '12h' },
  providers: [{ id: 'p_0000000001', name: 'deepseek', type: 'deepseek', keys: [] }],
  nodes: [{ id: 'n_0000000001', name: 'fast', provider: 'p_0000000001', model: 'm1' }],
  routes: [{ id: 'r_0000000001', name: 'strong', nodes: ['n_0000000001'] }],
  entrypoints: [{ id: 'e_0000000001', name: 'all', match: ['*'], routes: [{ route: 'r_0000000001' }] }],
} as unknown as ConfigResponse;

const metrics = {
  requests: 1, errors: 1, attempts: 1, retriedRequests: 1, p50Ms: 40, p95Ms: 40,
  inputTokens: 1, outputTokens: 0, cachedTokens: 0, outputTokensPerSecond: 0,
  attemptStatus: { '429': 1 }, failureStatus: { '429': 1 },
  lastFailureTs: Date.UTC(2026, 10, 11, 3, 0, 16), lastFailureStatus: 429,
  lastAttemptTs: Date.UTC(2026, 10, 11, 3, 0, 10), lastAttemptStatus: 429,
};

function render(el: React.ReactNode) {
  const container = document.createElement('div');
  document.body.appendChild(container);
  const root = createRoot(container);
  act(() => {
    root.render(el);
  });
  return { container, root };
}

async function settle() {
  await act(async () => {
    await Promise.resolve();
  });
}

function click(el: Element) {
  act(() => {
    el.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true }));
  });
}

describe('NodePanel activity list', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    setActiveLang('zh-CN');
    window.matchMedia = vi.fn().mockReturnValue({
      matches: false,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
    }) as unknown as typeof window.matchMedia;
    getRequestsMock.mockImplementation(async () => [session]);
    getAttemptsMock.mockImplementation(async () => [attempt]);
    getRequestMock.mockImplementation(async () => ({ ...session, attempts: [attempt] }));
  });

  it('lists the discarded attempts with the sessions of the node', async () => {
    const { container, root } = render(
      <NodePanel config={config} id="node:n_0000000001" metrics={metrics} since="" rangeMs={3_600_000} />,
    );
    await settle();

    const text = container.textContent ?? '';
    expect(text).toContain('最近会话');
    expect(text).toContain('重试尝试');
    expect(text).toContain('quota exhausted for this key');
    expect(text).toContain('rate limited');
    expect(text).toContain('上游尝试 #1');
    expect(getAttemptsMock).toHaveBeenCalledWith(expect.objectContaining({ node: 'n_0000000001', limit: 100 }));
    act(() => root.unmount());
  });

  it('opens the parent request, including its discarded attempts, when an attempt card is clicked', async () => {
    const { container, root } = render(
      <NodePanel config={config} id="node:n_0000000001" metrics={metrics} since="" rangeMs={3_600_000} />,
    );
    await settle();

    const cards = Array.from(container.querySelectorAll('.ant-card'));
    const attemptCard = cards.find((card) => (card.textContent ?? '').includes('quota exhausted for this key'));
    if (!attemptCard) throw new Error('attempt card not found');
    click(attemptCard);
    await settle();

    expect(getRequestMock).toHaveBeenCalledWith(session.requestId);
    // The drawer is portalled to the body and lists the same attempt again in
    // the request detail, which the Sessions page shares.
    const drawer = document.body.textContent ?? '';
    expect(drawer).toContain('上游尝试');
    expect(drawer).toContain('quota exhausted for this key');
    act(() => root.unmount());
  });

  it('fetches no attempts for an entrypoint panel, which has no attempt filter', async () => {
    // Attempts carry no client model, so an entrypoint node gets failure counts
    // but no attempt counters (see the stats graph invariants).
    const entrypointMetrics = { ...metrics, attempts: 0, retriedRequests: 0, attemptStatus: undefined };
    const { container, root } = render(
      <NodePanel config={config} id="entrypoint:e_0000000001" metrics={entrypointMetrics} since="" rangeMs={3_600_000} />,
    );
    await settle();

    expect(getAttemptsMock).not.toHaveBeenCalled();
    expect(container.textContent ?? '').not.toContain('上游尝试');
    act(() => root.unmount());
  });
});
