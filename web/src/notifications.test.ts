import { describe, expect, it } from 'vitest';
import type { AuthFailure, AuthFailureSnapshot } from './api/types';
import { setActiveLang } from './i18n';
import { authFailureNotifications, sameAuthFailures } from './notifications';

setActiveLang('zh-CN');

function failure(overrides: Partial<AuthFailure> = {}): AuthFailure {
  return {
    ts: '2026-11-08T12:00:00Z',
    reason: 'invalid',
    method: 'POST',
    path: '/v1/messages',
    clientIp: '192.0.2.7',
    ...overrides,
  };
}

function snapshot(recent: AuthFailure[]): AuthFailureSnapshot {
  return { recent };
}

describe('authFailureNotifications', () => {
  it('returns nothing for a missing or empty snapshot', () => {
    expect(authFailureNotifications(null)).toEqual([]);
    expect(authFailureNotifications({ recent: [] })).toEqual([]);
  });

  it('maps failures to generic notifications newest first', () => {
    const items = authFailureNotifications(
      snapshot([
        failure({ ts: '2026-11-08T11:59:00Z', reason: 'missing', clientIp: '203.0.113.9' }),
        failure({ ts: '2026-11-08T12:00:00Z', reason: 'invalid', clientIp: '192.0.2.7' }),
      ]),
    );

    expect(items).toHaveLength(2);
    expect(items[0].id).toBe('2026-11-08T12:00:00Z-0');
    expect(items[0].kind).toBe('auth-failure');
    expect(items[0].title).toBe('网关密钥鉴权失败');
    expect(items[0].detail).toContain('密钥无效');
    expect(items[0].detail).toContain('192.0.2.7');
    expect(items[0].detail).toContain('POST /v1/messages');
    expect(items[1].detail).toContain('未提供密钥');
    expect(items[1].detail).toContain('203.0.113.9');
  });

  it('keeps an unknown reason and a missing method readable', () => {
    const items = authFailureNotifications(
      snapshot([failure({ reason: 'rate-limited', method: undefined, path: undefined })]),
    );
    expect(items[0].detail).toContain('rate-limited');
    expect(items[0].detail).not.toContain('undefined');
  });
});

describe('sameAuthFailures', () => {
  it('treats identical retained entries as unchanged', () => {
    const entries = [failure(), failure({ ts: '2026-11-08T12:01:00Z' })];
    expect(sameAuthFailures(snapshot(entries), snapshot([...entries]))).toBe(true);
    expect(sameAuthFailures(null, snapshot([]))).toBe(false);
    expect(sameAuthFailures(snapshot([]), snapshot([]))).toBe(true);
  });

  it('detects an append, a drop and a clear', () => {
    const one = snapshot([failure()]);
    const two = snapshot([failure(), failure({ ts: '2026-11-08T12:01:00Z' })]);
    expect(sameAuthFailures(one, two)).toBe(false);
    expect(sameAuthFailures(two, one)).toBe(false);
    expect(sameAuthFailures(one, snapshot([]))).toBe(false);
    expect(sameAuthFailures({ recent: [failure()], truncated: true }, one)).toBe(false);
  });
});
