import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import { NodeBody, handleStyle, selectionStyle } from './nodes';

describe('selectionStyle', () => {
  it('draws selection inside the box without changing its size', () => {
    const selected = selectionStyle(true, '#16a34a');
    expect(selected.borderColor).toBe('#0ea5e9');
    expect(selected.boxShadow).toContain('inset');
    expect(selected).not.toHaveProperty('borderWidth');
  });
});

describe('handleStyle', () => {
  it('enlarges the endpoint dot so it is easier to grab', () => {
    const style = handleStyle('#6366f1');
    expect(style.width).toBe(14);
    expect(style.height).toBe(14);
    expect(style.background).toBe('#6366f1');
  });
});

describe('NodeBody', () => {
  it('reserves the metrics row even when metrics are unavailable', () => {
    const empty = renderToStaticMarkup(<NodeBody label="route" />);
    const withMetrics = renderToStaticMarkup(
      <NodeBody
        label="route"
        metrics={{
          requests: 10,
          errors: 0,
          p50Ms: 3,
          p95Ms: 5,
          inputTokens: 0,
          outputTokens: 0,
          cachedTokens: 0,
        }}
      />,
    );

    // Same reserved row in both edit (no metrics) and monitor (metrics) modes,
    // so switching modes cannot resize the node or move its edges.
    expect(empty).toContain('height:15px');
    expect(empty).toContain('width:170px');
    expect(withMetrics).toContain('height:15px');
    expect(withMetrics).toContain('width:170px');
    expect(withMetrics).toContain('10 req');
  });

  it('omits the metrics row for nodes that never have stats', () => {
    const html = renderToStaticMarkup(<NodeBody label="unrouted" stats={false} />);
    expect(html).not.toContain('height:15px');
  });
});
