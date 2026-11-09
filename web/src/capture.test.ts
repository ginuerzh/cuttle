import { describe, expect, it } from 'vitest';
import { isTruncated } from './capture';

describe('isTruncated', () => {
  it('returns false without a capture', () => {
    expect(isTruncated(undefined, 'requestOriginal')).toBe(false);
  });

  it('returns true only for listed fields', () => {
    const cap = { truncatedFields: ['requestOriginal'] };
    expect(isTruncated(cap, 'requestOriginal')).toBe(true);
    expect(isTruncated(cap, 'requestRewritten')).toBe(false);
  });

  it('returns false when the list is absent', () => {
    expect(isTruncated({}, 'requestOriginal')).toBe(false);
  });
});
