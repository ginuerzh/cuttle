import type { AuthFailureSnapshot } from './api/types';
import { translate as t } from './i18n';

/** One entry in the Overview notification bell. */
export interface AppNotification {
  id: string;
  kind: string;
  ts: string;
  title: string;
  detail: string;
}

const AUTH_FAILURE_REASONS: Record<string, string> = {
  missing: '未提供密钥',
  disabled: '密钥已禁用',
  invalid: '密钥无效',
};

/**
 * Adapt the auth-failure event payload into generic notifications, newest
 * first. Future sources append their own adapters to the same shape.
 */
export function authFailureNotifications(
  snapshot: AuthFailureSnapshot | null | undefined,
): AppNotification[] {
  const recent = snapshot?.recent ?? [];
  return recent
    .slice()
    .reverse()
    .map((failure, i) => ({
      id: `${failure.ts}-${i}`,
      kind: 'auth-failure',
      ts: failure.ts,
      title: t('网关密钥鉴权失败'),
      detail: [
        t(AUTH_FAILURE_REASONS[failure.reason] ?? failure.reason),
        failure.clientIp,
        failure.method ? `${failure.method} ${failure.path ?? ''}`.trim() : failure.path,
      ]
        .filter(Boolean)
        .join(' · '),
    }));
}

/**
 * Reports whether two snapshots carry the same retained entries. The server
 * sends a fresh object every tick; this keeps the bell from re-rendering when
 * nothing changed (important while the edit view is open).
 */
export function sameAuthFailures(
  a: AuthFailureSnapshot | null,
  b: AuthFailureSnapshot | null,
): boolean {
  if (a === b) return true;
  if (!a || !b) return false;
  if (a.truncated !== b.truncated || a.recent.length !== b.recent.length) return false;
  if (a.recent.length === 0) return true;
  const last = a.recent.length - 1;
  return a.recent[0].ts === b.recent[0].ts && a.recent[last].ts === b.recent[last].ts;
}
