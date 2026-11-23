// @vitest-environment jsdom
import { createRoot } from 'react-dom/client';
import { act } from 'react-dom/test-utils';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { ConfigResponse, EntrypointHealth } from '../api/types';

vi.mock('../api/client', () => ({
  getEntrypointHealth: vi.fn(async () => []),
  getSubagentHealth: vi.fn(async () => []),
  putEntrypoints: vi.fn(async () => undefined),
  getProviderModels: vi.fn(async () => []),
  getPresets: vi.fn(async () => []),
  createNode: vi.fn(),
  createProvider: vi.fn(),
  createRoute: vi.fn(),
  deleteNode: vi.fn(),
  deleteProvider: vi.fn(),
  deleteRoute: vi.fn(),
  updateNode: vi.fn(),
  updateProvider: vi.fn(),
  updateRoute: vi.fn(),
  getTopologyPositions: vi.fn(async () => ({})),
  putTopologyPositions: vi.fn(async () => undefined),
  getRequest: vi.fn(async () => null),
}));

import { getEntrypointHealth, getSubagentHealth, putEntrypoints } from '../api/client';
import { EntrypointsPage } from './Entrypoints';
import { setActiveLang } from '../i18n';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const getEntrypointHealthMock = vi.mocked(getEntrypointHealth);
const putEntrypointsMock = vi.mocked(putEntrypoints);
const getSubagentHealthMock = vi.mocked(getSubagentHealth);

const config = {
  providers: [],
  nodes: [],
  routes: [
    { id: 'r_0000000001', name: 'primary', nodes: [] },
    { id: 'r_0000000002', name: 'backup', nodes: [] },
  ],
  entrypoints: [
    { id: 'e_0000000001', name: 'all', match: ['*'], routes: [{ route: 'r_0000000001' }] },
    { id: 'e_0000000002', name: 'claude', match: ['claude-*'], routes: [{ route: 'r_0000000002' }] },
  ],
} as unknown as ConfigResponse;

const health: EntrypointHealth = {
  entrypointId: 'e_0000000001',
  entrypointName: 'all',
  active: { routeId: 'r_0000000001', fallback: false },
  bindings: [{ index: 0, routeId: 'r_0000000001', routeName: 'primary', eligible: false }],
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
    await Promise.resolve();
  });
}

function buttonByText(container: HTMLElement, text: string): HTMLButtonElement {
  const buttons = Array.from(container.querySelectorAll('button'));
  const button = buttons.find((b) => (b.textContent ?? '').trim() === text);
  if (!button) throw new Error(`button ${text} not found; have ${buttons.map((b) => b.textContent).join('|')}`);
  return button as HTMLButtonElement;
}

describe('EntrypointsPage binding health', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    setActiveLang('zh-CN');
    window.matchMedia = vi.fn().mockReturnValue({
      matches: false,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
    }) as unknown as typeof window.matchMedia;
    getEntrypointHealthMock.mockImplementation(async () => [health]);
    putEntrypointsMock.mockImplementation(async () => undefined);
  });

  it('fetches binding health on mount without a prior topology visit', async () => {
    const { container, root } = render(<EntrypointsPage config={config} onReload={async () => undefined} />);
    await settle();

    expect(getEntrypointHealthMock).toHaveBeenCalledTimes(1);
    expect(container.textContent).toContain('未通过');
    expect(container.textContent).toContain('● primary');
    root.unmount();
  });

  it('fetches binding health again after a save reload', async () => {
    const { container, root } = render(<EntrypointsPage config={config} onReload={async () => undefined} />);
    await settle();
    expect(getEntrypointHealthMock).toHaveBeenCalledTimes(1);

    act(() => {
      buttonByText(container, '↓').dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true }));
    });
    await settle();

    expect(putEntrypointsMock).toHaveBeenCalledTimes(1);
    expect(getEntrypointHealthMock).toHaveBeenCalledTimes(2);
    root.unmount();
  });
});

describe('EntrypointsPage marker health', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    window.matchMedia = vi.fn().mockReturnValue({
      matches: false,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
    }) as unknown as typeof window.matchMedia;
  });

  it('flags a client that sent traffic without its marker', async () => {
    getSubagentHealthMock.mockResolvedValue([
      { source: 'opencode', client: 'opencode', requests: 10, marked: 10, markedPct: 100, status: 'ok' },
      { source: 'claude_code', client: 'claude-code', requests: 5, marked: 0, markedPct: 0, status: 'regressed' },
      { source: 'codex', client: 'codex', requests: 0, marked: 0, markedPct: 0, status: 'absent' },
    ]);
    const { container } = render(<EntrypointsPage config={config} onReload={async () => {}} />);
    await settle();

    expect(container.textContent).toContain('标记健康');
    expect(container.textContent).toContain('claude-code');
    expect(container.textContent).toContain('未见到标记');
    expect(container.textContent).toContain('无流量');
  });
});
