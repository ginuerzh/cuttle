import { useState } from 'react';
import { Button, Drawer, Popconfirm, Space, Table, Tag, Typography, message } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { deleteRoute, reorderRoutes } from '../api/client';
import type { ConfigResponse, Route } from '../api/types';
import { RouteForm } from './Edit';

export function RoutesPage({ config, onReload }: { config: ConfigResponse; onReload: () => Promise<void> }) {
  const [editing, setEditing] = useState<Route | null>(null);
  const [dragIndex, setDragIndex] = useState<number | null>(null);
  const nodesByName = new Map(config.nodes.map((n) => [n.name, n]));

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
      message.success('已删除');
      await onReload();
    } catch (err) {
      message.error(err instanceof Error ? err.message : String(err));
    }
  };

  const columns: ColumnsType<Route> = [
    {
      title: '顺序',
      width: 60,
      render: (_, __, i) => <Typography.Text type="secondary">{i + 1}</Typography.Text>,
    },
    { title: '名称', dataIndex: 'name', width: 160, render: (v) => <Typography.Text strong>{v}</Typography.Text> },
    {
      title: '逻辑模型',
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
    { title: 'strategy', dataIndex: 'strategy', width: 90 },
    {
      title: 'tiers',
      width: 160,
      render: (_, r) => {
        const tiers = (r.nodes ?? [])
          .map((name) => nodesByName.get(name)?.tier)
          .filter((t): t is string => !!t);
        return tiers.length ? (
          <Space wrap size={2}>
            {[...new Set(tiers)].map((t) => (
              <Tag key={t} color="orange">
                {t}
              </Tag>
            ))}
          </Space>
        ) : (
          <Typography.Text type="secondary">base</Typography.Text>
        );
      },
    },
    {
      title: '操作',
      width: 140,
      render: (_, r) => (
        <Space size={4}>
          <Button size="small" onClick={() => setEditing(r)}>
            编辑
          </Button>
          <Popconfirm title={`删除 ${r.name}?`} onConfirm={() => void remove(r.name)}>
            <Button size="small" danger>
              删除
            </Button>
          </Popconfirm>
        </Space>
      ),
    },
  ];

  return (
    <div style={{ padding: 20, width: '100%', overflow: 'auto' }}>
      <Typography.Title level={4} style={{ marginTop: 0 }}>
        Routes
      </Typography.Title>
      <Typography.Paragraph type="secondary">拖动行调整顺序：顺序即 FIFO 优先级，catch-all 应放在最后。</Typography.Paragraph>
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

      <Drawer width={420} title={editing ? `编辑 ${editing.name}` : ''} open={!!editing} onClose={() => setEditing(null)} destroyOnClose>
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
    </div>
  );
}
