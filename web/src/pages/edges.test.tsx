import { Position, type Edge, type EdgeProps } from '@xyflow/react';
import { renderToStaticMarkup } from 'react-dom/server';
import { beforeEach, describe, expect, it } from 'vitest';
import type { EntrypointHealth } from '../api/types';
import { setActiveLang } from '../i18n';
import { bindingEdgeHealth, DotEdge } from './edges';
import { decorateEdge } from './Topology';

beforeEach(() => setActiveLang('zh-CN'));

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

describe('bindingEdgeHealth', () => {
  const health = (
    entrypointId: string,
    active: { routeId: string; fallback: boolean },
    bindings: [string, boolean][],
  ): EntrypointHealth => ({
    entrypointId,
    entrypointName: entrypointId,
    active,
    bindings: bindings.map(([routeId, eligible], index) => ({
      index,
      routeId,
      routeName: routeId,
      eligible,
    })),
  });

  it('keys each binding by the topology edge id', () => {
    const rows = bindingEdgeHealth([
      health('e_0000000001', { routeId: 'r_0000000002', fallback: false }, [
        ['r_0000000001', false],
        ['r_0000000002', true],
      ]),
    ]);

    expect(rows.get('entrypoint:e_0000000001->route:r_0000000001')).toEqual({
      eligible: false,
      active: false,
      fallback: false,
    });
    expect(rows.get('entrypoint:e_0000000001->route:r_0000000002')).toEqual({
      eligible: true,
      active: true,
      fallback: false,
    });
  });

  it('marks the fallback binding when nothing is eligible', () => {
    const rows = bindingEdgeHealth([
      health('e_0000000001', { routeId: 'r_0000000001', fallback: true }, [
        ['r_0000000001', false],
        ['r_0000000002', false],
      ]),
    ]);

    expect(rows.get('entrypoint:e_0000000001->route:r_0000000001')).toEqual({
      eligible: false,
      active: false,
      fallback: true,
    });
    expect(rows.get('entrypoint:e_0000000001->route:r_0000000002')).toEqual({
      eligible: false,
      active: false,
      fallback: false,
    });
  });

  it('keeps the first binding on duplicate rows', () => {
    const rows = bindingEdgeHealth([
      health('e_0000000001', { routeId: 'r_0000000002', fallback: false }, [
        ['r_0000000001', false],
        ['r_0000000001', true],
      ]),
    ]);

    expect(rows.get('entrypoint:e_0000000001->route:r_0000000001')?.eligible).toBe(false);
  });

  it('returns an empty map without health', () => {
    expect(bindingEdgeHealth(undefined).size).toBe(0);
  });

  it('preserves the selected flag when re-decorating an edge', () => {
    const selected = {
      id: 'entrypoint:e_0000000001->route:r_0000000001',
      source: 'entrypoint:e_0000000001',
      target: 'route:r_0000000001',
      selected: true,
    } as Edge;
    const badges = bindingEdgeHealth([
      health('e_0000000001', { routeId: 'r_0000000002', fallback: false }, [['r_0000000001', false]]),
    ]);

    const decorated = decorateEdge(selected, null, undefined, true, undefined, badges);

    expect(decorated.selected).toBe(true);
    expect(renderToStaticMarkup(<>{decorated.label}</>)).toContain('未通过');
  });
});
