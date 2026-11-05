import { useState } from 'react';
import { Button, Drawer, Empty, Popconfirm, Space, Switch, Tag, Typography, message } from 'antd';
import { PlusOutlined } from '@ant-design/icons';
import { deleteNode, updateNode } from '../api/client';
import { translate as t } from '../i18n';
import type { ConfigResponse, Node } from '../api/types';
import { AddNodeModal, NodeForm } from './Edit';
import { ListCard } from '../components/ListCard';
import { ListPage } from '../components/ListPage';

export function NodesPage({
  config,
  onReload,
}: {
  config: ConfigResponse;
  onReload: () => Promise<void>;
}) {
  const [editing, setEditing] = useState<Node | null>(null);
  const [adding, setAdding] = useState(false);
  const [busy, setBusy] = useState<string | null>(null);

  const toggle = async (n: Node, enabled: boolean) => {
    setBusy(n.name);
    try {
      await updateNode(n.name, {
        name: n.name,
        provider: n.provider,
        model: n.model,
        backup: n.backup,
        weight: n.weight,
        enabled,
      });
      await onReload();
    } catch (err) {
      message.error(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(null);
    }
  };

  const remove = async (name: string) => {
    try {
      await deleteNode(name);
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
          {t('节点')}
        </Typography.Title>
        <Button type="primary" icon={<PlusOutlined />} onClick={() => setAdding(true)}>
          {t('添加')}
        </Button>
      </Space>
      <div style={{ marginTop: 12 }}>
        {config.nodes.length === 0 ? <Empty /> : config.nodes.map((n) => {
          return (
            <ListCard
              key={n.name}
              title={n.name}
              subtitle={`${n.provider} · ${n.model}`}
              tags={
                <Space direction="vertical" align="end" size={2}>
                  {n.backup ? <Tag color="purple">backup</Tag> : null}
                  <Switch
                    size="small"
                    checked={n.enabled !== false}
                    disabled={busy === n.name}
                    onChange={(checked) => void toggle(n, checked)}
                    aria-label={t('切换 {name}', { name: n.name })}
                  />
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
      <Drawer
        open={!!editing}
        width={420}
        title={editing ? t('节点 · {name}', { name: editing.name }) : t('节点')}
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
    </ListPage>
  );
}
