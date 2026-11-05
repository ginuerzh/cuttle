import type { ReactNode } from 'react';
import { Card, Space, Typography } from 'antd';

/** Single-column list item used on every viewport. */
export function ListCard({
  title,
  subtitle,
  tags,
  actions,
  onClick,
  dense = false,
  tagsAlign = 'start',
}: {
  title: ReactNode;
  subtitle?: ReactNode;
  tags?: ReactNode;
  actions?: ReactNode;
  onClick?: () => void;
  dense?: boolean;
  tagsAlign?: 'start' | 'center';
}) {
  return (
    <Card
      size="small"
      style={{ marginBottom: dense ? 6 : 8 }}
      styles={{ body: { padding: dense ? 8 : 12 } }}
      onClick={onClick}
    >
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', gap: 8 }}>
        <div style={{ minWidth: 0 }}>
          <Typography.Text strong style={{ wordBreak: 'break-all' }}>
            {title}
          </Typography.Text>
          {subtitle ? (
            <div style={{ color: 'var(--gw-text-muted)', fontSize: dense ? 11 : 12, marginTop: 2, wordBreak: 'break-all' }}>{subtitle}</div>
          ) : null}
        </div>
        {tags ? <div style={{ flex: 'none', alignSelf: tagsAlign === 'center' ? 'center' : 'flex-start' }}>{tags}</div> : null}
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
