// @vitest-environment jsdom
import { createRoot } from 'react-dom/client';
import { act } from 'react-dom/test-utils';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { ConfigResponse, Node, Provider } from '../api/types';

vi.mock('../api/client', () => ({
  getProviderModels: vi.fn(async () => []),
  putEntrypoints: vi.fn(async () => undefined),
  updateNode: vi.fn(async () => undefined),
  updateProvider: vi.fn(async () => undefined),
}));

import { getProviderModels, putEntrypoints, updateNode, updateProvider } from '../api/client';
import { Edit, AddEntrypointModal, EdgeForm, EntrypointForm, NodeForm, ProviderForm, RouteNodeList } from './Edit';
import { setActiveLang } from '../i18n';

setActiveLang('zh-CN');

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const updateProviderMock = vi.mocked(updateProvider);
const updateNodeMock = vi.mocked(updateNode);
const putEntrypointsMock = vi.mocked(putEntrypoints);

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

function selectValue(container: HTMLElement, label: string): string {
  const item = Array.from(container.querySelectorAll('.ant-form-item')).find(
    (el) => el.querySelector('.ant-form-item-label')?.textContent?.trim() === label,
  );
  return item?.querySelector('.ant-select-selection-item')?.textContent?.trim() ?? '';
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

  it('stages a provider the topology drag picked before a model exists', async () => {
    const draft: Node = { ...node, provider: '', model: '', key: '' };
    const { container } = render(
      <NodeForm node={draft} config={config} pendingProvider="p_0000000001" onSaved={async () => undefined} />,
    );

    expect(selectValue(container, 'provider')).toBe('p');
  });

  it('fills the provider select when the node is connected from the topology', async () => {
    const draft: Node = { ...node, provider: '', model: 'm1', key: '' };
    const { container, root } = render(
      <NodeForm node={draft} config={config} onSaved={async () => undefined} />,
    );
    expect(selectValue(container, 'provider')).toBe('');

    await act(async () => {
      root.render(
        <NodeForm
          node={{ ...draft, provider: 'p_0000000001' }}
          config={config}
          onSaved={async () => undefined}
        />,
      );
    });

    expect(selectValue(container, 'provider')).toBe('p');
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

vi.mock('./Topology', () => ({
  Topology: (props: { onConnect?: (c: { source: string; target: string }) => void }) =>
    props.onConnect ? (
      <>
        <button
          type="button"
          onClick={() => props.onConnect!({ source: 'node:n_0000000001', target: 'provider:p_0000000001' })}
        >
          connect-draft
        </button>
        <button
          type="button"
          onClick={() => props.onConnect!({ source: 'entrypoint:e_0000000001', target: 'route:r_0000000002' })}
        >
          connect-entrypoint
        </button>
      </>
    ) : null,
}));

describe('Edit canvas connections', () => {
  const config = {
    gateway: { listen: ':8787' },
    admin: { listen: '', sessionTTL: '' },
    providers: [{ id: 'p_0000000001', name: 'p', type: 'generic', chat: 'https://a/v1', keys: [] }],
    nodes: [{ id: 'n_0000000001', name: 'draft', provider: '', model: 'm1' }],
    routes: [
      { id: 'r_0000000001', name: 'r', nodes: [] },
      { id: 'r_0000000002', name: 'r2', nodes: [] },
    ],
    entrypoints: [{ id: 'e_0000000001', name: 'e', match: ['*'], routes: [{ route: 'r_0000000001' }] }],
  } as unknown as ConfigResponse;

  beforeEach(() => {
    vi.clearAllMocks();
    window.matchMedia = vi.fn().mockReturnValue({
      matches: false,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
    }) as unknown as typeof window.matchMedia;
  });

  it('connects a node to a provider by dragging', async () => {
    updateNodeMock.mockClear();
    const { container } = render(<Edit config={config} onReload={async () => undefined} />);
    await act(async () => {
      buttonByText(container, 'connect-draft').click();
    });
    expect(updateNodeMock).toHaveBeenCalledWith('n_0000000001', expect.objectContaining({ provider: 'p_0000000001' }));
  });

  it('appends a route binding when a route is dragged onto an entrypoint', async () => {
    const { container } = render(<Edit config={config} onReload={async () => undefined} />);
    await act(async () => {
      buttonByText(container, 'connect-entrypoint').click();
    });

    expect(putEntrypointsMock).toHaveBeenCalledTimes(1);
    const [entrypoints] = putEntrypointsMock.mock.calls[0];
    expect(entrypoints[0].routes).toEqual([{ route: 'r_0000000001' }, { route: 'r_0000000002' }]);
  });

  it('does not save a duplicate route drag', async () => {
    const bound = {
      ...config,
      entrypoints: [{ ...config.entrypoints[0], routes: [{ route: 'r_0000000001' }, { route: 'r_0000000002' }] }],
    } as unknown as ConfigResponse;
    const { container } = render(<Edit config={bound} onReload={async () => undefined} />);
    await act(async () => {
      buttonByText(container, 'connect-entrypoint').click();
    });

    expect(putEntrypointsMock).not.toHaveBeenCalled();
  });
});

describe('EdgeForm entrypoint binding', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    window.matchMedia = vi.fn().mockReturnValue({
      matches: false,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
    }) as unknown as typeof window.matchMedia;
  });

  const config = {
    providers: [],
    nodes: [],
    routes: [
      { id: 'r_0000000001', name: 'primary', nodes: [] },
      { id: 'r_0000000002', name: 'backup', nodes: [] },
    ],
    entrypoints: [
      {
        id: 'e_0000000001',
        name: 'all',
        match: ['*'],
        routes: [{ route: 'r_0000000001' }, { route: 'r_0000000002' }],
      },
    ],
  } as unknown as ConfigResponse;

  it('disconnects only the binding the edge names', async () => {
    const { container } = render(
      <EdgeForm
        id="entrypoint:e_0000000001->route:r_0000000001"
        config={config}
        onSaved={async () => undefined}
      />,
    );

    await act(async () => {
      buttonByText(container, '断开').click();
    });

    expect(putEntrypointsMock).toHaveBeenCalledTimes(1);
    const [entrypoints] = putEntrypointsMock.mock.calls[0];
    expect(entrypoints[0].routes).toEqual([{ route: 'r_0000000002' }]);
  });

  it('leaves a draft when the edge names the last binding', async () => {
    const single = {
      ...config,
      entrypoints: [{ ...config.entrypoints[0], routes: [{ route: 'r_0000000001' }] }],
    } as unknown as ConfigResponse;
    const { container } = render(
      <EdgeForm
        id="entrypoint:e_0000000001->route:r_0000000001"
        config={single}
        onSaved={async () => undefined}
      />,
    );

    await act(async () => {
      buttonByText(container, '断开').click();
    });

    const [entrypoints] = putEntrypointsMock.mock.calls[0];
    expect(entrypoints[0].routes).toEqual([]);
  });
});

describe('draft affordance', () => {
  it('shows the draft placeholder on the node select and one empty binding row for an entrypoint', () => {
    const config = { providers: [], nodes: [], routes: [], entrypoints: [] } as unknown as ConfigResponse;
    const node: Node = { id: 'n_0000000001', name: 'draft', provider: '', model: '' };
    const nodeForm = render(<NodeForm node={node} config={config} onSaved={async () => undefined} />);
    expect(nodeForm.container.textContent).toContain('未连接（草稿）');

    const entrypointForm = render(
      <EntrypointForm
        entrypoint={{ id: 'e_0000000001', name: 'draft', match: ['*'], routes: [] }}
        index={0}
        config={config}
        onSaved={async () => undefined}
      />,
    );
    expect(entrypointForm.container.textContent).toContain('选择路由');
    expect(entrypointForm.container.textContent).toContain('没有绑定即为草稿');
  });
});

describe('EntrypointForm bindings', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    window.matchMedia = vi.fn().mockReturnValue({
      matches: false,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
    }) as unknown as typeof window.matchMedia;
  });

  const entrypoint = {
    id: 'e_0000000001',
    name: 'all',
    match: ['*'],
    routes: [
      { route: 'r_0000000001', probe: { command: 'curl -fsS http://127.0.0.1:9/ready', interval: '10s', timeout: '2s' } },
      { route: 'r_0000000002' },
    ],
  };

  const config = {
    providers: [],
    nodes: [],
    routes: [
      { id: 'r_0000000001', name: 'primary', nodes: [] },
      { id: 'r_0000000002', name: 'backup', nodes: [] },
    ],
    entrypoints: [entrypoint],
  } as unknown as ConfigResponse;

  it('round-trips the ordered bindings and their probe on save', async () => {
    const { container } = render(
      <EntrypointForm entrypoint={entrypoint} index={0} config={config} onSaved={async () => undefined} />,
    );

    await act(async () => {
      buttonByText(container, '保存').click();
    });

    expect(putEntrypointsMock).toHaveBeenCalledTimes(1);
    const [entrypoints] = putEntrypointsMock.mock.calls[0];
    expect(entrypoints[0].id).toBe('e_0000000001');
    expect(entrypoints[0].routes).toEqual([
      { route: 'r_0000000001', probe: { command: 'curl -fsS http://127.0.0.1:9/ready', interval: '10s', timeout: '2s' } },
      { route: 'r_0000000002' },
    ]);
  });

  it('reveals the probe fields behind the toggle and saves them', async () => {
    const draft = {
      id: 'e_0000000001',
      name: 'all',
      match: ['*'],
      routes: [{ route: 'r_0000000001' }],
    };
    const { container } = render(
      <EntrypointForm
        entrypoint={draft}
        index={0}
        config={{ ...config, entrypoints: [draft] } as unknown as ConfigResponse}
        onSaved={async () => undefined}
      />,
    );

    expect(container.querySelector('input[placeholder^="curl"]')).toBeNull();
    act(() => {
      buttonByText(container, '添加探测命令（可选）').click();
    });
    expect(container.textContent).toContain('移除探测命令');

    typeInto(container.querySelector('input[placeholder^="curl"]') as HTMLInputElement, 'true');
    typeInto(container.querySelector('input[placeholder="30s"]') as HTMLInputElement, '15s');
    typeInto(container.querySelector('input[placeholder="5s"]') as HTMLInputElement, '3s');

    await act(async () => {
      buttonByText(container, '保存').click();
    });

    const [entrypoints] = putEntrypointsMock.mock.calls[0];
    expect(entrypoints[0].routes).toEqual([
      { route: 'r_0000000001', probe: { command: 'true', interval: '15s', timeout: '3s' } },
    ]);
  });

  it('saves a draft when every binding row is cleared', async () => {
    const { container } = render(
      <EntrypointForm entrypoint={entrypoint} index={0} config={config} onSaved={async () => undefined} />,
    );

    const removeBinding = () => {
      const buttons = Array.from(container.querySelectorAll<HTMLButtonElement>('button[aria-label="删除绑定"]'));
      act(() => {
        buttons[0].click();
      });
    };
    removeBinding();
    removeBinding();

    await act(async () => {
      buttonByText(container, '保存').click();
    });

    const [entrypoints] = putEntrypointsMock.mock.calls[0];
    expect(entrypoints[0].routes).toEqual([]);
  });

  it('reorders a binding with the down control', async () => {
    const { container } = render(
      <EntrypointForm entrypoint={entrypoint} index={0} config={config} onSaved={async () => undefined} />,
    );

    const downButtons = Array.from(container.querySelectorAll<HTMLButtonElement>('button[aria-label="下移"]'));
    act(() => {
      downButtons[0].click();
    });

    await act(async () => {
      buttonByText(container, '保存').click();
    });

    const [entrypoints] = putEntrypointsMock.mock.calls[0];
    expect(entrypoints[0].routes?.map((b: { route: string }) => b.route)).toEqual([
      'r_0000000002',
      'r_0000000001',
    ]);
    expect(entrypoints[0].routes?.[1].probe?.command).toBe('curl -fsS http://127.0.0.1:9/ready');
  });
});

describe('AddEntrypointModal', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    window.matchMedia = vi.fn().mockReturnValue({
      matches: false,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
    }) as unknown as typeof window.matchMedia;
  });

  it('starts with one optional binding row and saves a draft when it is left empty', async () => {
    const config = {
      providers: [],
      nodes: [],
      routes: [{ id: 'r_0000000001', name: 'primary', nodes: [] }],
      entrypoints: [],
    } as unknown as ConfigResponse;
    render(
      <AddEntrypointModal
        open
        config={config}
        onClose={() => undefined}
        onDone={async () => undefined}
        onError={() => undefined}
      />,
    );

    // Earlier renders in this file leave their portals in the document, so use
    // the modal this test opened.
    const modals = Array.from(document.querySelectorAll<HTMLElement>('.ant-modal-content'));
    const modal = modals[modals.length - 1];
    expect(modal.textContent).toContain('选择路由');

    typeInto(modal.querySelector('input#name') as HTMLInputElement, 'new-ep');
    const match = modal.querySelector('.ant-select-selection-search-input') as HTMLInputElement;
    typeInto(match, 'gpt-*');
    act(() => {
      match.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', code: 'Enter', keyCode: 13, bubbles: true }));
    });

    await act(async () => {
      buttonByText(modal, 'OK').click();
    });

    expect(putEntrypointsMock).toHaveBeenCalledTimes(1);
    const [entrypoints] = putEntrypointsMock.mock.calls[0];
    expect(entrypoints[0].name).toBe('new-ep');
    expect(entrypoints[0].match).toEqual(['gpt-*']);
    expect(entrypoints[0].routes).toEqual([]);
  });
});

describe('draft round trip against the panel', () => {
  it('stages the provider in the panel when the node has no model yet', async () => {
    updateNodeMock.mockClear();
    const config = {
      gateway: { listen: ':8787' },
      admin: { listen: '', sessionTTL: '' },
      providers: [{ id: 'p_0000000001', name: 'p', type: 'generic', chat: 'https://a/v1', keys: [] }],
      nodes: [{ id: 'n_0000000001', name: 'draft', provider: '', model: '' }],
      routes: [{ id: 'r_0000000001', name: 'r', nodes: [] }],
      entrypoints: [{ id: 'e_0000000001', name: 'e', match: ['*'], routes: [{ route: 'r_0000000001' }] }],
    } as unknown as ConfigResponse;
    const { container } = render(<Edit config={config} onReload={async () => undefined} />);

    await act(async () => {
      buttonByText(container, 'connect-draft').click();
    });

    // Nothing is saved (a provider without a model is rejected), but the panel
    // opens on the node with the provider already chosen, so the operator only
    // has to pick a model.
    expect(updateNodeMock).not.toHaveBeenCalled();
    expect(container.textContent).toContain('已选好提供商');
    // The panel renders in a Drawer portal in jsdom, so query the document.
    expect(selectValue(document.body, 'provider')).toBe('p');
  });

  it('saves the node as a draft after the provider is cleared', async () => {
    updateNodeMock.mockClear();
    window.matchMedia = vi.fn().mockReturnValue({
      matches: false,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
    }) as unknown as typeof window.matchMedia;
    const node: Node = { id: 'n_0000000001', name: 'n', provider: 'p_0000000001', model: 'm1' };
    const config = {
      providers: [{ id: 'p_0000000001', name: 'p', type: 'generic', chat: 'https://a/v1', keys: [] }],
      nodes: [node],
      routes: [],
      entrypoints: [],
    } as unknown as ConfigResponse;
    const { container } = render(<NodeForm node={node} config={config} onSaved={async () => undefined} />);
    const clear = container.querySelector('.ant-select-clear');
    expect(clear).toBeTruthy();
    await act(async () => {
      clear!.dispatchEvent(new MouseEvent('mousedown', { bubbles: true }));
    });
    await act(async () => {
      buttonByText(container, '保存').click();
    });
    expect(updateNodeMock).toHaveBeenCalledWith(
      'n_0000000001',
      expect.objectContaining({ provider: '', model: '', key: '' }),
    );
  });
});
