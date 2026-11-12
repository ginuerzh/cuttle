// @vitest-environment jsdom
import { createRoot } from 'react-dom/client';
import { act } from 'react-dom/test-utils';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { BalanceChart } from './BalanceChart';
import type { ChartLine } from './BalanceChart';
import { setActiveLang } from '../i18n';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const from = '2026-11-12T00:00:00Z';
const to = '2026-11-13T00:00:00Z';

function render(el: React.ReactNode) {
  const container = document.createElement('div');
  document.body.appendChild(container);
  const root = createRoot(container);
  act(() => {
    root.render(el);
  });
  return { container, root };
}

function line(
  name: string,
  currency: string,
  points: Array<{ at: string; amount: number }>,
): ChartLine {
  return { name, trend: { currency, points } };
}

const usd = line('key-1', 'USD', [
  { at: '2026-11-12T01:00:00Z', amount: 20 },
  { at: '2026-11-12T02:00:00Z', amount: 11.12 },
]);

beforeEach(() => setActiveLang('zh-CN'));
afterEach(() => {
  document.body.innerHTML = '';
});

describe('BalanceChart', () => {
  it('draws one line per account and currency', () => {
    const cny = line('key-1', 'CNY', [
      { at: '2026-11-12T01:00:00Z', amount: 3 },
      { at: '2026-11-12T02:00:00Z', amount: 2 },
    ]);
    const { container } = render(<BalanceChart lines={[usd, cny]} from={from} to={to} />);
    expect(container.querySelectorAll('path[data-series="balance"]')).toHaveLength(2);
    expect(container.querySelectorAll('path[data-series="balance-area"]')).toHaveLength(2);
  });

  it('describes a reading with its time, amount and currency', () => {
    const { container } = render(<BalanceChart lines={[usd]} from={from} to={to} />);
    const circles = container.querySelectorAll('circle[data-point]');
    expect(circles).toHaveLength(2);
    const titles = Array.from(circles).map((c) => c.querySelector('title')?.textContent ?? '');
    expect(titles[1]).toContain('11.12');
    expect(titles[1]).toContain('USD');
  });

  it('scales every line against one shared maximum', () => {
    const small = line('key-1', 'USD', [
      { at: '2026-11-12T01:00:00Z', amount: 100 },
      { at: '2026-11-12T02:00:00Z', amount: 100 },
    ]);
    const tiny = line('key-2', 'CNY', [
      { at: '2026-11-12T01:00:00Z', amount: 1 },
      { at: '2026-11-12T02:00:00Z', amount: 1 },
    ]);
    const { container } = render(<BalanceChart lines={[small, tiny]} from={from} to={to} />);
    const second = container.querySelector('[data-trend="1"]');
    const cy = Number(second?.querySelector('circle[data-point]')?.getAttribute('cy'));
    expect(cy).toBeGreaterThan(72 * 0.85);
  });

  it('asks for more history when no line has two readings', () => {
    const lone = line('key-1', 'USD', [{ at: '2026-11-12T01:00:00Z', amount: 20 }]);
    const { container } = render(<BalanceChart lines={[lone]} from={from} to={to} />);
    expect(container.textContent).toContain('余额历史不足');
    expect(container.querySelector('svg')).toBeNull();
  });

  it('renders in English without any Chinese left', () => {
    setActiveLang('en-US');
    const lone = line('key-1', 'USD', [{ at: '2026-11-12T01:00:00Z', amount: 20 }]);
    const { container } = render(<BalanceChart lines={[lone]} from={from} to={to} />);
    expect(container.textContent).not.toMatch(/[\u4e00-\u9fff]/);
  });
});
