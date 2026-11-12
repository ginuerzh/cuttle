import type { Edge } from '@xyflow/react';
import { beforeEach, describe, expect, it } from 'vitest';
import { renderToStaticMarkup } from 'react-dom/server';
import type { EntrypointHealth, GraphMetrics } from '../api/types';
import type { GraphNode } from '../graph/build';
import { setActiveLang } from '../i18n';
import type { NodeRouteHealth } from './routeHealth';
import { bindingEdgeHealth } from './edges';
import { canEditTopology, decorateEdge, edgeLabel, mergeNodeHealth, viewportInit } from './Topology';

beforeEach(() => setActiveLang('zh-CN'));

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

  it('shows the discarded attempts instead of a zero request count', () => {
    const html = renderToStaticMarkup(
      <>{edgeLabel({ requests: 0, errors: 0, attempts: 2, retriedRequests: 2 })}</>,
    );
    const text = html.replace(/<[^>]+>/g, '');

    expect(text).toContain('↻2');
    expect(text.trim().startsWith('0')).toBe(false);
  });

  it('appends the attempt count after requests and errors', () => {
    const html = renderToStaticMarkup(
      <>{edgeLabel({ requests: 10, errors: 2, attempts: 3, retriedRequests: 2 })}</>,
    );
    const text = html.replace(/<[^>]+>/g, '');

    expect(text).toContain('10');
    expect(text).toContain('2');
    expect(text).toContain('↻3');
  });

  it('adds no attempt segment when nothing was discarded', () => {
    const html = renderToStaticMarkup(
      <>{edgeLabel({ requests: 10, errors: 0, attempts: 0, retriedRequests: 0 })}</>,
    );

    expect(html).not.toContain('↻');
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
      edges: { [edge.id]: { requests: 10, errors: 2, attempts: 3, retriedRequests: 2 } },
    } as unknown as GraphMetrics;

    const decorated = decorateEdge(edge, metrics, undefined, true, new Set([edge.id]));
    const html = renderToStaticMarkup(<>{decorated.label}</>);

    expect(html).toContain('10');
    expect(html).toContain('↻3');
    expect(html).toContain('dead');
    expect(decorated.style).toMatchObject({ stroke: '#dc2626', strokeDasharray: '6 4' });
  });

  it('labels a dead binding that only ever discarded attempts', () => {
    const edge = { id: 'route:r|node:n', source: 'route:r', target: 'node:n' } as Edge;
    const metrics = {
      nodes: {},
      edges: { [edge.id]: { requests: 0, errors: 0, attempts: 2, retriedRequests: 2 } },
    } as unknown as GraphMetrics;

    const decorated = decorateEdge(edge, metrics, undefined, true, new Set([edge.id]));
    const text = renderToStaticMarkup(<>{decorated.label}</>).replace(/<[^>]+>/g, '');

    expect(text).toContain('↻2');
    expect(text).toContain('dead');
    expect((decorated.style as { strokeWidth?: number }).strokeWidth ?? 0).toBeGreaterThan(1.5);
  });

  it('leaves a live binding solid', () => {
    const edge = { id: 'route:r|node:n', source: 'route:r', target: 'node:n' } as Edge;

    const decorated = decorateEdge(edge, null, undefined, true, new Set());

    expect(decorated.style).toMatchObject({ stroke: '#94a3b8' });
    expect((decorated.style as { strokeDasharray?: string }).strokeDasharray).toBeUndefined();
  });
});

describe('binding badges', () => {
  const edge = (routeId: string): Edge =>
    ({
      id: `entrypoint:e_0000000001->route:${routeId}`,
      source: 'entrypoint:e_0000000001',
      target: `route:${routeId}`,
    }) as Edge;

  const health = (activeRoute: string, fallback: boolean, bindings: [string, boolean][]): EntrypointHealth => ({
    entrypointId: 'e_0000000001',
    entrypointName: 'all',
    active: { routeId: activeRoute, fallback },
    bindings: bindings.map(([routeId, eligible], index) => ({
      index,
      routeId,
      routeName: routeId,
      eligible,
    })),
  });

  it('draws an ineligible inactive binding as dashed amber labelled 未通过', () => {
    const badges = bindingEdgeHealth([
      health('r_0000000002', false, [
        ['r_0000000001', false],
        ['r_0000000002', true],
      ]),
    ]);

    const decorated = decorateEdge(edge('r_0000000001'), null, undefined, true, undefined, badges);

    expect(decorated.style).toMatchObject({ stroke: '#d97706', strokeDasharray: '6 4' });
    expect(renderToStaticMarkup(<>{decorated.label}</>)).toContain('未通过');
  });

  it('keeps the active binding in its normal style', () => {
    const badges = bindingEdgeHealth([
      health('r_0000000002', false, [
        ['r_0000000001', false],
        ['r_0000000002', true],
      ]),
    ]);

    const decorated = decorateEdge(edge('r_0000000002'), null, undefined, true, undefined, badges);

    expect(decorated.style).toMatchObject({ stroke: '#94a3b8' });
    expect((decorated.style as { strokeDasharray?: string }).strokeDasharray).toBeUndefined();
    expect(renderToStaticMarkup(<>{decorated.label}</>)).not.toContain('未通过');
  });

  it('labels the binding x falls back to as 回退', () => {
    const badges = bindingEdgeHealth([
      health('r_0000000001', true, [
        ['r_0000000001', false],
        ['r_0000000002', false],
      ]),
    ]);

    const decorated = decorateEdge(edge('r_0000000001'), null, undefined, true, undefined, badges);

    expect(renderToStaticMarkup(<>{decorated.label}</>)).toContain('回退');
    expect((decorated.style as { strokeDasharray?: string }).strokeDasharray).toBeUndefined();
  });

  it('leaves the dead decoration of a route binding untouched', () => {
    const deadEdge = { id: 'route:r|node:n', source: 'route:r', target: 'node:n' } as Edge;
    const badges = bindingEdgeHealth([health('r_0000000001', false, [['r_0000000001', false]])]);

    const decorated = decorateEdge(deadEdge, null, undefined, true, new Set([deadEdge.id]), badges);

    expect(decorated.style).toMatchObject({ stroke: '#dc2626', strokeDasharray: '6 4' });
    expect(renderToStaticMarkup(<>{decorated.label}</>)).toContain('dead');
  });

  it('keeps an unbadged edge solid when the map has other bindings', () => {
    const badges = bindingEdgeHealth([health('r_0000000002', false, [['r_0000000001', false]])]);
    const other = { id: 'entrypoint:e_0000000001->route:r_0000000009', source: 'entrypoint:e_0000000001', target: 'route:r_0000000009' } as Edge;

    const decorated = decorateEdge(other, null, undefined, true, undefined, badges);

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

  it('resolves the dead routes to display names for the badge tooltip', () => {
    const [got] = mergeNodeHealth([node('a')], health([], [['a', ['r_0000000001']]]), (id) => `route ${id}`);

    expect(got.data.deadRoutes).toEqual(['r_0000000001']);
    expect(got.data.deadRouteLabels).toEqual(['route r_0000000001']);
  });

  it('refreshes the dead-route labels when a route is renamed', () => {
    const first = mergeNodeHealth([node('a')], health([], [['a', ['r1']]]), () => 'old');
    const renamed = mergeNodeHealth(first, health([], [['a', ['r1']]]), () => 'new');

    expect(renamed[0].data.deadRouteLabels).toEqual(['new']);
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
