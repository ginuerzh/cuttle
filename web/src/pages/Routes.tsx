import { useState } from 'react';
import { Button, Drawer, Popconfirm, Space, Table, Tag, Typography, message } from 'antd';
import { PlusOutlined } from '@ant-design/icons';
import type { ColumnsType } from 'antd/es/table';
import { deleteRoute, reorderRoutes } from '../api/client';
import { translate as t } from '../i18n';
import type { ConfigResponse, Route } from '../api/types';
import { AddRouteModal, RouteForm } from './Edit';
import { useIsMobile } from '../components/useIsMobile';
import { MobileCard } from '../components/MobileCard';

export function RoutesPage({ config, onReload }: { config: ConfigResponse; onReload: () => Promise<void> }) {
  const isMobile = useIsMobile();
  const [editing, setEditing] = useState<Route | null>(null);
  const [adding, setAdding] = useState(false);
  const [dragIndex, setDragIndex] = useState<number | null>(null);

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

  const columns: ColumnsType<Route> = [
    {
      title: t('顺序'),
      width: 60,
      render: (_, __, i) => <Typography.Text type="secondary">{i + 1}</Typography.Text>,
    },
    { title: t('名称'), dataIndex: 'name', width: 160, render: (v) => <Typography.Text strong>{v}</Typography.Text> },
    {
      title: t('逻辑模型'),
      width: 140,
      render: (_, r) => r.match?.model || <Tag>catch-all</Tag>,
    },
    {
      title: 'nodes',
      render: (_, r) => (
        <Space wrap size={2}>
          {(r.nodes ?? []).map((n) => (
            <Tag key={n}>{n}</Tag>
          ))}
        </Space>
      ),
    },
    { title: 'strategy', dataIndex: 'strategy', width: 90, responsive: ['lg'] },
    {
      title: t('操作'),
      width: 140,
      render: (_, r) => (
        <Space size={4}>
          <Button size="small" onClick={() => setEditing(r)}>
            {t('编辑')}
          </Button>
          <Popconfirm title={t('删除 {name}?', { name: r.name })} onConfirm={() => void remove(r.name)}>
            <Button size="small" danger>
              {t('删除')}
            </Button>
          </Popconfirm>
        </Space>
      ),
    },
  ];

  return (
    <div style={{ padding: isMobile ? 12 : 20, width: '100%', overflowX: 'hidden' }}>
      <Space style={{ width: '100%', justifyContent: 'space-between', alignItems: 'center' }}>
        <Typography.Title level={4} style={{ margin: 0 }}>
          Routes
        </Typography.Title>
        <Button type="primary" icon={<PlusOutlined />} onClick={() => setAdding(true)}>
          {t('添加')}
        </Button>
      </Space>
      <Typography.Paragraph type="secondary">
        {isMobile ? t('用 ↑ ↓ 调整顺序；顺序即 FIFO 优先级，catch-all 应放在最后。') : t('拖动行调整顺序：顺序即 FIFO 优先级，catch-all 应放在最后。')}
      </Typography.Paragraph>
      {isMobile ? (
        <div>
          {config.routes.map((r, i) => {
            return (
              <MobileCard
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
            );
          })}
        </div>
      ) : (
      <Table
        rowKey="name"
        size="small"
        columns={columns}
        dataSource={config.routes}
        pagination={false}
        onRow={(_, index) => ({
          draggable: true,
          onDragStart: () => setDragIndex(index ?? null),
          onDragOver: (e) => e.preventDefault(),
          onDrop: () => {
            if (dragIndex !== null && index !== undefined) void move(dragIndex, index);
            setDragIndex(null);
          },
          style: { cursor: 'grab' },
        })}
      />
      )}

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
    </div>
  );
}
