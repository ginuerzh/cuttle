import { useState } from 'react';
import { Button, Drawer, Empty, Popconfirm, Space, Typography, message } from 'antd';
import { PlusOutlined } from '@ant-design/icons';
import { deleteRoute, reorderRoutes } from '../api/client';
import { translate as t } from '../i18n';
import type { ConfigResponse, Route } from '../api/types';
import { AddRouteModal, RouteForm } from './Edit';
import { ListCard } from '../components/ListCard';
import { ListPage } from '../components/ListPage';

export function RoutesPage({ config, onReload }: { config: ConfigResponse; onReload: () => Promise<void> }) {
  const [editing, setEditing] = useState<Route | null>(null);
  const [adding, setAdding] = useState(false);

  const move = async (from: number, to: number) => {
    if (from === to) return;
    const names = config.routes.map((r) => r.name);
    const [moved] = names.splice(from, 1);
    names.splice(to, 0, moved);
    try {
      await reorderRoutes(names);
      await onReload();
    } catch (err) {
      message.error(err instanceof Error ? err.message : String(err));
    }
  };

  const remove = async (name: string) => {
    try {
      await deleteRoute(name);
      message.success(t('已删除'));
      await onReload();
    } catch (err) {
      message.error(err instanceof Error ? err.message : String(err));
    }
  };



  return (
    <ListPage>
      <Space style={{ width: '100%', justifyContent: 'space-between', alignItems: 'center', marginBottom: 12 }}>
        <Typography.Title level={4} style={{ margin: 0 }}>
          Routes
        </Typography.Title>
        <Button type="primary" icon={<PlusOutlined />} onClick={() => setAdding(true)}>
          {t('添加')}
        </Button>
      </Space>
      <Typography.Paragraph type="secondary">
        {t('用 ↑ ↓ 调整顺序；顺序即 FIFO 优先级，catch-all 应放在最后。')}
      </Typography.Paragraph>
      {config.routes.length === 0 ? <Empty /> : config.routes.map((r, i) => (
        <ListCard
          key={r.name}
          title={`${i + 1}. ${r.name}`}
          subtitle={`${r.match?.model || 'catch-all'} · ${r.strategy || 'round'} · ${(r.nodes ?? []).join(', ') || 'no nodes'}`}
          actions={
            <>
              <Button size="small" disabled={i === 0} onClick={() => void move(i, i - 1)}>↑</Button>
              <Button size="small" disabled={i === config.routes.length - 1} onClick={() => void move(i, i + 1)}>↓</Button>
              <Button size="small" onClick={() => setEditing(r)}>{t('编辑')}</Button>
              <Popconfirm title={t('删除 {name}?', { name: r.name })} onConfirm={() => void remove(r.name)}>
                <Button size="small" danger>{t('删除')}</Button>
              </Popconfirm>
            </>
          }
        />
      ))}

      <Drawer width={420} title={editing ? t('编辑 {name}', { name: editing.name }) : ''} open={!!editing} onClose={() => setEditing(null)} destroyOnClose>
        {editing ? (
          <RouteForm
            route={editing}
            config={config}
            onSaved={async () => {
              setEditing(null);
              await onReload();
            }}
          />
        ) : null}
      </Drawer>
      <AddRouteModal
        open={adding}
        config={config}
        onClose={() => setAdding(false)}
        onDone={async () => {
          setAdding(false);
          await onReload();
        }}
        onError={(s) => message.error(s)}
      />
    </ListPage>
  );
}
