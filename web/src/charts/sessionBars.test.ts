import { describe, expect, it } from 'vitest';
import { buildBars, bucketFor } from './sessionBars';

describe('bucketFor', () => {
  it('uses one-minute buckets for the 15 minute preset', () => {
    expect(bucketFor(15 * 60_000)).toBe(60_000);
  });

  it('uses two-minute buckets for the one hour preset', () => {
    expect(bucketFor(60 * 60_000)).toBe(120_000);
  });

  it('uses one-hour buckets for the 24 hour preset', () => {
    expect(bucketFor(24 * 60 * 60_000)).toBe(3_600_000);
  });

  it('keeps a generic range at 30 whole-minute buckets inside the bounds', () => {
    // 90 minutes / 30 = 3 minutes per bucket.
    expect(bucketFor(90 * 60_000)).toBe(180_000);
    // A tiny range never goes below one minute.
    expect(bucketFor(60_000)).toBe(60_000);
    // A wide range never goes above one hour.
    expect(bucketFor(72 * 60 * 60_000)).toBe(3_600_000);
  });
});

describe('buildBars', () => {
  const from = Date.UTC(2026, 10, 11, 10, 0, 0);
  const to = from + 6 * 60_000;
  const bucket = 60_000;

  it('pads empty buckets with zeros in the right position', () => {
    const bars = buildBars(
      [
        { bucket: new Date(from).toISOString(), requests: 2, errors: 1 },
        { bucket: new Date(from + 3 * bucket).toISOString(), requests: 5, errors: 0 },
      ],
      from,
      to,
      bucket,
    );

    expect(bars).toHaveLength(6);
    expect(bars[0]).toEqual({ bucket: from, requests: 2, errors: 1 });
    expect(bars[1]).toEqual({ bucket: from + bucket, requests: 0, errors: 0 });
    expect(bars[3]).toEqual({ bucket: from + 3 * bucket, requests: 5, errors: 0 });
    expect(bars[5].requests).toBe(0);
  });

  it('clips points outside the window', () => {
    const bars = buildBars(
      [
        { bucket: new Date(from - bucket).toISOString(), requests: 9, errors: 0 },
        { bucket: new Date(from + 10 * bucket).toISOString(), requests: 9, errors: 0 },
        { bucket: new Date(from).toISOString(), requests: 1, errors: 0 },
      ],
      from,
      to,
      bucket,
    );

    expect(bars.reduce((sum, bar) => sum + bar.requests, 0)).toBe(1);
  });

  it('keeps a single bar for a window narrower than one bucket', () => {
    const bars = buildBars([{ bucket: new Date(from).toISOString(), requests: 3, errors: 1 }], from, from + 1, bucket);

    expect(bars).toHaveLength(1);
    expect(bars[0].requests).toBe(3);
  });
});
