import type { Edge } from '@xyflow/react';
import { describe, expect, it } from 'vitest';
import { renderToStaticMarkup } from 'react-dom/server';
import type { GraphMetrics } from '../api/types';
import type { GraphNode } from '../graph/build';
import type { NodeRouteHealth } from './routeHealth';
import { canEditTopology, decorateEdge, edgeLabel, mergeNodeHealth, viewportInit } from './Topology';

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

describe('decorateEdge', () => {
  it('carries the one-shot pulse into the edge data', () => {
    const edge = {
      id: 'route:r|node:n',
      source: 'route:r',
      target: 'node:n',
      style: { stroke: '#16a34a' },
    } as Edge;

    const decorated = decorateEdge(edge, null, { pulse: 7, error: true, duration: 0.6 });

    expect(decorated.data).toMatchObject({ pulse: 7, error: true, duration: 0.6 });
  });

  it('suppresses the pulse when animations are off', () => {
    const edge = { id: 'route:r|node:n', source: 'route:r', target: 'node:n' } as Edge;
    const decorated = decorateEdge(edge, null, { pulse: 7, error: false, duration: 0.6 }, false);
    expect(decorated.data).toMatchObject({ pulse: 0 });
  });

  it('draws a dead binding as a red dashed edge labelled dead', () => {
    const edge = { id: 'route:r|node:n', source: 'route:r', target: 'node:n' } as Edge;

    const decorated = decorateEdge(edge, null, undefined, true, new Set([edge.id]));

    expect(decorated.style).toMatchObject({ stroke: '#dc2626', strokeDasharray: '6 4' });
    expect(renderToStaticMarkup(<>{decorated.label}</>)).toContain('dead');
  });

  it('keeps the traffic counts and still marks a dead binding', () => {
    const edge = { id: 'route:r|node:n', source: 'route:r', target: 'node:n' } as Edge;
    const metrics = {
      nodes: {},
      edges: { [edge.id]: { requests: 10, errors: 2 } },
    } as unknown as GraphMetrics;

    const decorated = decorateEdge(edge, metrics, undefined, true, new Set([edge.id]));
    const html = renderToStaticMarkup(<>{decorated.label}</>);

    expect(html).toContain('10');
    expect(html).toContain('dead');
    expect(decorated.style).toMatchObject({ stroke: '#dc2626', strokeDasharray: '6 4' });
  });

  it('leaves a live binding solid', () => {
    const edge = { id: 'route:r|node:n', source: 'route:r', target: 'node:n' } as Edge;

    const decorated = decorateEdge(edge, null, undefined, true, new Set());

    expect(decorated.style).toMatchObject({ stroke: '#94a3b8' });
    expect((decorated.style as { strokeDasharray?: string }).strokeDasharray).toBeUndefined();
  });
});

describe('mergeNodeHealth', () => {
  const node = (name: string, data: Partial<GraphNode['data']> = {}): GraphNode => ({
    id: `node:${name}`,
    type: 'node',
    position: { x: 0, y: 0 },
    data: { kind: 'node', label: name, ...data },
  });
  const health = (dead: string[], partial: [string, string[]][]): NodeRouteHealth => ({
    dead: new Set(dead),
    partial: new Map(partial),
    edges: new Set(),
  });

  it('marks a partially dead node with the routes that report it dead', () => {
    const [got] = mergeNodeHealth([node('a')], health([], [['a', ['r1']]]));

    expect(got.data.dead).toBe(false);
    expect(got.data.deadRoutes).toEqual(['r1']);
  });

  it('marks an all-dead node and clears a stale partial state', () => {
    const [got] = mergeNodeHealth([node('a', { deadRoutes: ['r1'] })], health(['a'], []));

    expect(got.data.dead).toBe(true);
    expect(got.data.deadRoutes).toEqual([]);
  });

  it('leaves unchanged and non-binding nodes untouched', () => {
    const healthy = node('a');
    const route = {
      id: 'route:r',
      type: 'route',
      position: { x: 0, y: 0 },
      data: { kind: 'route', label: 'r' },
    } as GraphNode;

    const got = mergeNodeHealth([healthy, route], health([], []));

    expect(got[0]).toBe(healthy);
    expect(got[1]).toBe(route);
  });
});
