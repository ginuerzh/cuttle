import { useState } from 'react';
import { Button, Drawer, Empty, Popconfirm, Space, Typography, message } from 'antd';
import { putAliases } from '../api/client';
import { translate as t } from '../i18n';
import type { ConfigResponse } from '../api/types';
import { AddAliasModal, AliasForm } from './Edit';
import { ListCard } from '../components/ListCard';
import { ListPage } from '../components/ListPage';

export function AliasesPage({ config, onReload }: { config: ConfigResponse; onReload: () => Promise<void> }) {
  const [editing, setEditing] = useState<number | null>(null);
  const [adding, setAdding] = useState(false);

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




  return (
    <ListPage>
      <Space style={{ width: '100%', justifyContent: 'space-between' }}>
        <Typography.Title level={4} style={{ margin: 0 }}>
          {t('别名')}
        </Typography.Title>
        <Button type="primary" onClick={() => setAdding(true)}>
          {t('添加')}
        </Button>
      </Space>
      <Typography.Paragraph type="secondary" style={{ marginTop: 12 }}>
        {t('用 ↑ ↓ 调整顺序。alias 是客户端模型进入路由的唯一入口。')}
      </Typography.Paragraph>
      {config.aliases.length === 0 ? <Empty /> : config.aliases.map((a, i) => (
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
    </ListPage>
  );
}
