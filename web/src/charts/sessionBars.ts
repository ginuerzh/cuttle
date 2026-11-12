import type { SeriesPoint } from '../api/types';

/** One bar of the session chart: `bucket` is the bucket start in unix ms. */
export interface Bar {
  bucket: number;
  requests: number;
  errors: number;
}

const MINUTE = 60_000;
const HOUR = 60 * MINUTE;
const TARGET_BARS = 30;
const MAX_TICKS = 6;

/**
 * The bucket width for a window: about 30 bars, rounded to whole minutes and
 * clamped to [1m, 1h] so short windows stay readable and long ones stay cheap.
 */
export function bucketFor(rangeMs: number): number {
  // A day or more reads best in whole hours (24 bars for the 24h preset).
  if (rangeMs >= 12 * HOUR) return HOUR;
  const raw = rangeMs > 0 ? rangeMs / TARGET_BARS : MINUTE;
  const minutes = Math.max(1, Math.round(raw / MINUTE));
  return Math.min(HOUR, minutes * MINUTE);
}

/**
 * Pads the sparse payload to exactly one bar per bucket of [from, to), so an
 * empty bucket renders as zero height instead of shifting the bars left. Points
 * outside the window are dropped.
 */
export function buildBars(points: SeriesPoint[], from: number, to: number, bucket: number): Bar[] {
  const width = bucket > 0 ? bucket : MINUTE;
  const count = Math.max(1, Math.ceil((to - from) / width));
  const bars: Bar[] = [];
  for (let i = 0; i < count; i++) {
    bars.push({ bucket: from + i * width, requests: 0, errors: 0 });
  }
  for (const point of points) {
    const ts = Date.parse(point.bucket);
    if (Number.isNaN(ts)) continue;
    const index = Math.floor((ts - from) / width);
    if (index < 0 || index >= count) continue;
    bars[index].requests += point.requests;
    bars[index].errors += point.errors;
  }
  return bars;
}

/**
 * The bars that carry an x-axis label: at most `maxTicks` of them, spread evenly
 * and always including the first and the last bar so both window edges read.
 * Labelling every bar of a 30 bar window would overlap.
 */
export function axisTicks(count: number, maxTicks = MAX_TICKS): number[] {
  if (count <= 0) return [];
  if (count <= maxTicks) return Array.from({ length: count }, (_, i) => i);
  const step = (count - 1) / (maxTicks - 1);
  return Array.from({ length: maxTicks }, (_, i) => Math.round(i * step));
}
