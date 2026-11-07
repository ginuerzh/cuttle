import { describe, expect, it } from 'vitest';
import {
  clampVelocity,
  decayVelocity,
  dragVelocity,
  INERTIA_FRICTION,
  INERTIA_MAX_SPEED,
} from './inertia';

describe('dragVelocity', () => {
  it('uses only the samples inside the tail window', () => {
    const samples = [
      { t: 0, x: 0, y: 0 },
      { t: 50, x: 10, y: 5 },
      { t: 70, x: 30, y: 15 },
      { t: 100, x: 60, y: 30 },
    ];

    const v = dragVelocity(samples, 100, 60);
    expect(v.vx).toBeCloseTo(1);
    expect(v.vy).toBeCloseTo(0.5);
  });

  it('returns zero when the pointer rested before release', () => {
    const samples = [
      { t: 0, x: 0, y: 0 },
      { t: 100, x: 50, y: 0 },
    ];

    expect(dragVelocity(samples, 250)).toEqual({ vx: 0, vy: 0 });
  });

  it('returns zero without two samples', () => {
    expect(dragVelocity([], 0)).toEqual({ vx: 0, vy: 0 });
    expect(dragVelocity([{ t: 0, x: 1, y: 2 }], 0)).toEqual({ vx: 0, vy: 0 });
  });
});

describe('clampVelocity', () => {
  it('caps the speed but keeps the direction', () => {
    const v = clampVelocity({ vx: 6, vy: 8 });
    expect(Math.hypot(v.vx, v.vy)).toBeCloseTo(INERTIA_MAX_SPEED);
    expect(v.vx / v.vy).toBeCloseTo(6 / 8);
  });

  it('leaves slow velocities alone', () => {
    expect(clampVelocity({ vx: 0.1, vy: -0.2 })).toEqual({ vx: 0.1, vy: -0.2 });
  });
});

describe('decayVelocity', () => {
  it('applies one frame of friction per 16.67ms', () => {
    const v = decayVelocity({ vx: 1, vy: 0 }, 1000 / 60);
    expect(v.vx).toBeCloseTo(INERTIA_FRICTION);
    expect(v.vy).toBe(0);
  });

  it('decays faster for longer frames', () => {
    const short = decayVelocity({ vx: 1, vy: 0 }, 16.667);
    const long = decayVelocity({ vx: 1, vy: 0 }, 33.334);
    expect(long.vx).toBeLessThan(short.vx);
  });
});
