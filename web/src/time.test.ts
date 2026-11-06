import { describe, expect, it } from 'vitest';
import { formatDateTime, formatDurationMs, formatTokenRate } from './time';

describe('formatDateTime', () => {
  it('formats a Date in local time as YYYY-MM-DD HH:mm:ss', () => {
    expect(formatDateTime(new Date(2026, 10, 6, 9, 8, 7))).toBe('2026-11-06 09:08:07');
  });

  it('formats a timestamp string', () => {
    expect(formatDateTime('2026-11-06T09:08:07')).toBe('2026-11-06 09:08:07');
  });

  it('returns a dash for missing values', () => {
    expect(formatDateTime(undefined)).toBe('—');
    expect(formatDateTime(null)).toBe('—');
    expect(formatDateTime('')).toBe('—');
  });

  it('leaves unparseable input untouched', () => {
    expect(formatDateTime('not-a-date')).toBe('not-a-date');
  });
});

describe('formatDurationMs', () => {
  it('keeps sub-second durations in milliseconds', () => {
    expect(formatDurationMs(0)).toBe('0ms');
    expect(formatDurationMs(3)).toBe('3ms');
    expect(formatDurationMs(999)).toBe('999ms');
  });

  it('switches to seconds at 1000 ms without a k suffix', () => {
    expect(formatDurationMs(999.6)).toBe('1s');
    expect(formatDurationMs(1000)).toBe('1s');
    expect(formatDurationMs(1500)).toBe('1.5s');
    expect(formatDurationMs(85_000)).toBe('85s');
  });
});

describe('formatTokenRate', () => {
  it('shows one decimal and strips a trailing .0', () => {
    expect(formatTokenRate(42.34)).toBe('42.3 tok/s');
    expect(formatTokenRate(42)).toBe('42 tok/s');
  });

  it('returns an empty string for non-positive rates', () => {
    expect(formatTokenRate(0)).toBe('');
    expect(formatTokenRate(-1)).toBe('');
  });
});
