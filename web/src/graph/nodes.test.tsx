import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import type { NodeProps } from '@xyflow/react';
import { ReactFlowProvider } from '@xyflow/react';
import type { GraphNode } from './build';
import { BindingNode, EntrypointNode, NodeBody, NodeStats, handleStyle, nodeMood, selectionStyle } from './nodes';

const stats = (requests: number, errors: number) => ({
  requests,
  errors,
  p50Ms: 0,
  p95Ms: 0,
  inputTokens: 0,
  outputTokens: 0,
  cachedTokens: 0,
  outputTokensPerSecond: 0,
});

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

describe('nodeMood', () => {
  it('maps traffic health to the four visible states', () => {
    expect(nodeMood(undefined)).toBe('idle');
    expect(nodeMood(stats(0, 0))).toBe('idle');
    expect(nodeMood(stats(10, 0))).toBe('healthy');
    expect(nodeMood(stats(100, 1))).toBe('warn');
    expect(nodeMood(stats(10, 2))).toBe('error');
    expect(nodeMood(stats(10, 0), true)).toBe('dead');
  });
});

describe('BindingNode', () => {
  const props = {
    id: 'node:a',
    type: 'node',
    data: { kind: 'node', label: 'a' },
    selected: false,
    isConnectable: false,
  } as unknown as NodeProps<GraphNode>;

  const render = (data: Partial<GraphNode['data']>) => {
    const nodeProps = { ...props, data: { ...props.data, ...data } } as unknown as NodeProps<GraphNode>;
    return renderToStaticMarkup(
      <ReactFlowProvider>
        <BindingNode {...nodeProps} />
      </ReactFlowProvider>,
    );
  };

  it('hides the dead state and the mood badge for an idle node', () => {
    const html = render({});
    expect(html).toContain('background:#0891b2');
    expect(html).not.toContain('var(--gw-text-muted)');
    expect(html).not.toContain('data-status');
  });

  it('greys the node icon and shows the dead icon inside the top-right corner', () => {
    const html = render({ dead: true });
    expect(html).toContain('background:var(--gw-text-muted)');
    expect(html).toContain('data-status="dead"');
    expect(html).toContain('aria-label="dead"');
    expect(html).toContain('title="dead"');
    expect(html).toContain('viewBox="0 0 64 64"');
    expect(html).toContain('position:absolute');
    expect(html).toContain('top:4px');
    expect(html).toContain('right:4px');
    expect(html).not.toContain('ant-badge-status-error');
    expect(html).not.toContain('>dead<');
  });

  it('shows a smile for healthy traffic and a meh for elevated errors', () => {
    expect(render({ metrics: stats(10, 0) })).toContain('anticon-smile');
    expect(render({ metrics: stats(10, 0) })).toContain('data-status="healthy"');
    expect(render({ metrics: stats(100, 1) })).toContain('anticon-meh');
    expect(render({ metrics: stats(100, 1) })).toContain('data-status="warn"');
  });

  it('shows a frown for a high error rate', () => {
    const html = render({ metrics: stats(10, 2) });
    expect(html).toContain('anticon-frown');
    expect(html).toContain('data-status="error"');
    expect(html).toContain('#dc2626');
    expect(html).not.toContain('anticon-meh');
  });

  it('flashes the icon for a traffic pulse and turns red for errors', () => {
    const pulseProps = {
      ...props,
      data: { ...props.data, pulse: 3, error: false },
    } as unknown as NodeProps<GraphNode>;
    const html = renderToStaticMarkup(
      <ReactFlowProvider>
        <BindingNode {...pulseProps} />
      </ReactFlowProvider>,
    );
    expect(html).toContain('gw-node-flash');
    expect(html).not.toContain('gw-node-pulse');
    expect(html).not.toContain('gw-node-flash-error');

    const errorProps = {
      ...props,
      data: { ...props.data, pulse: 3, error: true },
    } as unknown as NodeProps<GraphNode>;
    const errorHtml = renderToStaticMarkup(
      <ReactFlowProvider>
        <BindingNode {...errorProps} />
      </ReactFlowProvider>,
    );
    expect(errorHtml).toContain('gw-node-flash-error');
  });

  it('does not flash while idle', () => {
    const html = renderToStaticMarkup(
      <ReactFlowProvider>
        <BindingNode {...props} />
      </ReactFlowProvider>,
    );
    expect(html).not.toContain('gw-node-flash');
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
