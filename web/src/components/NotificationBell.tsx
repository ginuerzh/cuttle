import { BellOutlined } from '@ant-design/icons';
import { Badge, Button, Empty, Popover, Typography } from 'antd';
import { translate as t } from '../i18n';
import type { AppNotification } from '../notifications';
import { formatDateTime } from '../time';

interface NotificationListProps {
  items: AppNotification[];
  onClear: () => void;
}

/** Testable content of the notification popover. */
export function NotificationList({ items, onClear }: NotificationListProps) {
  return (
    <div style={{ width: 360, maxWidth: '80vw' }}>
      <div
        style={{
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
          gap: 12,
          marginBottom: 8,
        }}
      >
        <Typography.Text strong>{t('通知')}</Typography.Text>
        <Button size="small" type="link" disabled={items.length === 0} onClick={onClear}>
          {t('清除')}
        </Button>
      </div>
      {items.length === 0 ? (
        <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={t('暂无通知')} />
      ) : (
        <div style={{ maxHeight: 360, overflowY: 'auto' }}>
          {items.map((item) => (
            <div
              key={item.id}
              style={{ padding: '8px 0', borderTop: '1px solid var(--gw-border)' }}
            >
              <div style={{ fontSize: 13 }}>{item.title}</div>
              <div style={{ fontSize: 12, opacity: 0.75 }}>{item.detail}</div>
              <div style={{ fontSize: 11, opacity: 0.6 }}>{formatDateTime(item.ts)}</div>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

interface NotificationBellProps {
  items: AppNotification[];
  onClear: () => void;
}

/** Overview bell: a red dot while messages exist, click to open the list. */
export function NotificationBell({ items, onClear }: NotificationBellProps) {
  return (
    <Popover
      trigger="click"
      placement="bottomLeft"
      content={<NotificationList items={items} onClear={onClear} />}
    >
      <Badge dot={items.length > 0} offset={[-4, 4]}>
        <Button
          type="text"
          icon={<BellOutlined />}
          aria-label={
            items.length ? t('通知 ({count})', { count: items.length }) : t('通知')
          }
        />
      </Badge>
    </Popover>
  );
}
