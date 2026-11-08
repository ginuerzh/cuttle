// @vitest-environment jsdom
import { createRoot } from 'react-dom/client';
import { act } from 'react-dom/test-utils';
import { Position, type EdgeProps } from '@xyflow/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { DotEdge } from './edges';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const props: EdgeProps = {
  id: 'route:strong|node:deepseek-fast',
  source: 'route:strong',
  target: 'node:deepseek-fast',
  sourceX: 0,
  sourceY: 0,
  targetX: 120,
  targetY: 40,
  sourcePosition: Position.Right,
  targetPosition: Position.Left,
  style: { stroke: '#16a34a' },
  data: { pulse: 7, error: false, duration: 0.6 },
};

describe('DotEdge DOM', () => {
  let container: HTMLDivElement | null = null;
  let root: ReturnType<typeof createRoot> | null = null;
  let frames: FrameRequestCallback[] = [];

  beforeEach(() => {
    frames = [];
    window.matchMedia = vi.fn().mockReturnValue({
      matches: false,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
    });
    Object.defineProperty(Element.prototype, 'getTotalLength', {
      configurable: true,
      value: () => 100,
    });
    Object.defineProperty(Element.prototype, 'getPointAtLength', {
      configurable: true,
      value: (length: number) => ({ x: length, y: 0 }),
    });
    globalThis.requestAnimationFrame = (callback: FrameRequestCallback) => {
      frames.push(callback);
      return frames.length;
    };
    globalThis.cancelAnimationFrame = () => undefined;
  });

  afterEach(() => {
    act(() => root?.unmount());
    container?.remove();
    container = null;
    root = null;
  });

  it('moves the light dots along the edge path', () => {
    container = document.createElement('div');
    document.body.appendChild(container);
    root = createRoot(container);
    act(() => {
      root!.render(
        <svg>
          <DotEdge {...props} />
        </svg>,
      );
    });

    expect(container.querySelectorAll('.gw-edge-dot').length).toBe(3);
    const first = frames.shift();
    expect(first).toBeTypeOf('function');
    act(() => first!(performance.now() + 200));

    const dot = container.querySelector('.gw-edge-dot');
    expect(dot?.getAttribute('transform')).toMatch(/^translate\(/);
    expect(Number(dot?.getAttribute('opacity'))).toBeGreaterThan(0);
  });
});
