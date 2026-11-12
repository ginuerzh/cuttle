import { describe, expect, it } from 'vitest';
import { currencySymbol, formatCount } from './format';

describe('formatCount', () => {
  it('formats counts with one K/M/B unit scale', () => {
    expect(formatCount(999)).toBe('999');
    expect(formatCount(1_000)).toBe('1K');
    expect(formatCount(1_500)).toBe('1.5K');
    expect(formatCount(384_000)).toBe('384K');
    expect(formatCount(2_500_000)).toBe('2.5M');
    expect(formatCount(3_200_000_000)).toBe('3.2B');
  });

  it('promotes a value rounded up to the next unit', () => {
    expect(formatCount(999_500)).toBe('1M');
    expect(formatCount(999_999_999)).toBe('1B');
  });

  it('handles missing values', () => {
    expect(formatCount(undefined)).toBe('—');
    expect(formatCount(null)).toBe('—');
    expect(formatCount(Number.NaN)).toBe('—');
  });
});

describe('currencySymbol', () => {
  it('maps the currencies balances use to their sign', () => {
    expect(currencySymbol('USD')).toBe('$');
    expect(currencySymbol('cny')).toBe('¥');
    expect(currencySymbol('EUR')).toBe('€');
    expect(currencySymbol('GBP')).toBe('£');
  });

  it('passes an unknown currency through and keeps an absent one empty', () => {
    expect(currencySymbol('USDT')).toBe('USDT');
    expect(currencySymbol()).toBe('');
    expect(currencySymbol('')).toBe('');
  });
});
