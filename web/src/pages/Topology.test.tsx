import { describe, expect, it } from 'vitest';
import { renderToStaticMarkup } from 'react-dom/server';
import { canEditTopology, edgeLabel, viewportInit } from './Topology';

describe('viewportInit', () => {
  it('fits the graph on the first visit', () => {
    expect(viewportInit(null)).toEqual({ fitView: true });
  });

  it('restores the saved viewport across a remount instead of fitting again', () => {
    const saved = { x: 120, y: -40, zoom: 1.35 };
    expect(viewportInit(saved)).toEqual({ fitView: false, defaultViewport: saved });
  });
});

describe('canEditTopology', () => {
  it('keeps monitor mode read-only when editable is omitted', () => {
    expect(canEditTopology(undefined)).toBe(false);
    expect(canEditTopology(false)).toBe(false);
  });

  it('allows connections and dragging only in explicit edit mode', () => {
    expect(canEditTopology(true)).toBe(true);
  });
});

describe('edgeLabel', () => {
  it('drops the err suffix and colors only the error count', () => {
    const html = renderToStaticMarkup(<>{edgeLabel({ requests: 10, errors: 2 })}</>);

    expect(html).toContain('10');
    expect(html).toContain('2');
    expect(html).toContain('color:#dc2626');
    expect(html).not.toContain('err');
  });

  it('shows only the total when there are no errors', () => {
    const html = renderToStaticMarkup(<>{edgeLabel({ requests: 10, errors: 0 })}</>);

    expect(html).toContain('10');
    expect(html).not.toContain('#dc2626');
  });
});
