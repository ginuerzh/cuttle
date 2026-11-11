// @vitest-environment jsdom
import { createRoot } from 'react-dom/client';
import { act } from 'react-dom/test-utils';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { ConfigResponse } from '../api/types';

vi.mock('../api/client', () => ({
  getRequests: vi.fn(async () => []),
  getErrorSummary: vi.fn(async () => ({
    total: 0,
    errors: 0,
    errorRate: 0,
    byClass: [],
    byProvider: [],
    byRoute: [],
    byClientModel: [],
    topMessages: [],
    hourly: [],
  })),
  getSeries: vi.fn(async () => ({ bucket: '2m0s', points: [] })),
  getAlerts: vi.fn(async () => []),
  listCaptureRules: vi.fn(async () => []),
  createCaptureRule: vi.fn(async () => ({
    id: 'cap_0000000001',
    reason: 'investigate',
    limit: 10,
    ttl: 300,
    created: '',
    expires: '',
    used: 0,
  })),
  deleteCaptureRule: vi.fn(async () => undefined),
  listGatewayKeys: vi.fn(async () => [
    { id: 'gk_0000000001', name: 'claude-code', keyPreview: 'sk-...abcd', enabled: true },
  ]),
}));

import { createCaptureRule, getRequests, getSeries } from '../api/client';
import { RequestsPage } from './Requests';
import { setActiveLang } from '../i18n';

setActiveLang('zh-CN');

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const getRequestsMock = vi.mocked(getRequests);
const getSeriesMock = vi.mocked(getSeries);
const createCaptureMock = vi.mocked(createCaptureRule);

const config = {
  gateway: { listen: ':8787' },
  admin: { listen: ':8788', sessionTTL: '12h' },
  providers: [{ id: 'p_0000000001', name: 'DeepSeek 官方', type: 'deepseek', keys: [] }],
  nodes: [{ id: 'n_0000000001', name: 'ds-fast', provider: 'p_0000000001', model: 'm1' }],
  routes: [{ id: 'r_0000000001', name: 'strong', nodes: ['n_0000000001'] }],
  entrypoints: [{ id: 'e_0000000001', name: 'fast', match: ['*'], route: 'r_0000000001' }],
} as unknown as ConfigResponse;

function render(el: React.ReactNode) {
  const container = document.createElement('div');
  document.body.appendChild(container);
  const root = createRoot(container);
  act(() => {
    root.render(el);
  });
  return { container, root };
}

function buttonByText(container: ParentNode, text: string): HTMLButtonElement {
  const norm = (s: string) => s.replace(/\s+/g, '');
  const buttons = Array.from(container.querySelectorAll('button'));
  const button = buttons.find((b) => norm(b.textContent ?? '') === norm(text));
  if (!button) throw new Error(`button ${text} not found; have ${buttons.map((b) => b.textContent).join('|')}`);
  return button as HTMLButtonElement;
}

function mouseDown(el: Element) {
  act(() => {
    el.dispatchEvent(new MouseEvent('mousedown', { bubbles: true, cancelable: true }));
  });
}

function click(el: Element) {
  act(() => {
    el.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true }));
  });
}

function selectOption(container: ParentNode, current: string, option: string) {
  const select = Array.from(container.querySelectorAll<HTMLElement>('.ant-select')).find((el) =>
    (el.textContent ?? '').includes(current),
  );
  if (!select) {
    const have = Array.from(container.querySelectorAll<HTMLElement>('.ant-select')).map((el) => el.textContent);
    throw new Error(`select ${current} not found; have ${have.join('|')}`);
  }
  mouseDown(select.querySelector('.ant-select-selector') ?? select);
  const items = Array.from(document.querySelectorAll<HTMLElement>('.ant-select-item-option'));
  const target = items.find((el) => (el.textContent ?? '').trim() === option);
  if (!target) throw new Error(`option ${option} not found; have ${items.map((el) => el.textContent).join('|')}`);
  click(target);
}

async function settle() {
  await act(async () => {
    await Promise.resolve();
  });
}

describe('RequestsPage filters', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    window.matchMedia = vi.fn().mockReturnValue({
      matches: false,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
    }) as unknown as typeof window.matchMedia;
  });

  it('sends the provider id selected by name', async () => {
    const { container } = render(<RequestsPage config={config} />);
    await settle();

    selectOption(container, '提供商', 'DeepSeek 官方');
    await settle();

    expect(getRequestsMock).toHaveBeenLastCalledWith(
      expect.objectContaining({ provider: 'p_0000000001' }),
    );
  });

  it('sends the gateway key id selected by name', async () => {
    const { container } = render(<RequestsPage config={config} />);
    await settle();

    selectOption(container, '网关密钥', 'claude-code');
    await settle();

    expect(getRequestsMock).toHaveBeenLastCalledWith(
      expect.objectContaining({ gatewayKey: 'gk_0000000001' }),
    );
  });

  it('offers every entity filter and never free text', async () => {
    const { container } = render(<RequestsPage config={config} />);
    await settle();

    for (const placeholder of ['提供商', '路由', '节点', '入口']) {
      const input = container.querySelector(`input[placeholder="${placeholder}"]`);
      expect(input).toBeNull();
    }
  });
});

describe('RequestsPage trend', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    window.matchMedia = vi.fn().mockReturnValue({
      matches: false,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
    }) as unknown as typeof window.matchMedia;
  });

  it('clears the zoom when the range preset changes', async () => {
    const now = Date.now();
    getSeriesMock.mockResolvedValue({
      bucket: '2m0s',
      points: [{ bucket: new Date(now - 60_000).toISOString(), requests: 3, errors: 0 }],
    });
    const { container } = render(<RequestsPage config={config} />);
    await settle();

    const bar = container.querySelector('rect[data-bar]') as unknown as SVGRectElement;
    act(() => {
      bar.dispatchEvent(new MouseEvent('click', { bubbles: true }));
    });
    await settle();

    const zoomed = getSeriesMock.mock.calls.at(-1)?.[0];
    expect((zoomed!.to.getTime() - zoomed!.from.getTime())).toBeLessThanOrEqual(2 * 60_000);

    selectOption(container, '1 小时', '24 小时');
    await settle();

    const reset = getSeriesMock.mock.calls.at(-1)?.[0];
    expect(reset!.to.getTime() - reset!.from.getTime()).toBeGreaterThan(60 * 60_000);
  });

  it('keeps the session rows when the trend request fails', async () => {
    getRequestsMock.mockResolvedValueOnce([
      {
        requestId: 'req-1',
        ts: new Date().toISOString(),
        clientModel: 'claude-sonnet',
        model: 'deepseek-v4.1-flash',
        status: 200,
        durationMs: 10,
        inputTokens: 1,
        outputTokens: 1,
        cachedTokens: 0,
      } as never,
    ]);
    getSeriesMock.mockRejectedValueOnce(new Error('boom'));

    const { container } = render(<RequestsPage config={config} />);
    await settle();

    expect(container.textContent).toContain('趋势加载失败');
    expect(container.textContent).toContain('claude-sonnet');
  });
});

describe('RequestsPage capture rule', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    window.matchMedia = vi.fn().mockReturnValue({
      matches: false,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
    }) as unknown as typeof window.matchMedia;
  });

  it('creates the rule with the route id selected by name', async () => {
    const { container } = render(<RequestsPage config={config} />);
    await settle();

    act(() => {
      buttonByText(container, '捕获').click();
    });
    await settle();

    selectOption(document.body, '路由（留空表示全部）', 'strong');

    await act(async () => {
      buttonByText(document.body, '开始捕获').click();
    });
    await settle();

    expect(createCaptureMock).toHaveBeenCalledWith(
      expect.objectContaining({ route: 'r_0000000001' }),
    );
  });
});
