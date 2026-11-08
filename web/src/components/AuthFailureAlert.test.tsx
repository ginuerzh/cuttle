import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import type { AuthFailure, AuthFailureSnapshot } from '../api/types';
import { setActiveLang } from '../i18n';
import { formatDateTime } from '../time';
import { AuthFailureAlert, recentAuthFailures } from './AuthFailureAlert';

setActiveLang('zh-CN');

function failure(overrides: Partial<AuthFailure> = {}): AuthFailure {
  return {
    ts: '2026-11-08T12:00:00Z',
    reason: 'invalid',
    method: 'POST',
    path: '/v1/messages',
    clientIp: '192.0.2.7',
    userAgent: 'claude-code',
    ...overrides,
  };
}

function snapshot(recent: AuthFailure[], truncated = false): AuthFailureSnapshot {
  return { recent, truncated };
}

describe('recentAuthFailures', () => {
  it('keeps failures at or after the window start', () => {
    const recent = recentAuthFailures(
      snapshot([
        failure({ ts: '2026-11-08T11:59:00Z', reason: 'missing' }),
        failure({ ts: '2026-11-08T12:00:00Z', reason: 'invalid' }),
      ]),
      '2026-11-08T12:00:00Z',
    );

    expect(recent.map((f) => f.reason)).toEqual(['invalid']);
  });

  it('returns every entry when the window is absent or unparseable', () => {
    const entries = [failure(), failure({ ts: '2026-11-08T11:00:00Z' })];
    expect(recentAuthFailures(snapshot(entries), undefined)).toHaveLength(2);
    expect(recentAuthFailures(snapshot(entries), 'not-a-time')).toHaveLength(2);
    expect(recentAuthFailures(null, '2026-11-08T12:00:00Z')).toHaveLength(0);
  });
});

describe('AuthFailureAlert', () => {
  it('renders nothing when there is nothing to report', () => {
    expect(renderToStaticMarkup(<AuthFailureAlert failures={null} />)).toBe('');
    expect(renderToStaticMarkup(<AuthFailureAlert failures={snapshot([])} />)).toBe('');
    expect(
      renderToStaticMarkup(
        <AuthFailureAlert
          failures={snapshot([failure({ ts: '2026-11-08T11:00:00Z' })])}
          since="2026-11-08T12:00:00Z"
        />,
      ),
    ).toBe('');
  });

  it('shows the count, reason, client and path of recent failures', () => {
    const html = renderToStaticMarkup(
      <AuthFailureAlert failures={snapshot([failure()])} since="2026-11-08T11:00:00Z" />,
    );

    expect(html).toContain('最近 1 次网关密钥鉴权失败');
    expect(html).toContain('密钥无效');
    expect(html).toContain('192.0.2.7');
    expect(html).toContain('/v1/messages');
    expect(html).toContain(formatDateTime('2026-11-08T12:00:00Z'));
  });

  it('marks a truncated record with at least', () => {
    const html = renderToStaticMarkup(
      <AuthFailureAlert
        failures={snapshot([failure(), failure({ reason: 'missing' })], true)}
        since="2026-11-08T11:00:00Z"
      />,
    );

    expect(html).toContain('≥2');
  });
});
