import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import { setActiveLang } from '../i18n';
import { AccountRow, formatCount, windowLabel } from './Usage';

setActiveLang('zh-CN');

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

describe('OpenRouter usage labels', () => {
  it('localizes daily and key-limit windows', () => {
    expect(windowLabel('daily')).toBe('每日');
    expect(windowLabel('key limit')).toBe('密钥限额');
  });

  it('renders the unlimited-key note', () => {
    const html = renderToStaticMarkup(
      createElement(AccountRow, {
        account: { keyPreview: 'sk-or-...', note: 'unlimited key · all-time usage $25.50' },
        first: true,
      }),
    );
    expect(html).toContain('$25.50');
  });
});
