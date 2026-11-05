import { useState } from 'react';
import { Alert, Button, Drawer, Modal, Popconfirm, Space, Switch, Table, Tag, Typography, message } from 'antd';
import { PlusOutlined } from '@ant-design/icons';
import type { ColumnsType } from 'antd/es/table';
import { deleteProvider, getPresets, getProviderModels, testProvider, updateProvider } from '../api/client';
import type { ConfigResponse, Preset, Provider, ProviderModel } from '../api/types';
import { translate as t } from '../i18n';
import { AddProviderModal, ProviderForm } from './Edit';
import {
  configuredAPIs,
  providerPayload,
  toggleModelOverride,
} from './presets';
import { useIsMobile } from '../components/useIsMobile';
import { MobileCard } from '../components/MobileCard';
import { ModelCard } from '../components/ModelCard';

export function ProvidersPage({ config, onReload }: { config: ConfigResponse; onReload: () => Promise<void> }) {
  const isMobile = useIsMobile();
  const [editing, setEditing] = useState<Provider | null>(null);
  const [addOpen, setAddOpen] = useState(false);
  const [presets, setPresets] = useState<Preset[]>([]);
  const [models, setModels] = useState<{ name: string; list: ProviderModel[] } | null>(null);
  const [modelsError, setModelsError] = useState('');
  const [modelsBusy, setModelsBusy] = useState(false);
  const [providerBusy, setProviderBusy] = useState('');

  const test = async (name: string) => {
    try {
      const r = await testProvider(name);
      if (r.ok) message.success(`${name}: ${r.latencyMs}ms`);
      else message.error(`${name}: ${r.error || `status ${r.status}`}`);
    } catch (err) {
      message.error(err instanceof Error ? err.message : String(err));
    }
  };

  const toggleProvider = async (provider: Provider, enabled: boolean) => {
    setProviderBusy(provider.name);
    try {
      await updateProvider(provider.name, providerPayload({ ...provider, enabled }));
      await onReload();
    } catch (err) {
      message.error(err instanceof Error ? err.message : String(err));
    } finally {
      setProviderBusy('');
    }
  };

  const openAdd = async () => {
    try {
      setPresets(await getPresets());
    } catch (err) {
      message.error(err instanceof Error ? err.message : String(err));
    }
    setAddOpen(true);
  };

  const showModels = async (name: string, refresh = false) => {
    setModelsBusy(true);
    setModelsError('');
    setModels((cur) => (cur && cur.name === name ? cur : { name, list: [] }));
    try {
      setModels({ name, list: await getProviderModels(name, refresh) });
    } catch (err) {
      const text = err instanceof Error ? err.message : String(err);
      setModelsError(text);
      message.error(text);
    } finally {
      setModelsBusy(false);
    }
  };

  const toggleModel = async (model: ProviderModel, checked: boolean) => {
    if (!models) return;
    const provider = config.providers.find((p) => p.name === models.name);
    if (!provider) {
      message.error(t('未找到 Provider {name}', { name: models.name }));
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

  const setAllModelsEnabled = async (enabled: boolean) => {
    if (!models) return;
    const provider = config.providers.find((p) => p.name === models.name);
    if (!provider) {
      message.error(t('未找到 Provider {name}', { name: models.name }));
      return;
    }
    const next = enabled
      ? {}
      : Object.fromEntries(models.list.map((m) => [m.id, { disabled: true }]));
    setModelsBusy(true);
    try {
      await updateProvider(provider.name, providerPayload(provider, { models: next }));
      setModels((cur) =>
        cur && cur.name === provider.name
          ? { ...cur, list: cur.list.map((m) => ({ ...m, disabled: !enabled })) }
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
      message.success(t('已删除'));
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
          <Tag key={api} color="blue">{api}</Tag>
        ))}
      </Space>
    );
  };

  const anyEnabled = !!models && models.list.some((m) => !m.disabled);

  const columns: ColumnsType<Provider> = [
    { title: t('名称'), dataIndex: 'name', width: 180, render: (v) => <Typography.Text strong>{v}</Typography.Text> },
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
      title: t('探测'),
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
      title: t('启用'),
      dataIndex: 'enabled',
      width: 70,
      render: (enabled: boolean | undefined, p) => (
        <Switch
          size="small"
          checked={enabled !== false}
          disabled={providerBusy === p.name}
          onChange={(checked) => void toggleProvider(p, checked)}
          aria-label={t('切换 {name}', { name: p.name })}
        />
      ),
    },
    {
      title: t('操作'),
      width: 230,
      render: (_, p) => (
        <Space wrap size={4}>
          <Button size="small" onClick={() => void test(p.name)}>
            {t('测试')}
          </Button>
          <Button size="small" onClick={() => void showModels(p.name)}>
            {t('模型')}
          </Button>
          <Button size="small" onClick={() => setEditing(p)}>
            {t('编辑')}
          </Button>
          <Popconfirm title={t('删除 {name}?', { name: p.name })} onConfirm={() => void remove(p.name)}>
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
          Providers
        </Typography.Title>
        <Button type="primary" icon={<PlusOutlined />} onClick={() => void openAdd()}>
          {t('添加')}
        </Button>
      </Space>
      {isMobile ? (
        <div>
          {config.providers.map((p) => (
            <MobileCard
              key={p.name}
              title={p.name}
              subtitle={
                <>
                  <span>{p.type}</span>
                  <div style={{ marginTop: 2 }}>{apiTags(p)}</div>
                </>
              }
              tagsAlign="center"
              tags={
                <Space direction="vertical" align="end" size={2}>
                  {p.probe ? (
                    <Tag color={p.probe.ok ? 'green' : 'red'} style={{ marginInlineEnd: 0 }}>
                      {p.probe.ok ? `${p.probe.latencyMs}ms` : p.probe.error || 'fail'}
                    </Tag>
                  ) : (
                    <Tag style={{ marginInlineEnd: 0 }}>{p.keysSet} keys</Tag>
                  )}
                  <Switch
                    size="small"
                    checked={p.enabled !== false}
                    disabled={providerBusy === p.name}
                    onChange={(checked) => void toggleProvider(p, checked)}
                    aria-label={t('切换 {name}', { name: p.name })}
                  />
                </Space>
              }
              actions={
                <>
                  <Button size="small" onClick={() => void test(p.name)}>{t('测试')}</Button>
                  <Button size="small" onClick={() => void showModels(p.name)}>{t('模型')}</Button>
                  <Button size="small" onClick={() => setEditing(p)}>{t('编辑')}</Button>
                  <Popconfirm title={t('删除 {name}?', { name: p.name })} onConfirm={() => void remove(p.name)}>
                    <Button size="small" danger>{t('删除')}</Button>
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
        title={editing ? t('编辑 {name}', { name: editing.name }) : ''}
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
        title={models ? t('{name} 的模型', { name: models.name }) : ''}
        open={!!models}
        width={isMobile ? '94%' : 640}
        footer={
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 12 }}>
            <Space size={4}>
              <Switch
                size="small"
                checked={anyEnabled}
                disabled={modelsBusy || !models?.list.length}
                onChange={(checked) => void setAllModelsEnabled(checked)}
                aria-label={t('全局启用')}
              />
              <span style={{ fontSize: 12 }}>{t('全局启用')}</span>
            </Space>
            <Button size="small" loading={modelsBusy} onClick={() => models && void showModels(models.name, true)}>
              {t('刷新')}
            </Button>
          </div>
        }
        onCancel={() => setModels(null)}
        styles={{
          content: { padding: 0 },
          header: { padding: '16px 24px 0' },
          body: { padding: 0, maxHeight: 'min(480px, 60dvh)', overflowY: 'auto' },
          footer: { padding: '0 24px 16px' },
        }}
      >
        <div style={{ padding: '12px 24px 16px' }}>
          {modelsError ? (
            <Alert
              type="error"
              showIcon
              message={t('模型目录加载失败')}
              description={modelsError}
              style={{ marginBottom: 12 }}
            />
          ) : null}
          {models?.list.map((m) => (
            <div key={m.id} style={{ opacity: m.disabled ? 0.55 : 1 }}>
              <ModelCard
                model={m}
                tags={
                  <Switch
                    size="small"
                    checked={!m.disabled}
                    disabled={modelsBusy}
                    onChange={(checked) => void toggleModel(m, checked)}
                    aria-label={t('切换 {name}', { name: m.id })}
                  />
                }
              />
            </div>
          ))}
          {models && models.list.length === 0 && !modelsError ? (
            <Typography.Text type="secondary">{t('供应商没有返回模型')}</Typography.Text>
          ) : null}
        </div>
      </Modal>

      <AddProviderModal
        open={addOpen}
        presets={presets}
        onClose={() => setAddOpen(false)}
        onDone={async () => {
          setAddOpen(false);
          await onReload();
        }}
        onError={(s) => message.error(s)}
      />
    </div>
  );
}
