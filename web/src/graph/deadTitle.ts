import type { NodeMetrics } from '../api/types';
import { translate } from '../i18n';
import { formatClock } from '../time';

/**
 * The dead badge tooltip, shared by the topology node and the node panel icon so
 * both explain the same node the same way: `失败 3（429×2, 503×1）· 重试丢弃 2 ·
 * 最后 03:00:16 429`. Every section is omitted when it has nothing to report, and
 * the newest failure — a discarded attempt or a final failed request, whichever
 * came last — is appended only when the window has a timestamp.
 */
export function deadTitle(metrics?: NodeMetrics): string {
  const sections: string[] = [];
  const failed = sumCounts(metrics?.failureStatus);
  if (failed > 0) {
    sections.push(
      translate('失败 {failed}（{statuses}）', {
        failed,
        statuses: formatStatusCounts(metrics?.failureStatus),
      }),
    );
  }
  const discarded = metrics?.attempts ?? 0;
  if (discarded > 0) {
    sections.push(translate('重试丢弃 {discarded}', { discarded }));
  }
  const last = newestFailure(metrics);
  if (last) {
    sections.push(translate('最后 {time} {status}', { time: formatClock(last.ts), status: String(last.status) }));
  }
  if (sections.length === 0) return translate('窗口内没有失败记录');
  return joinSections(sections);
}

/**
 * Join the sections with ` · `. A section ending in a full-width bracket already
 * carries its own spacing, so the Chinese separator is `）· ` while every other
 * boundary keeps the spaces around the dot.
 */
function joinSections(sections: string[]): string {
  return sections.join(' · ').replace(/） · /g, '）· ');
}

/** Whether the window recorded any failure or discarded attempt for the node. */
export function hasFailureData(metrics?: NodeMetrics): boolean {
  return sumCounts(metrics?.failureStatus) > 0 || (metrics?.attempts ?? 0) > 0;
}

/** `429×2, 503×1` in numeric order; an empty input gives an empty string. */
export function formatStatusCounts(counts?: Record<string, number>): string {
  return Object.entries(counts ?? {})
    .filter(([, count]) => count > 0)
    .sort((a, b) => Number(a[0]) - Number(b[0]))
    .map(([status, count]) => `${status}×${count}`)
    .join(', ');
}

function sumCounts(counts?: Record<string, number>): number {
  return Object.values(counts ?? {}).reduce((sum, count) => sum + count, 0);
}

/**
 * The newest failure of the window. A discarded attempt and a final failed
 * request are both failures, so the later one is the one to show; a zero
 * timestamp means the window has none.
 */
function newestFailure(metrics?: NodeMetrics): { ts: number; status: number } | undefined {
  const failure = metrics?.lastFailureTs
    ? { ts: metrics.lastFailureTs, status: metrics.lastFailureStatus }
    : undefined;
  const attempt = metrics?.lastAttemptTs ? { ts: metrics.lastAttemptTs, status: metrics.lastAttemptStatus } : undefined;
  if (failure && attempt) return attempt.ts > failure.ts ? attempt : failure;
  return failure ?? attempt;
}
