import { beforeEach, describe, expect, it } from 'vitest';
import { deadTitle, hasFailureData } from './deadTitle';
import { setActiveLang } from '../i18n';
import type { NodeMetrics } from '../api/types';

const metrics: NodeMetrics = {
  requests: 12,
  errors: 3,
  attempts: 0,
  retriedRequests: 0,
  p50Ms: 3,
  p95Ms: 5,
  inputTokens: 0,
  outputTokens: 0,
  cachedTokens: 0,
  outputTokensPerSecond: 0,
  lastFailureStatus: 0,
  lastAttemptStatus: 0,
};

/** A local wall-clock instant, so the tooltip renders the same HH:MM:SS in any zone. */
const at = (h: number, m: number, s: number) => new Date(2026, 10, 11, h, m, s).getTime();

beforeEach(() => setActiveLang('zh-CN'));

describe('deadTitle', () => {
  const cases: Array<{ name: string; metrics?: NodeMetrics; want: string }> = [
    {
      name: 'lists both count sections and the newest failure',
      metrics: {
        ...metrics,
        attempts: 2,
        attemptStatus: { '429': 2 },
        failureStatus: { '503': 1, '429': 2 },
        lastFailureTs: at(3, 0, 16),
        lastFailureStatus: 429,
      },
      want: '失败 3（429×2, 503×1）· 重试丢弃 2 · 最后 03:00:16 429',
    },
    {
      name: 'keeps the discarded section and its newest attempt without a final failure',
      metrics: { ...metrics, attempts: 2, attemptStatus: { '429': 2 }, lastAttemptTs: at(3, 0, 16), lastAttemptStatus: 429 },
      want: '重试丢弃 2 · 最后 03:00:16 429',
    },
    {
      name: 'keeps the failure section without a timestamp',
      metrics: { ...metrics, failureStatus: { '429': 1 } },
      want: '失败 1（429×1）',
    },
    {
      name: 'reports whichever of the failure and the attempt is newer',
      metrics: {
        ...metrics,
        attempts: 1,
        attemptStatus: { '503': 1 },
        failureStatus: { '429': 1 },
        lastFailureTs: at(3, 0, 16),
        lastFailureStatus: 429,
        lastAttemptTs: at(3, 5, 0),
        lastAttemptStatus: 503,
      },
      want: '失败 1（429×1）· 重试丢弃 1 · 最后 03:05:00 503',
    },
    {
      name: 'says the window has no failures instead of an empty tooltip',
      metrics,
      want: '窗口内没有失败记录',
    },
    { name: 'says so without metrics', metrics: undefined, want: '窗口内没有失败记录' },
  ];

  for (const c of cases) {
    it(c.name, () => {
      expect(deadTitle(c.metrics)).toBe(c.want);
    });
  }

  it('translates every section', () => {
    setActiveLang('en-US');
    expect(
      deadTitle({
        ...metrics,
        attempts: 2,
        failureStatus: { '429': 2 },
        lastFailureTs: at(3, 0, 16),
        lastFailureStatus: 429,
      }),
    ).toBe('Failed 2 (429×2) · Discarded retries 2 · Last 03:00:16 429');
    expect(deadTitle(metrics)).toBe('No failures in the window');
  });
});

describe('hasFailureData', () => {
  it('reports whether the window explains a dead node', () => {
    expect(hasFailureData(undefined)).toBe(false);
    expect(hasFailureData(metrics)).toBe(false);
    expect(hasFailureData({ ...metrics, failureStatus: { '429': 1 } })).toBe(true);
    expect(hasFailureData({ ...metrics, attempts: 1, attemptStatus: { '429': 1 } })).toBe(true);
  });
});
