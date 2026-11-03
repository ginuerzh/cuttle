import { Empty, Typography } from 'antd';

export function Placeholder({ title, phase }: { title: string; phase: string }) {
  return (
    <div style={{ padding: 48 }}>
      <Typography.Title level={4} style={{ marginTop: 0 }}>
        {title}
      </Typography.Title>
      <Empty description={`${phase} 提供`} />
    </div>
  );
}
