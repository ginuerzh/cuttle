import { describe, expect, it } from 'vitest';
import { normalizeThemeMode, resolveTheme } from './theme';

describe('normalizeThemeMode', () => {
  it('accepts the three valid modes', () => {
    expect(normalizeThemeMode('system')).toBe('system');
    expect(normalizeThemeMode('light')).toBe('light');
    expect(normalizeThemeMode('dark')).toBe('dark');
  });

  it('falls back to system for null, empty, invalid or padded values', () => {
    expect(normalizeThemeMode(null)).toBe('system');
    expect(normalizeThemeMode('')).toBe('system');
    expect(normalizeThemeMode('sepia')).toBe('system');
    expect(normalizeThemeMode(' dark ')).toBe('system');
  });
});

describe('resolveTheme', () => {
  it('honours explicit light and dark modes', () => {
    expect(resolveTheme('light', true)).toBe('light');
    expect(resolveTheme('light', false)).toBe('light');
    expect(resolveTheme('dark', true)).toBe('dark');
    expect(resolveTheme('dark', false)).toBe('dark');
  });

  it('uses the system preference for system mode', () => {
    expect(resolveTheme('system', true)).toBe('dark');
    expect(resolveTheme('system', false)).toBe('light');
  });
});
