// @vitest-environment jsdom
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { getConfig, UnauthorizedError } from './api/client';
import type { SnapshotMessage } from './api/events';
import { useLiveGraph } from './useLiveGraph';

vi.mock('./api/client', async (importOriginal) => {
  const actual = await importOriginal<typeof import('./api/client')>();
  return { ...actual, getConfig: vi.fn() };
});

const emptyMetrics = { nodes: {}, edges: {}, errors: [] };

class FakeWebSocket {
  static instances: FakeWebSocket[] = [];

  url: string;
  closed = false;
  onmessage: ((event: MessageEvent) => void) | null = null;
  onerror: ((event: Event) => void) | null = null;
  onclose: ((event: CloseEvent) => void) | null = null;

  constructor(url: string | URL) {
    this.url = String(url);
    FakeWebSocket.instances.push(this);
  }

  close() {
    this.closed = true;
  }

  snapshot(since: string, requests = 0) {
    const metrics =
      requests === 0
        ? emptyMetrics
        : {
            ...emptyMetrics,
            nodes: {
              'provider:p': {
                requests,
                errors: 0,
                p50Ms: 0,
                p95Ms: 0,
                inputTokens: 0,
                outputTokens: 0,
                cachedTokens: 0,
                outputTokensPerSecond: 0,
              },
            },
          };
    this.onmessage?.({
      data: JSON.stringify({ type: 'snapshot', since, to: since, metrics }),
    } as MessageEvent);
  }

  closeWith(code: number) {
    this.closed = true;
    this.onclose?.({ code } as CloseEvent);
  }
}

type HarnessProps = {
  enabled: boolean;
  range: string;
  resetKey: number;
  sessionKey: number;
  onSnapshot: (msg: SnapshotMessage) => void;
  onStatus: (connected: boolean) => void;
  onAuthError: () => void;
};

function Harness(props: HarnessProps) {
  useLiveGraph(props);
  return null;
}

const originalWebSocket = globalThis.WebSocket;

let container: HTMLDivElement;
let root: Root;
let hidden = false;

function setup(overrides: Partial<HarnessProps> = {}) {
  const onSnapshot = vi.fn();
  const onStatus = vi.fn();
  const onAuthError = vi.fn();
  const props: HarnessProps = {
    enabled: true,
    range: '1h',
    resetKey: 0,
    sessionKey: 0,
    onSnapshot,
    onStatus,
    onAuthError,
    ...overrides,
  };
  act(() => {
    root.render(<Harness {...props} />);
  });
  const rerender = (next: Partial<HarnessProps>) => {
    Object.assign(props, next);
    act(() => {
      root.render(<Harness {...props} />);
    });
  };
  return { onSnapshot, onStatus, onAuthError, props, rerender };
}

async function flush() {
  await act(async () => {
    await Promise.resolve();
  });
}

async function advance(ms: number) {
  await act(async () => {
    await vi.advanceTimersByTimeAsync(ms);
  });
}

beforeEach(() => {
  FakeWebSocket.instances = [];
  container = document.createElement('div');
  document.body.appendChild(container);
  root = createRoot(container);
  hidden = false;
  Object.defineProperty(document, 'hidden', {
    configurable: true,
    get: () => hidden,
  });
  globalThis.WebSocket = FakeWebSocket as unknown as typeof WebSocket;
  vi.useFakeTimers();
  vi.mocked(getConfig).mockReset();
  vi.mocked(getConfig).mockResolvedValue({} as never);
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
});

afterEach(async () => {
  await act(async () => {
    root.unmount();
  });
  container.remove();
  vi.useRealTimers();
  globalThis.WebSocket = originalWebSocket;
  delete (document as unknown as Record<string, unknown>).hidden;
});

describe('useLiveGraph', () => {
  it('subscribes with the range and resumes with the server since', async () => {
    const { onSnapshot, onStatus } = setup();
    expect(FakeWebSocket.instances).toHaveLength(1);
    expect(new URL(FakeWebSocket.instances[0].url).searchParams.get('range')).toBe('1h');

    FakeWebSocket.instances[0].snapshot('2026-11-06T11:00:00Z', 3);
    expect(onSnapshot).toHaveBeenCalledTimes(1);
    expect(onStatus).toHaveBeenLastCalledWith(true);

    FakeWebSocket.instances[0].closeWith(1006);
    await flush();
    expect(FakeWebSocket.instances).toHaveLength(1);

    await advance(1500);
    expect(FakeWebSocket.instances).toHaveLength(2);
    expect(new URL(FakeWebSocket.instances[1].url).searchParams.get('since')).toBe(
      '2026-11-06T11:00:00Z',
    );
  });

  it('reconnects with reset=1 when resetKey changes', () => {
    const { rerender } = setup();
    FakeWebSocket.instances[0].snapshot('2026-11-06T11:00:00Z', 1);
    rerender({ resetKey: 1 });

    expect(FakeWebSocket.instances).toHaveLength(2);
    expect(FakeWebSocket.instances[0].closed).toBe(true);
    expect(new URL(FakeWebSocket.instances[1].url).searchParams.get('reset')).toBe('1');
  });

  it('reconnects with the range after a new session', () => {
    const { rerender } = setup();
    FakeWebSocket.instances[0].snapshot('2026-11-06T11:00:00Z', 1);
    rerender({ sessionKey: 1 });

    expect(FakeWebSocket.instances).toHaveLength(2);
    expect(new URL(FakeWebSocket.instances[1].url).searchParams.get('range')).toBe('1h');
  });

  it('stops on 4401 and reports an auth error', () => {
    const { onAuthError } = setup();
    FakeWebSocket.instances[0].closeWith(4401);

    expect(onAuthError).toHaveBeenCalledTimes(1);
    act(() => {
      vi.advanceTimersByTime(5000);
    });
    expect(FakeWebSocket.instances).toHaveLength(1);
    expect(getConfig).not.toHaveBeenCalled();
  });

  it('falls back to the range on 4408', async () => {
    setup();
    FakeWebSocket.instances[0].snapshot('2026-11-06T11:00:00Z', 1);
    FakeWebSocket.instances[0].closeWith(4408);

    await advance(1500);
    expect(FakeWebSocket.instances).toHaveLength(2);
    expect(new URL(FakeWebSocket.instances[1].url).searchParams.get('range')).toBe('1h');
  });

  it('probes the session after an unexpected close and stops on 401', async () => {
    const { onAuthError } = setup();
    vi.mocked(getConfig).mockRejectedValueOnce(new UnauthorizedError());

    FakeWebSocket.instances[0].closeWith(1006);
    await flush();

    expect(getConfig).toHaveBeenCalledTimes(1);
    expect(onAuthError).toHaveBeenCalledTimes(1);
    act(() => {
      vi.advanceTimersByTime(5000);
    });
    expect(FakeWebSocket.instances).toHaveLength(1);
  });

  it('closes while hidden and resumes with the stored since', () => {
    setup();
    FakeWebSocket.instances[0].snapshot('2026-11-06T11:00:00Z', 1);

    hidden = true;
    act(() => {
      document.dispatchEvent(new Event('visibilitychange'));
    });
    expect(FakeWebSocket.instances[0].closed).toBe(true);

    hidden = false;
    act(() => {
      document.dispatchEvent(new Event('visibilitychange'));
    });
    expect(FakeWebSocket.instances).toHaveLength(2);
    expect(new URL(FakeWebSocket.instances[1].url).searchParams.get('since')).toBe(
      '2026-11-06T11:00:00Z',
    );
  });

  it('ignores snapshots from a replaced socket', () => {
    const { onSnapshot, rerender } = setup();
    const stale = FakeWebSocket.instances[0];
    stale.snapshot('2026-11-06T11:00:00Z', 1);
    const calls = onSnapshot.mock.calls.length;

    rerender({ resetKey: 1 });
    stale.snapshot('2026-11-06T11:00:00Z', 99);

    expect(onSnapshot).toHaveBeenCalledTimes(calls);
  });
});
