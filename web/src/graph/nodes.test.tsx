import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import { NodeBody, NodeStats, handleStyle, selectionStyle } from './nodes';

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
  it('renders only the stable label and sub-lines', () => {
    const html = renderToStaticMarkup(<NodeBody label="route" sub="4 nodes · round" />);
    expect(html).toContain('route');
    expect(html).toContain('4 nodes · round');
    expect(html).not.toContain('position:absolute');
  });
});

describe('NodeStats', () => {
  it('renders nothing when metrics are unavailable', () => {
    expect(renderToStaticMarkup(<NodeStats />)).toBe('');
  });

  it('renders the metrics outside the node flow', () => {
    const html = renderToStaticMarkup(
      <NodeStats
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

    // Absolute and anchored to the card bottom: the caption must never change
    // the node size, otherwise switching edit/monitor moves every edge.
    expect(html).toContain('position:absolute');
    expect(html).toContain('top:100%');
    expect(html).toContain('text-align:center');
    expect(html).toContain('10 req');
  });
});
