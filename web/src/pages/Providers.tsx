import { useState } from 'react';
import { Button, Drawer, Modal, Popconfirm, Space, Table, Tag, Typography, message } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { deleteProvider, getProviderModels, testProvider } from '../api/client';
import type { ConfigResponse, Provider } from '../api/types';
import { ProviderForm } from './Edit';

export function ProvidersPage({ config, onReload }: { config: ConfigResponse; onReload: () => Promise<void> }) {
  const [editing, setEditing] = useState<Provider | null>(null);
  const [models, setModels] = useState<{ name: string; list: string[] } | null>(null);

  const test = async (name: string) => {
    try {
      const r = await testProvider(name);
      if (r.ok) message.success(`${name}: ${r.latencyMs}ms`);
      else message.error(`${name}: ${r.error || `status ${r.status}`}`);
    } catch (err) {
      message.error(err instanceof Error ? err.message : String(err));
    }
  };

  const showModels = async (name: string) => {
    try {
      setModels({ name, list: await getProviderModels(name) });
    } catch (err) {
      message.error(err instanceof Error ? err.message : String(err));
    }
  };

  const remove = async (name: string) => {
    try {
      await deleteProvider(name);
      message.success('已删除');
      await onReload();
    } catch (err) {
      message.error(err instanceof Error ? err.message : String(err));
    }
  };

  const columns: ColumnsType<Provider> = [
    { title: '名称', dataIndex: 'name', width: 180, render: (v) => <Typography.Text strong>{v}</Typography.Text> },
    { title: 'protocol', dataIndex: 'protocol', width: 100 },
    { title: 'baseURL', dataIndex: 'baseURL', ellipsis: true },
    {
      title: 'keys',
      dataIndex: 'keysSet',
      width: 70,
      render: (v: number) => <Tag>{v}</Tag>,
    },
    {
      title: '探测',
      dataIndex: 'probe',
      width: 150,
      render: (p?: Provider['probe']) =>
        p ? (
          <Tag color={p.ok ? 'green' : 'red'}>{p.ok ? `${p.latencyMs}ms` : p.error || 'fail'}</Tag>
        ) : (
          <Typography.Text type="secondary">—</Typography.Text>
        ),
    },
    {
      title: '状态',
      dataIndex: 'enabled',
      width: 90,
      render: (enabled: boolean | undefined) =>
        enabled === false ? <Tag>disabled</Tag> : <Tag color="green">enabled</Tag>,
    },
    {
      title: '操作',
      width: 260,
      render: (_, p) => (
        <Space size={4}>
          <Button size="small" onClick={() => void test(p.name)}>
            测试
          </Button>
          <Button size="small" onClick={() => void showModels(p.name)}>
            模型
          </Button>
          <Button size="small" onClick={() => setEditing(p)}>
            编辑
          </Button>
          <Popconfirm title={`删除 ${p.name}?`} onConfirm={() => void remove(p.name)}>
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
        Providers
      </Typography.Title>
      <Table rowKey="name" size="small" columns={columns} dataSource={config.providers} pagination={false} />

      <Drawer
        width={420}
        title={editing ? `编辑 ${editing.name}` : ''}
        open={!!editing}
        onClose={() => setEditing(null)}
        destroyOnClose
      >
        {editing ? (
          <ProviderForm
            provider={editing}
            onSaved={async () => {
              setEditing(null);
              await onReload();
            }}
          />
        ) : null}
      </Drawer>

      <Modal
        title={models ? `${models.name} 的模型` : ''}
        open={!!models}
        footer={null}
        onCancel={() => setModels(null)}
      >
        <Space wrap>
          {models?.list.map((m) => (
            <Tag key={m}>{m}</Tag>
          ))}
          {models && models.list.length === 0 ? <Typography.Text type="secondary">供应商没有返回模型</Typography.Text> : null}
        </Space>
      </Modal>
    </div>
  );
}
