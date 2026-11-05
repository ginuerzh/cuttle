import type { KeyboardEvent, ReactNode } from 'react';
import { Card, Space, Typography } from 'antd';

/** Single-column list item used on every viewport. */
export function ListCard({
  title,
  subtitle,
  tags,
  actions,
  onClick,
  dense = false,
}: {
  title: ReactNode;
  subtitle?: ReactNode;
  tags?: ReactNode;
  actions?: ReactNode;
  onClick?: () => void;
  dense?: boolean;
}) {
  return (
    <Card
      size="small"
      hoverable={!!onClick}
      style={{ marginBottom: dense ? 6 : 8, cursor: onClick ? 'pointer' : undefined }}
      styles={{ body: { padding: dense ? 8 : 12 } }}
      tabIndex={onClick ? 0 : undefined}
      role={onClick ? 'button' : undefined}
      onClick={onClick}
      onKeyDown={
        onClick
          ? (e: KeyboardEvent<HTMLDivElement>) => {
              if (e.key !== 'Enter' && e.key !== ' ') return;
              e.preventDefault();
              onClick();
            }
          : undefined
      }
    >
      <div
        style={{
          display: 'grid',
          gridTemplateColumns: 'minmax(0, 1fr) auto',
          columnGap: 8,
          alignItems: 'center',
        }}
      >
        <div style={{ minWidth: 0 }}>
          <Typography.Text strong style={{ wordBreak: 'break-all' }}>
            {title}
          </Typography.Text>
          {subtitle ? (
            <div style={{ color: 'var(--gw-text-muted)', fontSize: dense ? 11 : 12, marginTop: 2, wordBreak: 'break-all' }}>{subtitle}</div>
          ) : null}
        </div>
        {tags ? (
          <div style={{ gridColumn: 2, gridRow: actions ? '1 / span 2' : undefined, alignSelf: 'center' }}>{tags}</div>
        ) : null}
        {actions ? (
          <div style={{ gridColumn: 1, marginTop: 8 }}>
            <Space wrap size={4}>
              {actions}
            </Space>
          </div>
        ) : null}
      </div>
    </Card>
  );
}
