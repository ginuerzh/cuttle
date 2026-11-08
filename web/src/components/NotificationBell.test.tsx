import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import { setActiveLang } from '../i18n';
import type { AppNotification } from '../notifications';
import { formatDateTime } from '../time';
import { NotificationBell, NotificationList } from './NotificationBell';

setActiveLang('zh-CN');

const items: AppNotification[] = [
  {
    id: '2026-11-08T12:00:00Z-0',
    kind: 'auth-failure',
    ts: '2026-11-08T12:00:00Z',
    title: '网关密钥鉴权失败',
    detail: '密钥无效 · 192.0.2.7 · POST /v1/messages',
  },
];

describe('NotificationList', () => {
  it('shows an empty state with a disabled clear button', () => {
    const html = renderToStaticMarkup(<NotificationList items={[]} onClear={() => undefined} />);

    expect(html).toContain('暂无通知');
    expect(html).toContain('disabled');
  });

  it('lists every notification with its detail and time', () => {
    const html = renderToStaticMarkup(
      <NotificationList items={items} onClear={() => undefined} />,
    );

    expect(html).toContain('网关密钥鉴权失败');
    expect(html).toContain('密钥无效');
    expect(html).toContain('192.0.2.7');
    expect(html).toContain('POST /v1/messages');
    expect(html).toContain(formatDateTime(items[0].ts));
    expect(html).not.toContain('disabled');
  });
});

describe('NotificationBell', () => {
  it('shows the dot and count only when notifications exist', () => {
    const idle = renderToStaticMarkup(
      <NotificationBell items={[]} onClear={() => undefined} />,
    );
    expect(idle).not.toContain('ant-badge-dot');
    expect(idle).toContain('aria-label="通知"');

    const active = renderToStaticMarkup(
      <NotificationBell items={items} onClear={() => undefined} />,
    );
    expect(active).toContain('ant-badge-dot');
    expect(active).toContain('aria-label="通知 (1)"');
  });
});
