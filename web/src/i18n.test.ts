import { describe, expect, it } from 'vitest';
import { normalizeLang, translateFor } from './i18n';

describe('normalizeLang', () => {
  it('accepts Chinese and English variants', () => {
    expect(normalizeLang('zh-CN')).toBe('zh-CN');
    expect(normalizeLang('zh')).toBe('zh-CN');
    expect(normalizeLang('zh-TW')).toBe('zh-CN');
    expect(normalizeLang('en-US')).toBe('en-US');
    expect(normalizeLang('en')).toBe('en-US');
  });

  it('falls back to the browser language for unknown values', () => {
    const fallback = normalizeLang('fr-FR');
    expect(['zh-CN', 'en-US']).toContain(fallback);
  });
});

describe('translateFor', () => {
  it('keeps Chinese source text for zh-CN', () => {
    expect(translateFor('zh-CN', '退出')).toBe('退出');
  });

  it('translates keys and replaces placeholders for en-US', () => {
    expect(translateFor('en-US', '退出')).toBe('Log out');
    expect(translateFor('en-US', '总览')).toBe('Overview');
    expect(translateFor('en-US', '动画')).toBe('Animation');
    expect(translateFor('en-US', '删除 {name}?', { name: 'deepseek' })).toBe('Delete deepseek?');
  });

  it('falls back to the key when a translation is missing', () => {
    expect(translateFor('en-US', 'not-translated')).toBe('not-translated');
  });
});
