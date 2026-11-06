import { describe, expect, it } from 'vitest';
import { versionLabel, versionTitle } from './version';

describe('versionLabel', () => {
  it('appends the short commit to a release tag', () => {
    expect(versionLabel({ ok: true, version: 'v0.1.0', commit: '694deb53b2c9' })).toBe('v0.1.0 · 694deb5');
  });

  it('does not repeat the commit in an automatic build version', () => {
    expect(versionLabel({ ok: true, version: 'main-694deb5', commit: '694deb53b2c9' })).toBe('main-694deb5');
  });

  it('omits the commit when missing', () => {
    expect(versionLabel({ ok: true, version: 'dev' })).toBe('dev');
  });
});

describe('versionTitle', () => {
  it('joins the commit and build time', () => {
    expect(versionTitle({ ok: true, version: 'v0.1.0', commit: '694deb53b2c9', builtAt: '2026-11-06T10:00:00Z' }))
      .toBe('commit 694deb53b2c9 · built 2026-11-06T10:00:00Z');
  });

  it('returns undefined without metadata', () => {
    expect(versionTitle({ ok: true, version: 'dev' })).toBeUndefined();
  });
});
