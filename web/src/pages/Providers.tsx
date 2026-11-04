import { useState } from 'react';
import { Button, Drawer, Modal, Popconfirm, Space, Switch, Table, Tag, Typography, message } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { deleteProvider, getProviderModels, testProvider, updateProvider } from '../api/client';
import type { ConfigResponse, Provider, ProviderModel } from '../api/types';
import { ProviderForm } from './Edit';
import { configuredAPIs, modelApiLabel, modelMeta, providerPayload, toggleModelOverride } from './presets';
import { useIsMobile } from '../components/useIsMobile';
import { MobileCard } from '../components/MobileCard';

export function ProvidersPage({ config, onReload }: { config: ConfigResponse; onReload: () => Promise<void> }) {
  const isMobile = useIsMobile();
  const [editing, setEditing] = useState<Provider | null>(null);
  const [models, setModels] = useState<{ name: string; list: ProviderModel[] } | null>(null);
  const [modelsBusy, setModelsBusy] = useState(false);

  const test = async (name: string) => {
    try {
      const r = await testProvider(name);
      if (r.ok) message.success(`${name}: ${r.latencyMs}ms`);
      else message.error(`${name}: ${r.error || `status ${r.status}`}`);
    } catch (err) {
      message.error(err instanceof Error ? err.message : String(err));
    }
  };

  const showModels = async (name: string, refresh = false) => {
    setModelsBusy(true);
    try {
      setModels({ name, list: await getProviderModels(name, refresh) });
    } catch (err) {
      message.error(err instanceof Error ? err.message : String(err));
    } finally {
      setModelsBusy(false);
    }
  };

  const toggleModel = async (model: ProviderModel, checked: boolean) => {
    if (!models) return;
    const provider = config.providers.find((p) => p.name === models.name);
    if (!provider) {
      message.error(`未找到 Provider ${models.name}`);
      return;
    }
    const disabled = !checked;
    const next = toggleModelOverride(provider.models, model.id, disabled);
    setModelsBusy(true);
    try {
      await updateProvider(provider.name, providerPayload(provider, { models: next }));
      setModels((cur) =>
        cur && cur.name === provider.name
          ? { ...cur, list: cur.list.map((m) => (m.id === model.id ? { ...m, disabled } : m)) }
          : cur,
      );
      await onReload();
    } catch (err) {
      message.error(err instanceof Error ? err.message : String(err));
    } finally {
      setModelsBusy(false);
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

  const apiTags = (p: Provider) => {
    const apis = configuredAPIs(p);
    if (apis.length === 0) return <Typography.Text type="secondary">—</Typography.Text>;
    return (
      <Space wrap size={2}>
        {apis.map((api) => (
          <Tag key={api}>{api}</Tag>
        ))}
      </Space>
    );
  };

  const columns: ColumnsType<Provider> = [
    { title: '名称', dataIndex: 'name', width: 180, render: (v) => <Typography.Text strong>{v}</Typography.Text> },
    { title: 'type', dataIndex: 'type', width: 120, responsive: ['md'] },
    {
      title: 'APIs',
      key: 'apis',
      responsive: ['lg'],
      render: (_, p) => apiTags(p),
    },
    {
      title: 'keys',
      dataIndex: 'keysSet',
      width: 70,
      responsive: ['lg'],
      render: (v: number) => <Tag>{v}</Tag>,
    },
    {
      title: '探测',
      dataIndex: 'probe',
      width: 150,
      responsive: ['xl'],
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
      width: 230,
      render: (_, p) => (
        <Space wrap size={4}>
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
    <div style={{ padding: isMobile ? 12 : 20, width: '100%', overflowX: 'hidden' }}>
      <Typography.Title level={4} style={{ marginTop: 0 }}>
        Providers
      </Typography.Title>
      {isMobile ? (
        <div>
          {config.providers.map((p) => (
            <MobileCard
              key={p.name}
              title={p.name}
              subtitle={`${p.type} · ${configuredAPIs(p).join(' / ') || 'no API'}`}
              tags={
                p.probe ? (
                  <Tag color={p.probe.ok ? 'green' : 'red'}>
                    {p.probe.ok ? `${p.probe.latencyMs}ms` : p.probe.error || 'fail'}
                  </Tag>
                ) : (
                  <Tag>{p.keysSet} keys</Tag>
                )
              }
              actions={
                <>
                  <Button size="small" onClick={() => void test(p.name)}>测试</Button>
                  <Button size="small" onClick={() => void showModels(p.name)}>模型</Button>
                  <Button size="small" onClick={() => setEditing(p)}>编辑</Button>
                  <Popconfirm title={`删除 ${p.name}?`} onConfirm={() => void remove(p.name)}>
                    <Button size="small" danger>删除</Button>
                  </Popconfirm>
                </>
              }
            />
          ))}
        </div>
      ) : (
        <Table rowKey="name" size="small" columns={columns} dataSource={config.providers} pagination={false} />
      )}

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
        width={isMobile ? '94%' : 640}
        footer={
          <Button size="small" loading={modelsBusy} onClick={() => models && void showModels(models.name, true)}>
            刷新
          </Button>
        }
        onCancel={() => setModels(null)}
      >
        <div style={{ width: '100%', maxHeight: 'min(480px, 60dvh)', overflowY: 'auto' }}>
          {models?.list.map((m) => {
            const apis = modelApiLabel(m);
            const meta = modelMeta(m);
            return (
              <div key={m.id} style={{ opacity: m.disabled ? 0.55 : 1 }}>
                <MobileCard
                  title={m.name || m.id}
                  subtitle={
                    <>
                      <Typography.Text type="secondary" style={{ wordBreak: 'break-all' }}>
                        {m.id}
                      </Typography.Text>
                      {apis ? (
                        <div style={{ marginTop: 4 }}>
                          <Tag color="blue" style={{ whiteSpace: 'normal', wordBreak: 'break-all' }}>
                            {apis}
                          </Tag>
                        </div>
                      ) : null}
                      {meta.map((line) => (
                        <div key={line} style={{ color: 'var(--gw-text-muted)', fontSize: 12 }}>
                          {line}
                        </div>
                      ))}
                    </>
                  }
                  tags={
                    <Space direction="vertical" align="end" size={4}>
                      {m.disabled ? <Tag style={{ marginInlineEnd: 0 }}>已禁用</Tag> : null}
                      <Switch
                        size="small"
                        checked={!m.disabled}
                        disabled={modelsBusy}
                        onChange={(checked) => void toggleModel(m, checked)}
                        aria-label={`切换 ${m.id}`}
                      />
                    </Space>
                  }
                />
              </div>
            );
          })}
          {models && models.list.length === 0 ? <Typography.Text type="secondary">供应商没有返回模型</Typography.Text> : null}
        </div>
      </Modal>
    </div>
  );
}
