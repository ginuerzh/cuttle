import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import { setActiveLang } from '../i18n';
import { AccountRow, windowLabel } from './Usage';

setActiveLang('zh-CN');

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

  it('shows the key name beside the masked preview', () => {
    const html = renderToStaticMarkup(
      createElement(AccountRow, {
        account: { name: 'account-a', keyPreview: 'sk-...a', enabled: true },
        first: true,
      }),
    );
    expect(html).toContain('account-a');
    expect(html).toContain('sk-...a');
  });
});
