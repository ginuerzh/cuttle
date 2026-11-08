import { Position, type EdgeProps } from '@xyflow/react';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import { DotEdge } from './edges';

describe('DotEdge', () => {
  it('leaves the endpoint dots to the node handles', () => {
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
      style: { stroke: '#94a3b8' },
    };

    const html = renderToStaticMarkup(<DotEdge {...props} />);

    expect(html).toContain('react-flow__edge-path');
    expect(html).not.toContain('<circle');
  });

  it('highlights a selected edge', () => {
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
      style: { stroke: '#16a34a', strokeWidth: 2 },
      selected: true,
    };

    const html = renderToStaticMarkup(<DotEdge {...props} />);

    expect(html).toContain('stroke:#0ea5e9');
    expect(html).toContain('stroke-width:3.5');
  });

  it('moves one light dot once for an active pulse', () => {
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

    const html = renderToStaticMarkup(<DotEdge {...props} />);

    expect(html).toContain('gw-edge-dot');
    expect((html.match(/gw-edge-dot/g) || []).length).toBe(3);
    expect(html).not.toContain('animateMotion');
    expect(html).toContain('fill="#0ea5e9"');
    expect(html).toContain('fill="#fff"');
  });

  it('uses red light dots for an error pulse', () => {
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
      data: { pulse: 8, error: true, duration: 0.6 },
    };

    const html = renderToStaticMarkup(<DotEdge {...props} />);

    expect(html).toContain('fill="#dc2626"');
  });
});
