import { useEffect, useState } from 'react';
import { Button, Drawer, Empty, Popconfirm, Space, Tag, Typography, message } from 'antd';
import { PlusOutlined } from '@ant-design/icons';
import { deleteRoute, getRouteHealth } from '../api/client';
import { translate as t } from '../i18n';
import type { ConfigResponse, Route, RouteHealth } from '../api/types';
import { AddRouteModal, RouteForm } from './Edit';
import { ListCard } from '../components/ListCard';
import { ListPage } from '../components/ListPage';

export function RoutesPage({ config, onReload }: { config: ConfigResponse; onReload: () => Promise<void> }) {
  const [editing, setEditing] = useState<Route | null>(null);
  const [adding, setAdding] = useState(false);
  const [health, setHealth] = useState<RouteHealth[]>([]);

  useEffect(() => {
    let cancelled = false;
    const load = () => {
      getRouteHealth()
        .then((rows) => {
          if (!cancelled) setHealth(rows);
        })
        .catch(() => {
          if (!cancelled) setHealth([]);
        });
    };
    load();
    const timer = window.setInterval(load, 10_000);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, [config]);

  const healthByName = new Map(health.map((h) => [h.name, h]));

  const remove = async (name: string) => {
    try {
      await deleteRoute(name);
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
          {t('路由')}
        </Typography.Title>
        <Button type="primary" icon={<PlusOutlined />} onClick={() => setAdding(true)}>
          {t('添加')}
        </Button>
      </Space>
      {config.routes.length === 0 ? <Empty /> : config.routes.map((r, i) => {
        const h = healthByName.get(r.name);
        const dead = h?.nodes.filter((n) => n.dead).length ?? 0;
        return (
          <ListCard
            key={r.name}
            title={`${i + 1}. ${r.name}`}
            subtitle={`${r.strategy || 'round'} · maxFails ${r.maxFails ?? 1} / ${r.failTimeout ?? '10s'} · ${(r.nodes ?? []).join(', ') || 'no nodes'}`}
            tags={h ? (
              <Tag color={dead > 0 ? 'red' : 'green'}>
                {dead > 0 ? t('{count} dead', { count: dead }) : t('正常')}
              </Tag>
            ) : null}
            actions={
              <>
                <Button size="small" onClick={() => setEditing(r)}>{t('编辑')}</Button>
                <Popconfirm title={t('删除 {name}?', { name: r.name })} onConfirm={() => void remove(r.name)}>
                  <Button size="small" danger>{t('删除')}</Button>
                </Popconfirm>
              </>
            }
          />
        );
      })}

      <Drawer width={420} title={editing ? t('编辑 {name}', { name: editing.name }) : ''} open={!!editing} onClose={() => setEditing(null)} destroyOnClose>
        {editing ? (
          <RouteForm
            route={editing}
            config={config}
            health={healthByName.get(editing.name)}
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
    </ListPage>
  );
}
