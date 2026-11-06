/** Format a date or timestamp in local time as YYYY-MM-DD HH:mm:ss. */
export function formatDateTime(value?: string | Date | null): string {
  if (value === undefined || value === null || value === '') return '—';
  const date = value instanceof Date ? value : new Date(value);
  if (Number.isNaN(date.getTime())) {
    return typeof value === 'string' ? value : '—';
  }
  const pad = (n: number) => String(n).padStart(2, '0');
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`;
}

/** Format a duration in milliseconds, switching to seconds to avoid a "kms" suffix. */
export function formatDurationMs(ms = 0): string {
  const value = Number.isFinite(ms) ? Math.max(0, ms) : 0;
  const rounded = Math.round(value);
  if (rounded < 1000) return `${rounded}ms`;
  return `${(rounded / 1000).toFixed(1).replace(/\.0$/, '')}s`;
}

/** Format a token rate as a compact one-decimal value; empty when unavailable. */
export function formatTokenRate(rate: number): string {
  if (!Number.isFinite(rate) || rate <= 0) return '';
  return `${rate.toFixed(1).replace(/\.0$/, '')} tok/s`;
}
