import { describe, expect, it } from 'vitest';
import { renderToStaticMarkup } from 'react-dom/server';
import { canEditTopology, edgeLabel } from './Topology';

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
