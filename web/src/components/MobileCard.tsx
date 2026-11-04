import type { ReactNode } from 'react';
import { Card, Space, Typography } from 'antd';

/** Compact list item used instead of a table row on narrow screens. */
export function MobileCard({
  title,
  subtitle,
  tags,
  actions,
  onClick,
}: {
  title: ReactNode;
  subtitle?: ReactNode;
  tags?: ReactNode;
  actions?: ReactNode;
  onClick?: () => void;
}) {
  return (
    <Card
      size="small"
      style={{ marginBottom: 8 }}
      styles={{ body: { padding: 12 } }}
      onClick={onClick}
    >
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', gap: 8 }}>
        <div style={{ minWidth: 0 }}>
          <Typography.Text strong style={{ wordBreak: 'break-all' }}>
            {title}
          </Typography.Text>
          {subtitle ? (
            <div style={{ color: 'var(--gw-text-muted)', fontSize: 12, marginTop: 2, wordBreak: 'break-all' }}>{subtitle}</div>
          ) : null}
        </div>
        {tags ? <div style={{ flex: 'none' }}>{tags}</div> : null}
      </div>
      {actions ? (
        <div style={{ marginTop: 8 }}>
          <Space wrap size={4}>
            {actions}
          </Space>
        </div>
      ) : null}
    </Card>
  );
}
