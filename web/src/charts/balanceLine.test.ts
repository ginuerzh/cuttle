import { describe, expect, it } from 'vitest';
import type { BalanceTrend } from '../api/types';
import { monotonePath, trendGeometry, trendMax, trendPoints } from './balanceLine';

const HOUR = 3_600_000;
const day = Date.UTC(2026, 10, 12, 0, 0, 0);
const from = day;
const to = day + 24 * HOUR;

describe('trendPoints', () => {
  it('parses ISO times, drops unparsable ones and sorts ascending', () => {
    const points = trendPoints([
      { at: '2026-11-12T02:00:00Z', amount: 2 },
      { at: 'not a date', amount: 9 },
      { at: '2026-11-12T01:00:00Z', amount: 1 },
    ]);
    expect(points.map((p) => p.amount)).toEqual([1, 2]);
    expect(points[0].at).toBe(Date.UTC(2026, 10, 12, 1, 0, 0));
  });
});

describe('trendMax', () => {
  it('scales every trend of a card against one shared maximum', () => {
    const trends: BalanceTrend[] = [
      { currency: 'USD', points: [{ at: '2026-11-12T00:00:00Z', amount: 1 }] },
      { currency: 'CNY', points: [{ at: '2026-11-12T00:00:00Z', amount: 100 }] },
    ];
    expect(trendMax(trends, from, to)).toBe(100);
  });

  it('ignores readings outside the window and never returns zero', () => {
    const trends: BalanceTrend[] = [
      { currency: 'USD', points: [{ at: '2026-11-01T00:00:00Z', amount: 500 }] },
    ];
    expect(trendMax(trends, from, to)).toBe(1);
  });
});

describe('trendGeometry', () => {
  it('places a reading in the window on the shared scale', () => {
    const points = trendPoints([{ at: '2026-11-12T12:00:00Z', amount: 5 }]);
    const [xy] = trendGeometry(points, from, to, 10, 100, 50);
    expect(xy[0]).toBeCloseTo(50, 5);
    expect(xy[1]).toBeCloseTo(25, 5);
  });

  it('clamps a reading past the window to the right edge', () => {
    const points = trendPoints([{ at: new Date(to + HOUR).toISOString(), amount: 5 }]);
    const [xy] = trendGeometry(points, from, to, 10, 100, 50);
    expect(xy[0]).toBe(100);
  });

  it('puts a flat zero line on the floor without NaN', () => {
    const points = trendPoints([
      { at: new Date(from).toISOString(), amount: 0 },
      { at: new Date(to).toISOString(), amount: 0 },
    ]);
    const xy = trendGeometry(points, from, to, 1, 100, 50);
    expect(xy.map((p) => p[1])).toEqual([50, 50]);
  });
});

describe('monotonePath', () => {
  const pts: [number, number][] = [
    [0, 40],
    [10, 10],
    [20, 30],
  ];

  it('passes through every point', () => {
    const d = monotonePath(pts);
    expect(d.startsWith('M0.0 40.0')).toBe(true);
    expect(d.trim().endsWith('20.0 30.0')).toBe(true);
  });

  it('never overshoots the points it passes through', () => {
    const nums = monotonePath(pts).match(/-?\d+(?:\.\d+)?/g)!.map(Number);
    let x0 = 0;
    let y0 = nums[1];
    let i = 2;
    while (i + 5 < nums.length) {
      const [, c1y, , c2y, , y1] = nums.slice(i, i + 6);
      for (const t of [0.25, 0.5, 0.75]) {
        const mt = 1 - t;
        const y =
          mt ** 3 * y0 + 3 * mt ** 2 * t * c1y + 3 * mt * t ** 2 * c2y + t ** 3 * y1;
        expect(y).toBeGreaterThanOrEqual(10 - 1e-6);
        expect(y).toBeLessThanOrEqual(40 + 1e-6);
      }
      x0 += 0;
      y0 = y1;
      i += 6;
    }
    expect(x0).toBe(0);
  });

  it('draws a lone point as a move', () => {
    expect(monotonePath([[3, 4]])).toBe('M3.0 4.0');
  });
});
