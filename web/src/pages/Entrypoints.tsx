import { useState } from 'react';
import { Button, Drawer, Empty, Popconfirm, Space, Typography, message } from 'antd';
import { putEntrypoints } from '../api/client';
import { translate as t } from '../i18n';
import type { ConfigResponse } from '../api/types';
import { entityLabel, labelMap } from '../labels';
import { AddEntrypointModal, EntrypointForm } from './Edit';
import { ListCard } from '../components/ListCard';
import { ListPage } from '../components/ListPage';

export function EntrypointsPage({ config, onReload }: { config: ConfigResponse; onReload: () => Promise<void> }) {
  const [editing, setEditing] = useState<number | null>(null);
  const [adding, setAdding] = useState(false);
  const routeNames = labelMap(config.routes);

  const move = async (from: number, to: number) => {
    if (from === to) return;
    const next = [...config.entrypoints];
    const [moved] = next.splice(from, 1);
    next.splice(to, 0, moved);
    try {
      await putEntrypoints(next);
      await onReload();
    } catch (err) {
      message.error(err instanceof Error ? err.message : String(err));
    }
  };

  const remove = async (index: number) => {
    try {
      await putEntrypoints(config.entrypoints.filter((_, i) => i !== index));
      message.success(t('已删除'));
      await onReload();
    } catch (err) {
      message.error(err instanceof Error ? err.message : String(err));
    }
  };

  return (
    <ListPage>
      <Space style={{ width: '100%', justifyContent: 'space-between' }}>
        <Typography.Title level={4} style={{ margin: 0 }}>
          {t('入口')}
        </Typography.Title>
        <Button type="primary" onClick={() => setAdding(true)}>
          {t('添加')}
        </Button>
      </Space>
      <Typography.Paragraph type="secondary" style={{ marginTop: 12 }}>
        {t('用 ↑ ↓ 调整顺序。入口按顺序匹配客户端模型，第一个命中的入口决定 route。')}
      </Typography.Paragraph>
      {config.entrypoints.length === 0 ? <Empty /> : config.entrypoints.map((e, i) => (
        <ListCard
          key={e.id}
          title={`${i + 1}. ${e.name}`}
          subtitle={`${(e.match ?? []).join(', ')} → ${entityLabel(e.route, routeNames) || t('未连接')}`}
          actions={
            <>
              <Button size="small" disabled={i === 0} onClick={() => void move(i, i - 1)}>↑</Button>
              <Button size="small" disabled={i === config.entrypoints.length - 1} onClick={() => void move(i, i + 1)}>↓</Button>
              <Button size="small" onClick={() => setEditing(i)}>{t('编辑')}</Button>
              <Popconfirm title={t('删除入口 {name}?', { name: e.name })} onConfirm={() => void remove(i)}>
                <Button size="small" danger>{t('删除')}</Button>
              </Popconfirm>
            </>
          }
        />
      ))}

      <Drawer width={380} title={editing !== null ? t('编辑入口 #{index}', { index: editing }) : ''} open={editing !== null} onClose={() => setEditing(null)} destroyOnClose>
        {editing !== null && config.entrypoints[editing] ? (
          <EntrypointForm
            key={editing}
            entrypoint={config.entrypoints[editing]}
            index={editing}
            config={config}
            onSaved={async () => {
              setEditing(null);
              await onReload();
            }}
          />
        ) : null}
      </Drawer>

      <AddEntrypointModal
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
    </ListPage>
  );
}
