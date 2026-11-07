import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import type { NodeProps } from '@xyflow/react';
import { ReactFlowProvider } from '@xyflow/react';
import type { GraphNode } from './build';
import { EntrypointNode, NodeBody, NodeStats, handleStyle, selectionStyle } from './nodes';

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

describe('EntrypointNode', () => {
  it('uses the gateway icon instead of the old alias tag', () => {
    const props = {
      id: 'entrypoint:main',
      type: 'entrypoint',
      data: { kind: 'entrypoint', label: 'main' },
      selected: false,
      isConnectable: false,
    } as unknown as NodeProps<GraphNode>;

    const html = renderToStaticMarkup(
      <ReactFlowProvider>
        <EntrypointNode {...props} />
      </ReactFlowProvider>,
    );
    expect(html).toContain('anticon-gateway');
    expect(html).not.toContain('anticon-tag');
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
          outputTokensPerSecond: 0,
        }}
      />,
    );

    // Absolute and anchored to the card bottom: the caption must never change
    // the node size, otherwise switching edit/monitor moves every edge.
    expect(html).toContain('position:absolute');
    expect(html).toContain('top:100%');
    expect(html).toContain('text-align:center');
    expect(html).toContain('10 ·');
    expect(html).not.toContain('req');
  });

  it('colors the error rate red and drops the err suffix', () => {
    const html = renderToStaticMarkup(
      <NodeStats
        metrics={{
          requests: 10,
          errors: 2,
          p50Ms: 3,
          p95Ms: 5,
          inputTokens: 0,
          outputTokens: 0,
          cachedTokens: 0,
          outputTokensPerSecond: 0,
        }}
      />,
    );

    expect(html).toContain('10 ·');
    expect(html).toContain('20%');
    expect(html).toContain('color:#dc2626');
    expect(html).not.toContain('err');
    expect(html).not.toContain('req');
  });

  it('hides the error share when there are no errors', () => {
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
          outputTokensPerSecond: 0,
        }}
      />,
    );

    expect(html).toContain('10 · p95 5ms');
    expect(html).not.toContain('0.0%');
  });

  it('renders long p95 latencies in seconds, never as kms', () => {
    const html = renderToStaticMarkup(
      <NodeStats
        metrics={{
          requests: 10,
          errors: 0,
          p50Ms: 800,
          p95Ms: 1500,
          inputTokens: 0,
          outputTokens: 0,
          cachedTokens: 0,
          outputTokensPerSecond: 0,
        }}
      />,
    );

    expect(html).toContain('p95 1.5s');
    expect(html).not.toContain('kms');
  });

  it('shows the token rate only when asked and available', () => {
    const metrics = {
      requests: 10, errors: 0, p50Ms: 3, p95Ms: 5,
      inputTokens: 0, outputTokens: 80, cachedTokens: 0,
      outputTokensPerSecond: 42.34,
    };
    const withRate = renderToStaticMarkup(<NodeStats metrics={metrics} showRate />);
    expect(withRate).toContain('42.3 tok/s');

    const withoutRate = renderToStaticMarkup(<NodeStats metrics={metrics} />);
    expect(withoutRate).not.toContain('tok/s');
  });

  it('omits a zero token rate', () => {
    const html = renderToStaticMarkup(
      <NodeStats
        metrics={{
          requests: 10, errors: 0, p50Ms: 3, p95Ms: 5,
          inputTokens: 0, outputTokens: 0, cachedTokens: 0,
          outputTokensPerSecond: 0,
        }}
        showRate
      />,
    );
    expect(html).not.toContain('tok/s');
  });
});
