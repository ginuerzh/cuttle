import { useState, type ReactNode } from 'react';
import { Alert, Button, Drawer, Modal, Popconfirm, Space, Switch, Table, Tag, Tooltip, Typography, message } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { deleteProvider, getProviderModels, testProvider, updateProvider } from '../api/client';
import type { ConfigResponse, Provider, ProviderModel } from '../api/types';
import { ProviderForm } from './Edit';
import {
  configuredAPIs,
  modelApiLabel,
  modelDirectionRows,
  normalizedModalities,
  providerPayload,
  toggleModelOverride,
} from './presets';
import { useIsMobile } from '../components/useIsMobile';
import { MobileCard } from '../components/MobileCard';

const MODALITY_ICONS: Record<string, { label: string; color: string; paths: ReactNode }> = {
  text: {
    label: '文本',
    color: '#0088fe',
    paths: (
      <>
        <path d="M12 4v16" />
        <path d="M4 7V5a1 1 0 0 1 1-1h14a1 1 0 0 1 1 1v2" />
        <path d="M9 20h6" />
      </>
    ),
  },
  file: {
    label: '文件',
    color: '#daa520',
    paths: (
      <>
        <path d="M6 22a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h8a2.4 2.4 0 0 1 1.704.706l3.588 3.588A2.4 2.4 0 0 1 20 8v12a2 2 0 0 1-2 2z" />
        <path d="M14 2v5a1 1 0 0 0 1 1h5" />
      </>
    ),
  },
  image: {
    label: '图像',
    color: '#2e8b57',
    paths: (
      <>
        <rect width="18" height="18" x="3" y="3" rx="2" ry="2" />
        <circle cx="9" cy="9" r="2" />
        <path d="m21 15-3.086-3.086a2 2 0 0 0-2.828 0L6 21" />
      </>
    ),
  },
  audio: {
    label: '音频',
    color: '#7b68ee',
    paths: (
      <>
        <path d="M2 10v3" />
        <path d="M6 6v11" />
        <path d="M10 3v18" />
        <path d="M14 8v7" />
        <path d="M18 5v13" />
        <path d="M22 10v3" />
      </>
    ),
  },
  video: {
    label: '视频',
    color: '#ff4500',
    paths: (
      <>
        <path d="m16 13 5.223 3.482a.5.5 0 0 0 .777-.416V7.87a.5.5 0 0 0-.752-.432L16 10.5" />
        <rect x="2" y="6" width="14" height="12" rx="2" />
      </>
    ),
  },
};

function DirectionRow({
  text,
  direction,
  values,
}: {
  text: string | null;
  direction: string;
  values?: string[];
}) {
  const kinds = normalizedModalities(values);
  if (!text && kinds.length === 0) return null;
  return (
    <div style={{ display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: 4, fontSize: 10, marginTop: 2 }}>
      {text ? <span>{text}</span> : null}
      {kinds.map((kind) => {
        const item = MODALITY_ICONS[kind];
        return item ? (
          <Tooltip key={kind} title={`${direction}：${item.label}`}>
            <span
              aria-label={`${direction} ${item.label}`}
              style={{
                display: 'inline-flex',
                alignItems: 'center',
                justifyContent: 'center',
                width: 16,
                height: 16,
                borderRadius: 4,
                color: item.color,
                background: `color-mix(in srgb, ${item.color} 12%, transparent)`,
              }}
            >
              <svg
                width="11"
                height="11"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                strokeWidth="2.5"
                strokeLinecap="round"
                strokeLinejoin="round"
                aria-hidden="true"
              >
                {item.paths}
              </svg>
            </span>
          </Tooltip>
        ) : null;
      })}
    </div>
  );
}

export function ProvidersPage({ config, onReload }: { config: ConfigResponse; onReload: () => Promise<void> }) {
  const isMobile = useIsMobile();
  const [editing, setEditing] = useState<Provider | null>(null);
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

  const setAllModelsEnabled = async (enabled: boolean) => {
    if (!models) return;
    const provider = config.providers.find((p) => p.name === models.name);
    if (!provider) {
      message.error(`未找到 Provider ${models.name}`);
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

  const anyEnabled = !!models && models.list.some((m) => !m.disabled);

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
      title: '启用',
      dataIndex: 'enabled',
      width: 70,
      render: (enabled: boolean | undefined, p) => (
        <Switch
          size="small"
          checked={enabled !== false}
          disabled={providerBusy === p.name}
          onChange={(checked) => void toggleProvider(p, checked)}
          aria-label={`切换 ${p.name}`}
        />
      ),
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
                <Space direction="vertical" align="end" size={2}>
                  <Switch
                    size="small"
                    checked={p.enabled !== false}
                    disabled={providerBusy === p.name}
                    onChange={(checked) => void toggleProvider(p, checked)}
                    aria-label={`切换 ${p.name}`}
                  />
                  {p.probe ? (
                    <Tag color={p.probe.ok ? 'green' : 'red'}>
                      {p.probe.ok ? `${p.probe.latencyMs}ms` : p.probe.error || 'fail'}
                    </Tag>
                  ) : (
                    <Tag>{p.keysSet} keys</Tag>
                  )}
                </Space>
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
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 12 }}>
            <Space size={4}>
              <Switch
                size="small"
                checked={anyEnabled}
                disabled={modelsBusy || !models?.list.length}
                onChange={(checked) => void setAllModelsEnabled(checked)}
                aria-label="全局启用"
              />
              <span style={{ fontSize: 12 }}>全局启用</span>
            </Space>
            <Button size="small" loading={modelsBusy} onClick={() => models && void showModels(models.name, true)}>
              刷新
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
              message="模型目录加载失败"
              description={modelsError}
              style={{ marginBottom: 12 }}
            />
          ) : null}
          {models?.list.map((m) => {
            const apis = modelApiLabel(m);
            const rows = modelDirectionRows(m);
            return (
              <div key={m.id} style={{ opacity: m.disabled ? 0.55 : 1 }}>
                <MobileCard
                  dense
                  title={m.name || m.id}
                  subtitle={
                    <>
                      <div style={{ display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: 4 }}>
                        <Typography.Text type="secondary" style={{ fontSize: 10, wordBreak: 'break-all' }}>
                          {m.id}
                        </Typography.Text>
                        {apis ? (
                          <Tag
                            color="blue"
                            style={{ marginInlineEnd: 0, fontSize: 10, lineHeight: '15px', paddingInline: 4 }}
                          >
                            {apis}
                          </Tag>
                        ) : null}
                      </div>
                      <DirectionRow text={rows.input} direction="输入" values={m.inputs} />
                      <DirectionRow text={rows.output} direction="输出" values={m.outputs} />
                    </>
                  }
                  tags={
                    <Space direction="vertical" align="end" size={2}>
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
          {models && models.list.length === 0 && !modelsError ? (
            <Typography.Text type="secondary">供应商没有返回模型</Typography.Text>
          ) : null}
        </div>
      </Modal>
    </div>
  );
}
