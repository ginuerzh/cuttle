import { Alert } from 'antd';
import type { AuthFailure, AuthFailureSnapshot } from '../api/types';
import { translate as t } from '../i18n';
import { formatDateTime } from '../time';

const REASON_LABELS: Record<string, string> = {
  missing: '未提供密钥',
  disabled: '密钥已禁用',
  invalid: '密钥无效',
};

const MAX_SHOWN = 5;

/** Failures inside the monitor window, oldest first. */
export function recentAuthFailures(
  snapshot: AuthFailureSnapshot | null | undefined,
  since?: string,
): AuthFailure[] {
  const recent = snapshot?.recent ?? [];
  if (!since) return recent;
  const floor = Date.parse(since);
  if (Number.isNaN(floor)) return recent;
  return recent.filter((failure) => {
    const ts = Date.parse(failure.ts);
    return Number.isNaN(ts) || ts >= floor;
  });
}

interface Props {
  failures?: AuthFailureSnapshot | null;
  since?: string;
}

/**
 * Read-only warning panel for rejected gateway-key attempts. It reports the
 * client-selected level of detail only: no credential or request body.
 */
export function AuthFailureAlert({ failures, since }: Props) {
  const recent = recentAuthFailures(failures, since);
  if (recent.length === 0) return null;

  const shown = recent.slice(-MAX_SHOWN).reverse();
  const count = failures?.truncated ? `≥${recent.length}` : String(recent.length);
  return (
    <Alert
      type="warning"
      showIcon
      style={{ maxWidth: 460 }}
      message={t('最近 {count} 次网关密钥鉴权失败', { count })}
      description={
        <ul style={{ margin: 0, paddingInlineStart: 18, fontSize: 12 }}>
          {shown.map((failure, i) => (
            <li key={`${failure.ts}-${i}`}>
              {formatDateTime(failure.ts)} · {t(REASON_LABELS[failure.reason] ?? failure.reason)} ·{' '}
              {failure.clientIp || '—'} · {authFailureTarget(failure)}
            </li>
          ))}
        </ul>
      }
    />
  );
}

function authFailureTarget(failure: AuthFailure): string {
  const path = failure.path || '—';
  return failure.method ? `${failure.method} ${path}` : path;
}
