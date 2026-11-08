// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  ANIMATION_STORAGE_KEY,
  loadAnimationEnabled,
  normalizeAnimationEnabled,
  saveAnimationEnabled,
} from './animation';

beforeEach(() => {
  window.localStorage.clear();
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe('normalizeAnimationEnabled', () => {
  it('defaults to on and only treats the literal 0 as off', () => {
    expect(normalizeAnimationEnabled('0')).toBe(false);
    expect(normalizeAnimationEnabled('1')).toBe(true);
    expect(normalizeAnimationEnabled(null)).toBe(true);
    expect(normalizeAnimationEnabled(undefined)).toBe(true);
    expect(normalizeAnimationEnabled('no')).toBe(true);
  });
});

describe('animation storage', () => {
  it('round-trips the choice under the documented key', () => {
    expect(loadAnimationEnabled()).toBe(true);
    saveAnimationEnabled(false);
    expect(window.localStorage.getItem(ANIMATION_STORAGE_KEY)).toBe('0');
    expect(loadAnimationEnabled()).toBe(false);
    saveAnimationEnabled(true);
    expect(window.localStorage.getItem(ANIMATION_STORAGE_KEY)).toBe('1');
    expect(loadAnimationEnabled()).toBe(true);
  });

  it('defaults on and does not throw when storage is blocked', () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('blocked');
    });
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('blocked');
    });
    expect(loadAnimationEnabled()).toBe(true);
    expect(() => saveAnimationEnabled(false)).not.toThrow();
  });
});
