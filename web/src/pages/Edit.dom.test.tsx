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
import { NodeForm, ProviderForm, RouteNodeList } from './Edit';
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

/** Open one antd Select by its current label and choose an option by its label. */
function selectOption(container: HTMLElement, current: string, option: string) {
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

  it('renames a provider without changing its id', async () => {
    const { container } = render(<ProviderForm provider={provider} onSaved={async () => undefined} />);

    const nameInput = container.querySelector('input#name') as HTMLInputElement;
    expect(nameInput).not.toBeNull();
    expect(nameInput.value).toBe('p');
    expect(container.textContent).toContain('仅用于显示');
    typeInto(nameInput, '重命名后的提供商');

    await act(async () => {
      buttonByText(container, '保存').click();
    });

    expect(updateProviderMock).toHaveBeenCalledTimes(1);
    const [id, payload] = updateProviderMock.mock.calls[0];
    expect(id).toBe('p_0000000001');
    expect(payload.name).toBe('重命名后的提供商');
    expect(payload.id).toBeUndefined();
  });

  it('adds a named key and saves id-carrying key updates', async () => {
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
    const updates = (payload as { keyUpdates?: { id?: string; name?: string; key?: string }[] }).keyUpdates;
    // A new key has no id yet; the admin API mints one.
    expect(updates).toMatchObject([{ name: 'account-c', key: 'sk-c' }]);
    expect(updates?.[0].id).toBeUndefined();
  });

  it('renames an existing key by its id', async () => {
    const { container } = render(<ProviderForm provider={provider} onSaved={async () => undefined} />);

    const nameInput = container.querySelector('input[aria-label="key 名称"]') as HTMLInputElement;
    expect(nameInput).not.toBeNull();
    expect(nameInput.value).toBe('account-a');
    typeInto(nameInput, 'account-main');

    await act(async () => {
      buttonByText(container, '保存').click();
    });

    expect(updateProviderMock).toHaveBeenCalledTimes(1);
    const [, payload] = updateProviderMock.mock.calls[0];
    expect(payload).toMatchObject({
      keyUpdates: [{ id: 'pk_0000000001', name: 'account-main', enabled: true }],
    });
  });

  it('deletes an existing key by its id', async () => {
    const { container } = render(<ProviderForm provider={provider} onSaved={async () => undefined} />);

    act(() => {
      (container.querySelector('button[aria-label="删除 key"]') as HTMLButtonElement).click();
    });

    await act(async () => {
      buttonByText(container, '保存').click();
    });

    expect(updateProviderMock).toHaveBeenCalledTimes(1);
    const [, payload] = updateProviderMock.mock.calls[0];
    expect(payload).toMatchObject({ keyUpdates: [{ id: 'pk_0000000001', delete: true }] });
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

  it('pins a provider key by id', async () => {
    const { container } = render(<NodeForm node={node} config={config} onSaved={async () => undefined} />);
    await act(async () => {
      await getProviderModels('p_0000000001');
    });

    selectOption(container, 'account-a', 'account-b（已禁用）');

    await act(async () => {
      buttonByText(container, '保存').click();
    });

    expect(updateNodeMock).toHaveBeenCalledTimes(1);
    const [id, payload] = updateNodeMock.mock.calls[0];
    expect(id).toBe('n_0000000001');
    expect(payload).toMatchObject({ key: 'pk_0000000002' });
  });

  it('renames a node without changing its id', async () => {
    const { container } = render(<NodeForm node={node} config={config} onSaved={async () => undefined} />);

    const nameInput = container.querySelector('input#name') as HTMLInputElement;
    expect(nameInput).not.toBeNull();
    typeInto(nameInput, 'ds-fast');

    await act(async () => {
      buttonByText(container, '保存').click();
    });

    const [id, payload] = updateNodeMock.mock.calls[0];
    expect(id).toBe('n_0000000001');
    expect(payload).toMatchObject({ name: 'ds-fast', provider: 'p_0000000001' });
  });
});

describe('RouteNodeList', () => {
  const nodes: Node[] = [
    { id: 'n_0000000001', name: 'ds-fast', provider: 'p_0000000001', model: 'm1' },
    { id: 'n_0000000002', name: 'ds-slow', provider: 'p_0000000001', model: 'm2' },
  ];
  const providers: Provider[] = [
    { id: 'p_0000000001', name: 'DeepSeek 官方', type: 'deepseek', keys: [] },
  ];

  beforeEach(() => {
    vi.clearAllMocks();
    window.matchMedia = vi.fn().mockReturnValue({
      matches: false,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
    }) as unknown as typeof window.matchMedia;
  });

  it('renders node names and submits node ids', () => {
    const onChange = vi.fn();
    const { container } = render(
      <RouteNodeList
        value={['n_0000000001']}
        onChange={onChange}
        nodes={nodes}
        providers={providers}
      />,
    );

    expect(container.textContent).toContain('ds-fast');
    expect(container.textContent).toContain('DeepSeek 官方');
    expect(container.textContent).not.toContain('n_0000000001');

    selectOption(container, '添加 node', 'ds-slow (DeepSeek 官方)');
    expect(onChange).toHaveBeenCalledWith(['n_0000000001', 'n_0000000002']);

    onChange.mockClear();
    act(() => {
      (container.querySelector('button[aria-label="移除 ds-fast"]') as HTMLButtonElement).click();
    });
    expect(onChange).toHaveBeenCalledWith([]);
  });
});
