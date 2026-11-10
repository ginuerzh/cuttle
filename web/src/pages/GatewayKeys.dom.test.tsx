// @vitest-environment jsdom
import { createRoot } from 'react-dom/client';
import { act } from 'react-dom/test-utils';
import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../api/client', () => ({
  listGatewayKeys: vi.fn(async () => [
    { id: 'gk_0000000001', name: 'claude-code', keyPreview: 'sk-...abcd', enabled: true },
  ]),
  getGatewayKeyUsage: vi.fn(async () => ({
    from: '',
    to: '',
    checkedAt: '',
    totals: { requests: 0, errors: 0, inputTokens: 0, outputTokens: 0, cachedTokens: 0 },
    keys: [
      {
        id: 'gk_0000000001',
        name: 'claude-code',
        configured: true,
        enabled: true,
        requests: 0,
        errors: 0,
        inputTokens: 0,
        outputTokens: 0,
        cachedTokens: 0,
      },
    ],
  })),
  getRequests: vi.fn(async () => []),
  updateGatewayKey: vi.fn(async () => undefined),
  createGatewayKey: vi.fn(),
  rotateGatewayKey: vi.fn(),
  deleteGatewayKey: vi.fn(),
}));

import { updateGatewayKey } from '../api/client';
import { GatewayKeysPage } from './GatewayKeys';
import { setActiveLang } from '../i18n';

setActiveLang('zh-CN');

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const updateMock = vi.mocked(updateGatewayKey);

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

function typeInto(input: HTMLInputElement, value: string) {
  const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')?.set;
  act(() => {
    input.focus();
    setter?.call(input, value);
    input.dispatchEvent(new Event('input', { bubbles: true }));
  });
}

async function settle() {
  await act(async () => {
    await Promise.resolve();
  });
}

describe('GatewayKeysPage rename', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    window.matchMedia = vi.fn().mockReturnValue({
      matches: false,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
    }) as unknown as typeof window.matchMedia;
  });

  it('renames a key by id and never sends the id in the payload', async () => {
    const { container } = render(<GatewayKeysPage />);
    await settle();
    expect(container.textContent).toContain('claude-code');

    act(() => {
      buttonByText(container, '重命名').click();
    });

    const input = document.body.querySelector('.ant-modal input') as HTMLInputElement;
    expect(input).not.toBeNull();
    typeInto(input, 'claude-code-2');

    await act(async () => {
      buttonByText(document.body, '保存').click();
    });
    await settle();

    expect(updateMock).toHaveBeenCalledWith('gk_0000000001', { name: 'claude-code-2' });
  });

  it('hints that the name is display-only in the rename and add dialogs', async () => {
    const hint = '仅用于显示，可随时修改；实体 id 不变。';
    document.body.innerHTML = '';

    const renameView = render(<GatewayKeysPage />);
    await settle();
    act(() => {
      buttonByText(renameView.container, '重命名').click();
    });
    expect(document.body.querySelector('.ant-modal')?.textContent).toContain(hint);

    act(() => {
      renameView.root.unmount();
    });
    document.body.innerHTML = '';

    const addView = render(<GatewayKeysPage />);
    await settle();
    act(() => {
      buttonByText(addView.container, '添加网关密钥').click();
    });
    const modal = document.body.querySelector('.ant-modal');
    expect(modal).not.toBeNull();
    expect(modal?.textContent).toContain(hint);
    act(() => {
      addView.root.unmount();
    });
  });
});
