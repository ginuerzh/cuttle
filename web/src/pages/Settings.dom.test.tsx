// @vitest-environment jsdom
import { createRoot } from 'react-dom/client';
import { act } from 'react-dom/test-utils';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { ConfigResponse } from '../api/types';

vi.mock('../api/client', () => ({
  changePassword: vi.fn(async () => undefined),
  putStatsCapture: vi.fn(async () => undefined),
}));

import { putStatsCapture } from '../api/client';
import { SettingsPage } from './Settings';

const capture = vi.mocked(putStatsCapture);

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const config = {
  gateway: { listen: ':8787' },
  admin: { listen: ':8788', sessionTTL: '12h' },
  stats: { capture: { mode: 'errors', sampleRate: 0.5, maxBytes: 65536, retain: '24h' } },
  entrypoints: [],
  providers: [],
  nodes: [],
  routes: [],
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

describe('SettingsPage capture form', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    window.matchMedia = vi.fn().mockReturnValue({
      matches: false,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
    }) as unknown as typeof window.matchMedia;
  });

  it('keeps the stored sample rate when saving outside sample mode', async () => {
    const { container } = render(<SettingsPage config={config} onLogout={() => undefined} />);

    const form = container.querySelector('form');
    expect(form).not.toBeNull();
    const submit = form!.querySelector('button[type="submit"]') as HTMLButtonElement;
    expect(submit).not.toBeNull();

    await act(async () => {
      submit.click();
    });

    expect(capture).toHaveBeenCalledTimes(1);
    expect(capture).toHaveBeenCalledWith({
      mode: 'errors',
      sampleRate: 0.5,
      maxBytes: 65536,
      retain: '24h',
    });
  });
});
