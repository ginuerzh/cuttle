import type { RequestRow } from './api/types';
import { translate as t } from './i18n';

export function statusLabel(r: RequestRow): string {
  if (r.status === 0) {
    return r.errorClass === 'gateway_timeout' ? t('502 网关超时') : t('502 上游失败');
  }
  return String(r.status);
}

const ERROR_CLASS_LABELS: Record<string, string> = {
  upstream_auth: '上游认证失败',
  upstream_forbidden: '上游拒绝',
  upstream_not_found: '上游 404',
  upstream_quota: '上游限流/额度',
  upstream_bad_request: '上游请求错误',
  upstream_unavailable: '上游不可用',
  upstream_truncated: '上游流截断',
  upstream_error: '上游错误',
  gateway_timeout: '网关超时',
  network: '网络错误',
  rewrite_error: '改写错误',
  client_error: '客户端错误',
  client_disconnect: '客户端断开',
};

/** Translate a raw error class key, falling back to the key itself. */
export function errorClassText(errorClass?: string): string {
  if (!errorClass) return '';
  return t(ERROR_CLASS_LABELS[errorClass] || errorClass);
}

export function errorClassLabel(r: RequestRow): string {
  return errorClassText(r.errorClass);
}

export function gatewayKeyLabel(row: RequestRow): string {
  return row.gatewayKey ? `key:${row.gatewayKey}` : '';
}

export function retryLabel(row: RequestRow): string {
  const count = row.retries ?? 0;
  if (count <= 0) return '';
  return t('重试 {count} 次', { count });
}
