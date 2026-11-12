// @vitest-environment jsdom
import { createRoot } from 'react-dom/client';
import { act } from 'react-dom/test-utils';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { SessionChart } from './SessionChart';
import { setActiveLang } from '../i18n';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const from = Date.UTC(2026, 10, 11, 10, 0, 0);
const to = from + 3 * 60_000;
const bucket = 60_000;

function render(el: React.ReactNode) {
  const container = document.createElement('div');
  document.body.appendChild(container);
  const root = createRoot(container);
  act(() => {
    root.render(el);
  });
  return { container, root };
}

beforeEach(() => setActiveLang('zh-CN'));

describe('SessionChart', () => {
  it('draws one bar per bucket, including the empty ones', () => {
    const { container } = render(
      <SessionChart
        points={[{ bucket: new Date(from).toISOString(), requests: 2, errors: 1 }]}
        from={from}
        to={to}
        bucket={bucket}
        onSelect={() => undefined}
      />,
    );

    expect(container.querySelectorAll('rect[data-bar]')).toHaveLength(3);
  });

  it('stacks the errors above the sessions in red', () => {
    const { container } = render(
      <SessionChart
        points={[{ bucket: new Date(from).toISOString(), requests: 5, errors: 2 }]}
        from={from}
        to={to}
        bucket={bucket}
        onSelect={() => undefined}
      />,
    );

    const errors = container.querySelectorAll('rect[data-series="errors"]');
    expect(errors).toHaveLength(1);
    expect(errors[0].getAttribute('fill')).toBe('#dc2626');
    expect(container.querySelectorAll('rect[data-series="requests"]')).toHaveLength(3);
  });

  it('describes a bar with its clock time and counts', () => {
    const { container } = render(
      <SessionChart
        points={[{ bucket: new Date(from).toISOString(), requests: 5, errors: 2 }]}
        from={from}
        to={to}
        bucket={bucket}
        onSelect={() => undefined}
      />,
    );

    const titles = Array.from(container.querySelectorAll('title')).map((el) => el.textContent ?? '');
    expect(titles.some((text) => text.includes('5 会话 / 2 错误'))).toBe(true);
  });

  it('selects a bucket on click and clears it on the second click', () => {
    const onSelect = vi.fn();
    const { container, root } = render(
      <SessionChart
        points={[{ bucket: new Date(from).toISOString(), requests: 1, errors: 0 }]}
        from={from}
        to={to}
        bucket={bucket}
        onSelect={onSelect}
      />,
    );
    const bar = container.querySelector('rect[data-bar="1"]') as unknown as SVGRectElement;

    act(() => {
      bar.dispatchEvent(new MouseEvent('click', { bubbles: true }));
    });
    expect(onSelect).toHaveBeenLastCalledWith(from + bucket);

    // The page stores the zoom and hands it back as `selected`; a second click
    // on the selected bucket asks it to clear.
    act(() => {
      root.render(
        <SessionChart
          points={[{ bucket: new Date(from).toISOString(), requests: 1, errors: 0 }]}
          from={from}
          to={to}
          bucket={bucket}
          onSelect={onSelect}
          selected={from + bucket}
        />,
      );
    });
    const selectedBar = container.querySelector('rect[data-bar="1"]') as unknown as SVGRectElement;
    act(() => {
      selectedBar.dispatchEvent(new MouseEvent('click', { bubbles: true }));
    });
    expect(onSelect).toHaveBeenLastCalledWith(undefined);
  });

  it('labels the x axis with the time of each labelled bar', () => {
    // Local time so the labels do not depend on the runner's time zone.
    const start = new Date(2026, 10, 11, 10, 0, 0).getTime();
    const { container } = render(
      <SessionChart
        points={[{ bucket: new Date(start).toISOString(), requests: 1, errors: 0 }]}
        from={start}
        to={start + 3 * 60_000}
        bucket={60_000}
        onSelect={() => undefined}
      />,
    );

    const ticks = Array.from(container.querySelectorAll('[data-tick]')).map((el) => el.textContent);
    expect(ticks).toEqual(['10:00', '10:01', '10:02']);
  });

  it('spreads six x axis labels over a 30 bar window', () => {
    const start = new Date(2026, 10, 11, 10, 0, 0).getTime();
    const { container } = render(
      <SessionChart
        points={[{ bucket: new Date(start).toISOString(), requests: 1, errors: 0 }]}
        from={start}
        to={start + 60 * 60_000}
        bucket={2 * 60_000}
        onSelect={() => undefined}
      />,
    );

    expect(container.querySelectorAll('rect[data-bar]')).toHaveLength(30);
    const ticks = Array.from(container.querySelectorAll('[data-tick]')).map((el) => el.textContent);
    expect(ticks).toEqual(['10:00', '10:12', '10:24', '10:34', '10:46', '10:58']);
  });

  it('says so when the window has no sessions', () => {
    const { container } = render(
      <SessionChart points={[]} from={from} to={to} bucket={bucket} onSelect={() => undefined} />,
    );

    expect(container.textContent).toContain('窗口内没有会话');
  });

  it('reports a failed series without hiding the rest of the page', () => {
    const { container } = render(
      <SessionChart points={[]} from={from} to={to} bucket={bucket} onSelect={() => undefined} failed />,
    );

    expect(container.textContent).toContain('趋势加载失败');
  });
});
