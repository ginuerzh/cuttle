import { Typography } from 'antd';
import type { BalanceTrend } from '../api/types';
import { monotonePath, trendGeometry, trendMax, trendPoints } from '../charts/balanceLine';
import { currencySymbol } from '../format';
import { translate as t } from '../i18n';
import { formatDateTime } from '../time';

const HEIGHT = 72;
const PAD = 4;
const WIDTH = 600;
const PALETTE = ['#0ea5e9', '#16a34a', '#d97706', '#7c3aed', '#dc2626', '#0891b2'];

/** One line of a provider card's balance chart. */
export interface ChartLine {
  name: string;
  trend: BalanceTrend;
}

function legend(line: ChartLine): string {
  return [line.name, currencySymbol(line.trend.currency)].filter(Boolean).join(' · ');
}

/**
 * A provider card's balance history: one smooth line per key and currency, all
 * on one scale so the lines can be compared. Readings that cannot make a line
 * (fewer than two) leave a muted hint instead of inventing history.
 */
export function BalanceChart({
  lines,
  from,
  to,
}: {
  lines: ChartLine[];
  from: string;
  to: string;
}) {
  const fromMs = Date.parse(from);
  const toMs = Date.parse(to);
  const drawable = lines
    .map((line) => ({ line, points: trendPoints(line.trend.points) }))
    .filter((entry) => entry.points.length >= 2);
  if (!Number.isFinite(fromMs) || !Number.isFinite(toMs) || drawable.length === 0) {
    return <Typography.Text type="secondary">{t('余额历史不足，采样中')}</Typography.Text>;
  }
  const maxY = trendMax(
    drawable.map((entry) => entry.line.trend),
    fromMs,
    toMs,
  );

  return (
    <div>
      <svg
        role="img"
        aria-label={t('余额曲线')}
        viewBox={`0 0 ${WIDTH} ${HEIGHT}`}
        preserveAspectRatio="none"
        style={{ width: '100%', height: HEIGHT, display: 'block' }}
      >
        {drawable.map((entry, i) => {
          const color = PALETTE[i % PALETTE.length];
          const points = trendGeometry(
            entry.points,
            fromMs,
            toMs,
            maxY,
            WIDTH - PAD * 2,
            HEIGHT - PAD * 2,
          ).map(([x, y]) => [x + PAD, y + PAD] as [number, number]);
          const line = monotonePath(points);
          const floor = (HEIGHT - PAD).toFixed(1);
          const area = `${line}L${points[points.length - 1][0].toFixed(1)} ${floor}L${points[0][0].toFixed(1)} ${floor}Z`;
          const currency = entry.line.trend.currency;
          return (
            <g key={`${entry.line.name}-${currency}`} data-trend={i}>
              <path
                data-series="balance-area"
                d={area}
                fill={color}
                fillOpacity={0.12}
                stroke="none"
              />
              <path
                data-series="balance"
                d={line}
                fill="none"
                stroke={color}
                strokeWidth={1.5}
                vectorEffect="non-scaling-stroke"
              />
              {points.map(([x, y], j) => (
                <circle key={j} data-point="" cx={x} cy={y} r={7} fill="transparent">
                  <title>{`${formatDateTime(new Date(entry.points[j].at))} · ${entry.points[j].amount.toFixed(2)} ${currency}`.trim()}</title>
                </circle>
              ))}
            </g>
          );
        })}
      </svg>
      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 12, marginTop: 4 }}>
        {drawable.map((entry, i) => (
          <span
            key={`${entry.line.name}-${entry.line.trend.currency}`}
            style={{
              display: 'inline-flex',
              alignItems: 'center',
              gap: 6,
              fontSize: 12,
              color: 'var(--gw-text-secondary, #6b7280)',
            }}
          >
            <i
              style={{
                width: 8,
                height: 8,
                borderRadius: 4,
                background: PALETTE[i % PALETTE.length],
                display: 'inline-block',
              }}
            />
            {legend(entry.line)}
          </span>
        ))}
      </div>
    </div>
  );
}
