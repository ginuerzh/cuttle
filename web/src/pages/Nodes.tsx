import { useState } from 'react';
import { Button, Drawer, Popconfirm, Space, Table, Tag, Typography, message } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { deleteNode } from '../api/client';
import type { ConfigResponse, Node } from '../api/types';
import { NodeForm } from './Edit';
import { useIsMobile } from '../components/useIsMobile';
import { MobileCard } from '../components/MobileCard';

export function NodesPage({
  config,
  onReload,
}: {
  config: ConfigResponse;
  onReload: () => Promise<void>;
}) {
  const isMobile = useIsMobile();
  const [editing, setEditing] = useState<Node | null>(null);

  const remove = async (name: string) => {
    try {
      await deleteNode(name);
      message.success('已删除');
      await onReload();
    } catch (err) {
      message.error(err instanceof Error ? err.message : String(err));
    }
  };

  const columns: ColumnsType<Node> = [
    {
      title: '名称',
      dataIndex: 'name',
      width: 180,
      render: (v) => <Typography.Text strong>{v}</Typography.Text>,
    },
    { title: 'provider', dataIndex: 'provider', width: 160, responsive: ['md'] },
    {
      title: 'models',
      dataIndex: 'models',
      responsive: ['xl'],
      render: (m?: Record<string, string>) => {
        const entries = Object.entries(m ?? {});
        if (entries.length === 0) return <Typography.Text type="secondary">—</Typography.Text>;
        return (
          <Space wrap size={2}>
            {entries.map(([logical, vendor]) => (
              <Tag key={logical}>
                {logical} → {vendor}
              </Tag>
            ))}
          </Space>
        );
      },
    },
    {
      title: 'backup',
      dataIndex: 'backup',
      width: 80,
      responsive: ['lg'],
      render: (v?: boolean) => (v ? <Tag color="purple">backup</Tag> : null),
    },
    { title: 'weight', dataIndex: 'weight', width: 70, responsive: ['lg'] },
    {
      title: '启用',
      dataIndex: 'enabled',
      width: 70,
      render: (v?: boolean) => (v === false ? <Tag>off</Tag> : <Tag color="green">on</Tag>),
    },
    {
      title: '操作',
      width: 130,
      render: (_, n) => (
        <Space size={4}>
          <Button size="small" onClick={() => setEditing(n)}>
            编辑
          </Button>
          <Popconfirm title={`删除 ${n.name}?`} onConfirm={() => void remove(n.name)}>
            <Button size="small" danger>
              删除
            </Button>
          </Popconfirm>
        </Space>
      ),
    },
  ];

  return (
    <>
      {isMobile ? (
        <div style={{ padding: 12 }}>
          {config.nodes.map((n) => {
            const models = Object.keys(n.models ?? {}).length;
            return (
              <MobileCard
                key={n.name}
                title={n.name}
                subtitle={`${n.provider} · ${models} models`}
                tags={
                  <Space size={2}>
                    {n.backup ? <Tag color="purple">backup</Tag> : null}
                    {n.enabled === false ? <Tag>off</Tag> : <Tag color="green">on</Tag>}
                  </Space>
                }
                actions={
                  <>
                    <Button size="small" onClick={() => setEditing(n)}>编辑</Button>
                    <Popconfirm title={`删除 ${n.name}?`} onConfirm={() => void remove(n.name)}>
                      <Button size="small" danger>删除</Button>
                    </Popconfirm>
                  </>
                }
              />
            );
          })}
        </div>
      ) : (
        <Table rowKey="name" dataSource={config.nodes} columns={columns} pagination={false} />
      )}
      <Drawer
        open={!!editing}
        width={420}
        title={editing ? `Node · ${editing.name}` : 'Node'}
        onClose={() => setEditing(null)}
        destroyOnClose
      >
        {editing ? (
          <NodeForm
            key={editing.name}
            node={editing}
            config={config}
            onSaved={async () => {
              setEditing(null);
              await onReload();
            }}
          />
        ) : null}
      </Drawer>
    </>
  );
}
