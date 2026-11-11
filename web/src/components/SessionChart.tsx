import { Typography } from 'antd';
import type { SeriesPoint } from '../api/types';
import { buildBars } from '../charts/sessionBars';
import { translate as t } from '../i18n';
import { formatClock } from '../time';

const HEIGHT = 48;
const GAP = 1;

/**
 * Sessions and errors over time. The payload is sparse, so `buildBars` pads it
 * to one bar per bucket: an empty bucket draws at zero height instead of
 * shifting the rest of the chart left.
 */
export function SessionChart({
  points,
  from,
  to,
  bucket,
  onSelect,
  selected,
  failed,
}: {
  points: SeriesPoint[];
  from: number;
  to: number;
  bucket: number;
  onSelect: (bucket?: number) => void;
  selected?: number;
  failed?: boolean;
}) {
  if (failed) {
    return <Typography.Text type="secondary">{t('趋势加载失败')}</Typography.Text>;
  }
  if (points.length === 0) {
    return <Typography.Text type="secondary">{t('窗口内没有会话')}</Typography.Text>;
  }

  const bars = buildBars(points, from, to, bucket);
  const max = Math.max(1, ...bars.map((bar) => bar.requests));
  const width = bars.length * 12;

  return (
    <svg
      role="img"
      aria-label={t('会话趋势')}
      viewBox={`0 0 ${width} ${HEIGHT}`}
      preserveAspectRatio="none"
      style={{ width: '100%', height: HEIGHT, display: 'block' }}
    >
      {bars.map((bar, index) => {
        const x = index * 12;
        const total = (bar.requests / max) * HEIGHT;
        const errors = (Math.min(bar.errors, bar.requests) / max) * HEIGHT;
        const isSelected = selected === bar.bucket;
        return (
          <g key={bar.bucket}>
            <rect
              data-bar={index}
              data-series="requests"
              x={x + GAP}
              y={HEIGHT - total}
              width={12 - GAP * 2}
              height={total}
              fill={isSelected ? '#0ea5e9' : '#94a3b8'}
              style={{ cursor: 'pointer' }}
              onClick={() => onSelect(isSelected ? undefined : bar.bucket)}
            >
              <title>
                {`${formatClock(bar.bucket)} · ${t('{requests} 会话 / {errors} 错误', {
                  requests: bar.requests,
                  errors: bar.errors,
                })}`}
              </title>
            </rect>
            {errors > 0 ? (
              <rect
                data-series="errors"
                x={x + GAP}
                y={HEIGHT - total}
                width={12 - GAP * 2}
                height={errors}
                fill="#dc2626"
                style={{ pointerEvents: 'none' }}
              />
            ) : null}
          </g>
        );
      })}
    </svg>
  );
}
