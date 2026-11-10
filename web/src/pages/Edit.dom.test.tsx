// @vitest-environment jsdom
import { createRoot } from 'react-dom/client';
import { act } from 'react-dom/test-utils';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { ConfigResponse, Node, Provider } from '../api/types';

vi.mock('../api/client', () => ({
  getProviderModels: vi.fn(async () => []),
  updateNode: vi.fn(async () => undefined),
  updateProvider: vi.fn(async () => undefined),
}));

import { getProviderModels, updateNode, updateProvider } from '../api/client';
import { NodeForm, ProviderForm } from './Edit';
import { setActiveLang } from '../i18n';

setActiveLang('zh-CN');

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const updateProviderMock = vi.mocked(updateProvider);
const updateNodeMock = vi.mocked(updateNode);

function render(el: React.ReactNode) {
  const container = document.createElement('div');
  document.body.appendChild(container);
  const root = createRoot(container);
  act(() => {
    root.render(el);
  });
  return { container, root };
}

function buttonByText(container: HTMLElement, text: string): HTMLButtonElement {
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

const provider: Provider = {
  id: 'p_0000000001',
  name: 'p',
  type: 'generic',
  chat: 'https://a.example/v1',
  keys: [{ id: 'pk_0000000001', name: 'account-a', keyPreview: 'sk-...a', enabled: true }],
};

describe('ProviderForm key editor', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    window.matchMedia = vi.fn().mockReturnValue({
      matches: false,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
    }) as unknown as typeof window.matchMedia;
  });

  it('adds a named key and saves name-based keyUpdates', async () => {
    const { container } = render(<ProviderForm provider={provider} onSaved={async () => undefined} />);

    act(() => {
      buttonByText(container, '添加 key').click();
    });
    const nameInput = container.querySelector('input[placeholder="key 名称"]') as HTMLInputElement;
    const valueInput = container.querySelector('input[placeholder="sk-..."]') as HTMLInputElement;
    expect(nameInput).not.toBeNull();
    typeInto(nameInput, 'account-c');
    typeInto(valueInput, 'sk-c');

    await act(async () => {
      buttonByText(container, '保存').click();
    });

    expect(updateProviderMock).toHaveBeenCalledTimes(1);
    const [, payload] = updateProviderMock.mock.calls[0];
    expect(payload).toMatchObject({ keyUpdates: [{ name: 'account-c', key: 'sk-c' }] });
  });

  it('keeps focus while typing a new key name', async () => {
    const { container } = render(<ProviderForm provider={provider} onSaved={async () => undefined} />);

    act(() => {
      buttonByText(container, '添加 key').click();
    });
    const nameInput = container.querySelector('input[placeholder="key 名称"]') as HTMLInputElement;
    typeInto(nameInput, 'a');

    expect(document.activeElement).toBe(nameInput);
  });

  it('does not save a partially filled add row', async () => {
    const { container } = render(<ProviderForm provider={provider} onSaved={async () => undefined} />);

    act(() => {
      buttonByText(container, '添加 key').click();
    });
    const nameInput = container.querySelector('input[placeholder="key 名称"]') as HTMLInputElement;
    typeInto(nameInput, 'account-c');

    await act(async () => {
      buttonByText(container, '保存').click();
    });

    expect(updateProviderMock).not.toHaveBeenCalled();
  });
});

describe('NodeForm key pin', () => {
  const node: Node = {
    id: 'n_0000000001',
    name: 'n',
    provider: 'p_0000000001',
    model: 'm1',
    key: 'pk_0000000001',
  };
  const config = {
    providers: [
      {
        id: 'p_0000000001',
        name: 'p',
        type: 'generic',
        chat: 'https://a.example/v1',
        keys: [
          { id: 'pk_0000000001', name: 'account-a', keyPreview: 'sk-...a', enabled: true },
          { id: 'pk_0000000002', name: 'account-b', keyPreview: 'sk-...b', enabled: false },
        ],
      },
    ],
    nodes: [node],
    routes: [],
    entrypoints: [],
  } as unknown as ConfigResponse;

  beforeEach(() => {
    vi.clearAllMocks();
    window.matchMedia = vi.fn().mockReturnValue({
      matches: false,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
    }) as unknown as typeof window.matchMedia;
  });

  it('saves the pinned key', async () => {
    const { container } = render(<NodeForm node={node} config={config} onSaved={async () => undefined} />);
    await act(async () => {
      await getProviderModels('p');
    });

    await act(async () => {
      buttonByText(container, '保存').click();
    });

    expect(updateNodeMock).toHaveBeenCalledTimes(1);
    const [id, payload] = updateNodeMock.mock.calls[0];
    expect(id).toBe('n_0000000001');
    expect(payload).toMatchObject({ key: 'pk_0000000001' });
  });
});
