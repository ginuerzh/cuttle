import { useState } from 'react';
import { Button, Drawer, Popconfirm, Space, Table, Typography, message } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { putAliases } from '../api/client';
import { translate as t } from '../i18n';
import type { Alias, ConfigResponse } from '../api/types';
import { AddAliasModal, AliasForm } from './Edit';
import { useIsMobile } from '../components/useIsMobile';
import { ListCard } from '../components/ListCard';

export function AliasesPage({ config, onReload }: { config: ConfigResponse; onReload: () => Promise<void> }) {
  const isMobile = useIsMobile();
  const [editing, setEditing] = useState<number | null>(null);
  const [adding, setAdding] = useState(false);
  const [dragIndex, setDragIndex] = useState<number | null>(null);

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
      message.success(t('已删除'));
      await onReload();
    } catch (err) {
      message.error(err instanceof Error ? err.message : String(err));
    }
  };


  const columns: ColumnsType<Alias> = [
    { title: t('顺序'), width: 60, render: (_, __, i) => <Typography.Text type="secondary">{i + 1}</Typography.Text> },
    { title: t('客户端模型 glob'), dataIndex: 'match', render: (v) => <Typography.Text code>{v}</Typography.Text> },
    { title: t('逻辑模型'), dataIndex: 'model', width: 160 },
    {
      title: t('操作'),
      width: 100,
      render: (_, __, i) => (
        <Button size="small" onClick={() => setEditing(i)}>
          {t('编辑')}
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
          {t('添加')}
        </Button>
      </Space>
      <Typography.Paragraph type="secondary" style={{ marginTop: 12 }}>
        {isMobile ? t('用 ↑ ↓ 调整顺序。') : t('顺序即匹配优先级；拖动行调整。')}
        {t('alias 是客户端模型进入路由的唯一入口。')}
      </Typography.Paragraph>
      {isMobile ? (
        <div>
          {config.aliases.map((a, i) => (
            <ListCard
              key={`${i}-${a.match}`}
              title={`${i + 1}. ${a.match}`}
              subtitle={`→ ${a.model}`}
              actions={
                <>
                  <Button size="small" disabled={i === 0} onClick={() => void move(i, i - 1)}>↑</Button>
                  <Button size="small" disabled={i === config.aliases.length - 1} onClick={() => void move(i, i + 1)}>↓</Button>
                  <Button size="small" onClick={() => setEditing(i)}>{t('编辑')}</Button>
                  <Popconfirm title={t('删除 alias {name}?', { name: a.match })} onConfirm={() => void remove(i)}>
                    <Button size="small" danger>{t('删除')}</Button>
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

      <Drawer width={380} title={editing !== null ? t('编辑 alias #{index}', { index: editing }) : ''} open={editing !== null} onClose={() => setEditing(null)} destroyOnClose>
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

      <AddAliasModal
        open={adding}
        config={config}
        onClose={() => setAdding(false)}
        onDone={async () => {
          message.success(t('已添加'));
          setAdding(false);
          await onReload();
        }}
        onError={(s) => message.error(s)}
      />
    </div>
  );
}
