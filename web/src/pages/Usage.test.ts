import { describe, expect, it } from 'vitest';
import { formatCount } from './Usage';

describe('formatCount', () => {
  it('formats counts with k, M, and B units', () => {
    expect(formatCount(999)).toBe('999');
    expect(formatCount(1_500)).toBe('1.5k');
    expect(formatCount(2_500_000)).toBe('2.5M');
    expect(formatCount(3_200_000_000)).toBe('3.2B');
  });

  it('handles missing values', () => {
    expect(formatCount(undefined)).toBe('—');
    expect(formatCount(null as unknown as number)).toBe('—');
  });
});
