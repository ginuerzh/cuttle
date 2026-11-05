import { useState } from 'react';
import { Button, Drawer, Popconfirm, Space, Table, Tag, Typography, message } from 'antd';
import { PlusOutlined } from '@ant-design/icons';
import type { ColumnsType } from 'antd/es/table';
import { deleteNode } from '../api/client';
import { translate as t } from '../i18n';
import type { ConfigResponse, Node } from '../api/types';
import { AddNodeModal, NodeForm } from './Edit';
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
  const [adding, setAdding] = useState(false);

  const remove = async (name: string) => {
    try {
      await deleteNode(name);
      message.success(t('已删除'));
      await onReload();
    } catch (err) {
      message.error(err instanceof Error ? err.message : String(err));
    }
  };

  const columns: ColumnsType<Node> = [
    {
      title: t('名称'),
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
      title: t('启用'),
      dataIndex: 'enabled',
      width: 70,
      render: (v?: boolean) => (v === false ? <Tag>off</Tag> : <Tag color="green">on</Tag>),
    },
    {
      title: t('操作'),
      width: 130,
      render: (_, n) => (
        <Space size={4}>
          <Button size="small" onClick={() => setEditing(n)}>
            {t('编辑')}
          </Button>
          <Popconfirm title={t('删除 {name}?', { name: n.name })} onConfirm={() => void remove(n.name)}>
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
      <Space style={{ width: '100%', justifyContent: 'space-between', alignItems: 'center', marginBottom: 12 }}>
        <Typography.Title level={4} style={{ margin: 0 }}>
          Nodes
        </Typography.Title>
        <Button type="primary" icon={<PlusOutlined />} onClick={() => setAdding(true)}>
          {t('添加')}
        </Button>
      </Space>
      <div style={{ marginTop: 12 }}>
        {isMobile ? (
          <div>
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
                      <Button size="small" onClick={() => setEditing(n)}>{t('编辑')}</Button>
                      <Popconfirm title={t('删除 {name}?', { name: n.name })} onConfirm={() => void remove(n.name)}>
                        <Button size="small" danger>{t('删除')}</Button>
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
      </div>
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
      <AddNodeModal
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
