import { useState } from 'react';
import { AutoComplete, Button, Drawer, Form, Input, Modal, Popconfirm, Space, Table, Typography, message } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { putAliases } from '../api/client';
import type { Alias, ConfigResponse } from '../api/types';
import { AliasForm } from './Edit';
import { useIsMobile } from '../components/useIsMobile';
import { MobileCard } from '../components/MobileCard';

export function AliasesPage({ config, onReload }: { config: ConfigResponse; onReload: () => Promise<void> }) {
  const isMobile = useIsMobile();
  const [editing, setEditing] = useState<number | null>(null);
  const [adding, setAdding] = useState(false);
  const [dragIndex, setDragIndex] = useState<number | null>(null);
  const [form] = Form.useForm();
  const models = config.routes.map((r) => r.match?.model || '').filter(Boolean);

  const move = async (from: number, to: number) => {
    if (from === to) return;
    const next = [...config.aliases];
    const [moved] = next.splice(from, 1);
    next.splice(to, 0, moved);
    try {
      await putAliases(next);
      await onReload();
    } catch (err) {
      message.error(err instanceof Error ? err.message : String(err));
    }
  };

  const remove = async (index: number) => {
    try {
      await putAliases(config.aliases.filter((_, i) => i !== index));
      message.success('已删除');
      await onReload();
    } catch (err) {
      message.error(err instanceof Error ? err.message : String(err));
    }
  };

  const add = async () => {
    const v = await form.validateFields();
    try {
      await putAliases([...config.aliases, { match: v.match, model: v.model }]);
      message.success('已添加');
      setAdding(false);
      form.resetFields();
      await onReload();
    } catch (err) {
      message.error(err instanceof Error ? err.message : String(err));
    }
  };

  const columns: ColumnsType<Alias> = [
    { title: '顺序', width: 60, render: (_, __, i) => <Typography.Text type="secondary">{i + 1}</Typography.Text> },
    { title: '客户端模型 glob', dataIndex: 'match', render: (v) => <Typography.Text code>{v}</Typography.Text> },
    { title: '逻辑模型', dataIndex: 'model', width: 160 },
    {
      title: '操作',
      width: 100,
      render: (_, __, i) => (
        <Button size="small" onClick={() => setEditing(i)}>
          编辑
        </Button>
      ),
    },
  ];

  return (
    <div style={{ padding: isMobile ? 12 : 20, width: '100%', overflowX: 'hidden' }}>
      <Space style={{ width: '100%', justifyContent: 'space-between' }}>
        <Typography.Title level={4} style={{ margin: 0 }}>
          Aliases
        </Typography.Title>
        <Button type="primary" onClick={() => setAdding(true)}>
          添加
        </Button>
      </Space>
      <Typography.Paragraph type="secondary" style={{ marginTop: 12 }}>
        {isMobile ? '用 ↑ ↓ 调整顺序。' : '顺序即匹配优先级；拖动行调整。'}
        alias 是客户端模型进入路由的唯一入口。
      </Typography.Paragraph>
      {isMobile ? (
        <div>
          {config.aliases.map((a, i) => (
            <MobileCard
              key={`${i}-${a.match}`}
              title={`${i + 1}. ${a.match}`}
              subtitle={`→ ${a.model}`}
              actions={
                <>
                  <Button size="small" disabled={i === 0} onClick={() => void move(i, i - 1)}>↑</Button>
                  <Button size="small" disabled={i === config.aliases.length - 1} onClick={() => void move(i, i + 1)}>↓</Button>
                  <Button size="small" onClick={() => setEditing(i)}>编辑</Button>
                  <Popconfirm title={`删除 alias ${a.match}?`} onConfirm={() => void remove(i)}>
                    <Button size="small" danger>删除</Button>
                  </Popconfirm>
                </>
              }
            />
          ))}
        </div>
      ) : (
      <Table
        rowKey={(a, i) => `${i}-${a.match}`}
        size="small"
        columns={columns}
        dataSource={config.aliases}
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

      <Drawer width={380} title={editing !== null ? `编辑 alias #${editing}` : ''} open={editing !== null} onClose={() => setEditing(null)} destroyOnClose>
        {editing !== null && config.aliases[editing] ? (
          <AliasForm
            key={editing}
            alias={config.aliases[editing]}
            index={editing}
            config={config}
            onSaved={async () => {
              setEditing(null);
              await onReload();
            }}
          />
        ) : null}
      </Drawer>

      <Modal title="添加 Alias" open={adding} onCancel={() => setAdding(false)} onOk={() => void add()} destroyOnClose>
        <Form form={form} layout="vertical" size="small">
          <Form.Item name="match" label="客户端模型 glob" rules={[{ required: true }]}>
            <Input placeholder="claude-opus*" />
          </Form.Item>
          <Form.Item name="model" label="逻辑模型" rules={[{ required: true }]}>
            <AutoComplete options={models.map((m) => ({ value: m }))} placeholder="strong" />
          </Form.Item>
        </Form>
      </Modal>
    </div>
  );
}
