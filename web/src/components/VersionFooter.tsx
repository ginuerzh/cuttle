import { Typography } from 'antd';
import type { Health } from '../api/types';
import { versionLabel, versionTitle } from '../version';

export function VersionFooter({ health }: { health: Health | null }) {
  if (!health) return null;
  return (
    <Typography.Text
      type="secondary"
      style={{ fontSize: 12, display: 'block', textAlign: 'center', wordBreak: 'break-all' }}
      title={versionTitle(health)}
    >
      {versionLabel(health)}
    </Typography.Text>
  );
}
