/**
 * One compact unit scale for every count and token amount in the console, so
 * the same number never appears as "1.5k" on one page and "1500" on another.
 */
export function formatCount(n?: number | null): string {
  if (n === undefined || n === null || !Number.isFinite(n)) return '—';
  const sign = n < 0 ? '-' : '';
  const abs = Math.abs(n);
  const units: Array<[number, string]> = [
    [1_000_000_000, 'B'],
    [1_000_000, 'M'],
    [1_000, 'K'],
  ];
  for (let i = 0; i < units.length; i++) {
    const [value, suffix] = units[i];
    if (abs < value) continue;
    // Rounding 999,950 to "1000.0K" would look wrong; promote it to "1M".
    if (Math.round(abs / value) >= 1_000 && i > 0) {
      const [nextValue, nextSuffix] = units[i - 1];
      return `${sign}${scale(abs / nextValue)}${nextSuffix}`;
    }
    return `${sign}${scale(abs / value)}${suffix}`;
  }
  return `${n}`;
}

/** One decimal at most, without a trailing ".0". */
function scale(value: number): string {
  const rounded = Math.round(value * 10) / 10;
  return Number.isInteger(rounded) ? String(rounded) : rounded.toFixed(1);
}
