import { renderToStaticMarkup } from 'react-dom/server';
import { beforeEach, describe, expect, it } from 'vitest';
import { setActiveLang } from '../i18n';
import { GatewayKeyCard, GatewaySecretContent, gatewayKeyState } from './GatewayKeys';

beforeEach(() => setActiveLang('zh-CN'));

describe('gatewayKeyState', () => {
  it('classifies the empty and all-disabled states', () => {
    expect(gatewayKeyState([])).toBe('none');
    expect(gatewayKeyState([{ enabled: false }])).toBe('all-disabled');
    expect(gatewayKeyState([{ enabled: true }])).toBe('ok');
    expect(gatewayKeyState([{ enabled: false }, { enabled: true }])).toBe('ok');
  });
});

describe('GatewayKeyCard', () => {
  it('renders a key card with mask and usage', () => {
    const html = renderToStaticMarkup(
      <GatewayKeyCard
        item={{ name: 'claude-code', keyPreview: 'sk-...abcd', enabled: true }}
        usage={{
          name: 'claude-code',
          configured: true,
          enabled: true,
          keyPreview: 'sk-...abcd',
          requests: 12,
          errors: 1,
          inputTokens: 1000,
          outputTokens: 2000,
          cachedTokens: 500,
        }}
      />,
    );

    expect(html).toContain('claude-code');
    expect(html).toContain('sk-...abcd');
    expect(html).toContain('会话 12');
    expect(html).toContain('错误 1');
    expect(html).toContain('输入 1000');
    expect(html).toContain('输出 2000');
    expect(html).toContain('缓存 500');
    expect(html).toContain('aria-label="启用 claude-code"');
  });
});

describe('GatewaySecretContent', () => {
  it('shows the one-time secret and warning', () => {
    const html = renderToStaticMarkup(
      <GatewaySecretContent secret={{ name: 'k', key: 'sk-gw-raw' }} />,
    );

    expect(html).toContain('sk-gw-raw');
    expect(html).toContain('该密钥只显示一次，请立即复制保存。');
  });
});
