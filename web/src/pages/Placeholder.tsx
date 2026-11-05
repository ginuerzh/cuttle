import { Empty, Typography } from 'antd';
import { translate as t } from '../i18n';

export function Placeholder({ title, phase }: { title: string; phase: string }) {
  return (
    <div style={{ padding: 48 }}>
      <Typography.Title level={4} style={{ marginTop: 0 }}>
        {title}
      </Typography.Title>
      <Empty description={t('{phase} 提供', { phase })} />
    </div>
  );
}
