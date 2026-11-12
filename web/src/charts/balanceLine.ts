import type { BalanceTrend, BalanceTrendPoint } from '../api/types';

/** A reading placed on the chart's own axes: `at` is unix ms. */
export interface TrendPoint {
  at: number;
  amount: number;
}

/** Parses a payload's readings, dropping unparsable times, sorted by time. */
export function trendPoints(points: BalanceTrendPoint[]): TrendPoint[] {
  const out: TrendPoint[] = [];
  for (const p of points) {
    const at = Date.parse(p.at);
    if (Number.isNaN(at) || !Number.isFinite(p.amount)) continue;
    out.push({ at, amount: p.amount });
  }
  out.sort((a, b) => a.at - b.at);
  return out;
}

/**
 * The largest reading of the window across every line of one card, at least 1.
 * One shared maximum is what keeps a $0.50 line from looking like a $500 line.
 */
export function trendMax(trends: BalanceTrend[], from: number, to: number): number {
  let max = 0;
  for (const trend of trends) {
    for (const p of trendPoints(trend.points)) {
      if (p.at < from || p.at > to) continue;
      if (p.amount > max) max = p.amount;
    }
  }
  return max > 0 ? max : 1;
}

/**
 * Places readings inside a `width` × `height` box: x by the window (clamped, so
 * a reading a second past `to` sits on the right edge) and y up from the floor.
 */
export function trendGeometry(
  points: TrendPoint[],
  from: number,
  to: number,
  maxY: number,
  width: number,
  height: number,
): Array<[number, number]> {
  const span = to - from;
  const scale = maxY > 0 ? maxY : 1;
  return points.map((p) => {
    const ratio = span > 0 ? (p.at - from) / span : 1;
    const x = Math.max(0, Math.min(1, ratio)) * width;
    const y = height - Math.max(0, Math.min(1, p.amount / scale)) * height;
    return [x, y];
  });
}

/**
 * A smooth path through `points` as d3's curveMonotoneX draws it
 * (Fritsch–Carlson): it bends between points but never past one, so a quiet
 * hour is not drawn dipping under zero or a peak overshooting its value.
 */
export function monotonePath(points: Array<[number, number]>): string {
  const f = (v: number) => v.toFixed(1);
  const n = points.length;
  if (n === 0) return '';
  let d = `M${f(points[0][0])} ${f(points[0][1])}`;
  if (n === 1) return d;

  const slope: number[] = [];
  for (let i = 0; i < n - 1; i++) {
    const dx = points[i + 1][0] - points[i][0];
    slope.push(dx === 0 ? 0 : (points[i + 1][1] - points[i][1]) / dx);
  }
  const m: number[] = new Array(n).fill(0);
  for (let i = 1; i < n - 1; i++) {
    const h0 = points[i][0] - points[i - 1][0];
    const h1 = points[i + 1][0] - points[i][0];
    if (h0 <= 0 || h1 <= 0) continue;
    const p = (slope[i - 1] * h1 + slope[i] * h0) / (h0 + h1);
    m[i] =
      slope[i - 1] * slope[i] <= 0
        ? 0
        : (Math.sign(slope[i - 1]) + Math.sign(slope[i])) *
          Math.min(Math.abs(slope[i - 1]), Math.abs(slope[i]), Math.abs(p) / 2);
  }
  m[0] = n > 2 ? (3 * slope[0] - m[1]) / 2 : slope[0];
  m[n - 1] = n > 2 ? (3 * slope[n - 2] - m[n - 2]) / 2 : slope[0];

  for (let i = 0; i < n - 1; i++) {
    const [xa, ya] = points[i];
    const [xb, yb] = points[i + 1];
    const dx = (xb - xa) / 3;
    d += `C${f(xa + dx)} ${f(ya + dx * m[i])} ${f(xb - dx)} ${f(yb - dx * m[i + 1])} ${f(xb)} ${f(yb)}`;
  }
  return d;
}
