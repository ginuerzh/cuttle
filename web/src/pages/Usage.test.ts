import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import { setActiveLang } from '../i18n';
import { AccountRow, ProviderCard, windowLabel } from './Usage';

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

const providerWithTrends = {
  id: 'p_1',
  name: 'deepseek',
  type: 'deepseek',
  enabled: true,
  supported: true,
  accounts: [
    {
      name: 'key-1',
      keyId: 'pk_1',
      keyPreview: 'sk-...1',
      enabled: true,
      balance: [{ currency: 'USD', total: 11.12 }],
      trends: [
        {
          currency: 'USD',
          points: [
            { at: '2026-11-12T01:00:00Z', amount: 20 },
            { at: '2026-11-12T02:00:00Z', amount: 11.12 },
          ],
        },
      ],
    },
  ],
} as unknown as Parameters<typeof ProviderCard>[0]['provider'];

function providerCard(showStats: boolean) {
  return createElement(ProviderCard, {
    isMobile: false,
    showStats,
    from: '2026-11-12T00:00:00Z',
    to: '2026-11-13T00:00:00Z',
    provider: providerWithTrends,
  });
}

describe('provider balance curve', () => {
  it('draws a balance curve per account trend', () => {
    const html = renderToStaticMarkup(providerCard(true));
    expect(html).toContain('data-series="balance"');
    expect(html).toContain('key-1');
  });

  it('draws no curve while statistics are off', () => {
    const html = renderToStaticMarkup(providerCard(false));
    expect(html).not.toContain('data-series="balance"');
  });

  it('renders the curve in English without any Chinese left', () => {
    setActiveLang('en-US');
    const html = renderToStaticMarkup(providerCard(true));
    setActiveLang('zh-CN');
    expect(html).toContain('data-series="balance"');
    expect(html).not.toMatch(/[\u4e00-\u9fff]/);
  });
});
