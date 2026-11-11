/** Format a date or timestamp in local time as YYYY-MM-DD HH:mm:ss. */
export function formatDateTime(value?: string | Date | null): string {
  if (value === undefined || value === null || value === '') return '—';
  const date = value instanceof Date ? value : new Date(value);
  if (Number.isNaN(date.getTime())) {
    return typeof value === 'string' ? value : '—';
  }
  return `${date.getFullYear()}-${pad2(date.getMonth() + 1)}-${pad2(date.getDate())} ${formatClock(date)}`;
}

/** Format a timestamp as local HH:mm:ss, for a label that already names the day. */
export function formatClock(value?: string | Date | number | null): string {
  if (value === undefined || value === null || value === '') return '—';
  const date = value instanceof Date ? value : new Date(value);
  if (Number.isNaN(date.getTime())) return '—';
  return `${pad2(date.getHours())}:${pad2(date.getMinutes())}:${pad2(date.getSeconds())}`;
}

function pad2(n: number): string {
  return String(n).padStart(2, '0');
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
  const value = rate.toFixed(1);
  if (Number(value) === 0) return '';
  return `${value.replace(/\.0$/, '')} tok/s`;
}
