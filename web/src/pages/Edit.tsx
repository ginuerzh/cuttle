import { useEffect, useMemo, useState } from 'react';
import {
  Alert,
  AutoComplete,
  Button,
  Divider,
  Drawer,
  Grid,
  Form,
  Input,
  InputNumber,
  Modal,
  Select,
  Space,
  Switch,
  Tag,
  Typography,
  message,
} from 'antd';
import { PlusOutlined } from '@ant-design/icons';
import type { Connection } from '@xyflow/react';
import {
  createNode,
  createProvider,
  createRoute,
  deleteNode,
  deleteProvider,
  deleteRoute,
  getPresets,
  getProviderModels,
  putAliases,
  reorderRoutes,
  updateProvider,
  updateNode,
  updateRoute,
} from '../api/client';
import type { Alias, ConfigResponse, Node as BindNode, Preset, Provider, ProviderModel, Route } from '../api/types';
import { aliasFromNodeId, splitId } from './selection';
import {
  configuredAPIs,
  derivedTargetAPI,
  disabledTargets,
  enabledModels,
  genericCanonicalNote,
  isBaseURLLocked,
  modelOption,
  presetFormValues,
  providerPayload,
  PROVIDER_TYPES,
  unknownModels,
} from './presets';
import { MapField, mapToRows, rowsToMap, type MapRow } from '../components/MapFields';
import { Topology, type Selection } from './Topology';

interface Props {
  config: ConfigResponse;
  onReload: () => Promise<void>;
}

function logicalModels(config: ConfigResponse): string[] {
  return config.routes.map((r) => r.match?.model || '').filter(Boolean);
}

function errorText(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

/** Load a provider's model catalog; failures are non-blocking. */
function useProviderModels(providerName?: string) {
  const [models, setModels] = useState<ProviderModel[]>([]);
  const [error, setError] = useState('');

  useEffect(() => {
    let active = true;
    setModels([]);
    setError('');
    if (!providerName) return;
    getProviderModels(providerName)
      .then((list) => {
        if (active) setModels(list);
      })
      .catch((err) => {
        if (active) setError(errorText(err));
      });
    return () => {
      active = false;
    };
  }, [providerName]);

  return { models, error };
}

/** Warning shown below the model mapping field. */
function ModelCatalogHint({
  models,
  error,
  rows,
}: {
  models: ProviderModel[];
  error: string;
  rows: MapRow[] | undefined;
}) {
  const disabled = disabledTargets(rows, models);
  const missing = unknownModels(rows, models).filter((id) => !disabled.includes(id));
  return (
    <>
      {error ? (
        <Typography.Paragraph type="warning" style={{ fontSize: 12, marginBottom: 8 }}>
          无法加载模型目录：{error}（仍可手动输入）
        </Typography.Paragraph>
      ) : null}
      {disabled.length > 0 ? (
        <Alert
          type="warning"
          showIcon
          style={{ marginBottom: 8 }}
          message={`目标模型已在 Provider 中禁用，路由会跳过该 node：${disabled.join(', ')}`}
          description="可在 Provider 的模型列表中重新启用；禁用期间该映射不会参与路由。"
        />
      ) : null}
      {missing.length > 0 ? (
        <Alert
          type="warning"
          showIcon
          style={{ marginBottom: 8 }}
          message={`未知模型或 API 待定：${missing.join(', ')}`}
          description="目录中没有该模型，或目录尚未提供它的 API。仍可保存，网关会按默认 API 顺序解析。"
        />
      ) : null}
    </>
  );
}

/** Read-only APIs the gateway will use for each model mapping. */
function DerivedTargetAPIHint({
  provider,
  models,
  rows,
}: {
  provider?: Provider;
  models: ProviderModel[];
  rows: MapRow[] | undefined;
}) {
  if (!provider) return null;
  const apis = configuredAPIs(provider);
  const byID = new Map(models.map((m) => [m.id, m]));
  const targets = (rows ?? [])
    .map((row) => ({ logical: (row?.key ?? '').trim(), model: (row?.value ?? '').trim() }))
    .filter((t) => t.model)
    .map((t) => ({
      ...t,
      api: derivedTargetAPI(provider.type, apis, byID.get(t.model)?.apis),
      disabled: byID.get(t.model)?.disabled === true,
    }));
  const note = genericCanonicalNote(provider.type, apis);
  if (targets.length === 0 && !note) return null;
  return (
    <>
      {targets.length > 0 ? (
        <Typography.Paragraph type="secondary" style={{ fontSize: 12, marginBottom: 8 }}>
          目标 API：
          {targets
            .map(
              (t) =>
                `${t.logical || '?'} → ${t.model}: ${
                  t.api === 'unknown' ? '未知（无可用 base URL）' : t.api
                }${t.disabled ? '（已禁用）' : ''}`,
            )
            .join('；')}
        </Typography.Paragraph>
      ) : null}
      {note ? (
        <Typography.Paragraph type="warning" style={{ fontSize: 12, marginBottom: 8 }}>
          {note}
        </Typography.Paragraph>
      ) : null}
    </>
  );
}

// ---------------------------------------------------------------- forms

export function ProviderForm({ provider, onSaved }: { provider: Provider; onSaved: () => Promise<void> }) {
  const [busy, setBusy] = useState(false);
  const [form] = Form.useForm();

  const save = async () => {
    const v = await form.validateFields();
    setBusy(true);
    try {
      const payload = providerPayload(
        {
          ...provider,
          type: v.type,
          chat: String(v.chat ?? '').trim(),
          responses: String(v.responses ?? '').trim(),
          anthropic: String(v.anthropic ?? '').trim(),
          gemini: String(v.gemini ?? '').trim(),
          enabled: v.enabled,
          keys: undefined,
        },
        { models: provider.models ?? {} },
      );
      const keys = String(v.keys ?? '')
        .split('\n')
        .map((s) => s.trim())
        .filter(Boolean);
      if (keys.length > 0) payload.keys = keys;
      await updateProvider(provider.name, payload);
      message.success('已保存');
      await onSaved();
    } catch (err) {
      message.error(errorText(err));
    } finally {
      setBusy(false);
    }
  };

  const remove = async () => {
    setBusy(true);
    try {
      await deleteProvider(provider.name);
      message.success('已删除');
      await onSaved();
    } catch (err) {
      message.error(errorText(err));
    } finally {
      setBusy(false);
    }
  };

  const baseURLLocked = isBaseURLLocked(provider.type);

  return (
    <Form
      form={form}
      layout="vertical"
      size="small"
      initialValues={{
        type: provider.type || 'generic',
        chat: provider.chat ?? '',
        responses: provider.responses ?? '',
        anthropic: provider.anthropic ?? '',
        gemini: provider.gemini ?? '',
        keys: '',
        enabled: provider.enabled !== false,
      }}
    >
      <Typography.Title level={5} style={{ marginTop: 0 }}>
        {provider.name}
      </Typography.Title>
      <Tag>{`keys: ${provider.keysSet}`}</Tag>
      {provider.keyPreviews.map((k) => (
        <Tag key={k}>{k}</Tag>
      ))}
      <Divider />
      <Form.Item name="type" label="type" rules={[{ required: true }]} extra="创建后不可修改">
        <Select disabled options={PROVIDER_TYPES.map((v) => ({ value: v, label: v }))} />
      </Form.Item>
      {baseURLLocked ? (
        <Typography.Paragraph type="secondary" style={{ fontSize: 12, marginBottom: 8 }}>
          非 generic Provider 的 API base URL 创建后锁定，只有 generic 类型可以编辑；如需更换请删除后重建。
        </Typography.Paragraph>
      ) : null}
      <Form.Item
        name="chat"
        label="chat base URL"
        extra="OpenAI Chat Completions 端点，通常以 /v1 结尾；留空表示不提供。"
      >
        <Input disabled={baseURLLocked} placeholder="https://api.example.com/v1" />
      </Form.Item>
      <Form.Item
        name="responses"
        label="responses base URL"
        extra="OpenAI Responses 端点，通常以 /v1 结尾；留空表示不提供。"
      >
        <Input disabled={baseURLLocked} placeholder="https://api.example.com/v1" />
      </Form.Item>
      <Form.Item
        name="anthropic"
        label="anthropic base URL"
        extra="Anthropic Messages 端点，通常是根路径；留空表示不提供。"
      >
        <Input disabled={baseURLLocked} placeholder="https://api.example.com/anthropic" />
      </Form.Item>
      <Form.Item
        name="gemini"
        label="gemini base URL"
        extra="Gemini API 端点，通常以 /v1beta 结尾；留空表示不提供。"
      >
        <Input disabled={baseURLLocked} placeholder="https://generativelanguage.googleapis.com/v1beta" />
      </Form.Item>
      <Form.Item name="keys" label="keys（留空保持不变，每行一个）">
        <Input.TextArea rows={2} placeholder="sk-..." />
      </Form.Item>
      <Form.Item name="enabled" label="enabled" valuePropName="checked">
        <Switch size="small" />
      </Form.Item>
      <Typography.Paragraph type="secondary" style={{ fontSize: 12 }}>
        至少填写一个 API base URL。模型映射与 backup/weight 在 Node 上配置；同一个 Provider 可以被多个 Node 引用。
      </Typography.Paragraph>
      <Divider />
      <Space>
        <Button type="primary" loading={busy} onClick={save}>
          保存
        </Button>
        <Button danger loading={busy} onClick={remove}>
          删除
        </Button>
      </Space>
    </Form>
  );
}

export function RouteForm({ route, config, onSaved }: { route: Route; config: ConfigResponse; onSaved: () => Promise<void> }) {
  const [busy, setBusy] = useState(false);
  const [form] = Form.useForm();

  const save = async () => {
    const v = await form.validateFields();
    setBusy(true);
    try {
      await updateRoute(route.name, {
        name: route.name,
        nodes: v.nodes ?? [],
        strategy: v.strategy,
        match: { model: v.model || '' },
      });
      message.success('已保存');
      await onSaved();
    } catch (err) {
      message.error(errorText(err));
    } finally {
      setBusy(false);
    }
  };

  const remove = async () => {
    setBusy(true);
    try {
      await deleteRoute(route.name);
      message.success('已删除');
      await onSaved();
    } catch (err) {
      message.error(errorText(err));
    } finally {
      setBusy(false);
    }
  };

  const nodeOptions = config.nodes.map((n) => ({
    value: n.name,
    label: `${n.name} (${n.provider})`,
  }));

  return (
    <Form
      form={form}
      layout="vertical"
      size="small"
      initialValues={{
        model: route.match?.model ?? '',
        strategy: route.strategy ?? 'round',
        nodes: route.nodes,
      }}
    >
      <Typography.Title level={5} style={{ marginTop: 0 }}>
        {route.name}
      </Typography.Title>
      <Form.Item name="model" label="逻辑模型（空 = catch-all）">
        <Input />
      </Form.Item>
      <Form.Item name="strategy" label="strategy">
        <Select options={['round', 'fifo', 'random'].map((v) => ({ value: v, label: v }))} />
      </Form.Item>
      <Form.Item name="nodes" label="Nodes（顺序 = 优先级）">
        <Select mode="multiple" options={nodeOptions} />
      </Form.Item>
      <Divider />
      <Space>
        <Button type="primary" loading={busy} onClick={save}>
          保存
        </Button>
        <Button danger loading={busy} onClick={remove}>
          删除
        </Button>
      </Space>
    </Form>
  );
}


export function NodeForm({
  node,
  config,
  onSaved,
}: {
  node: BindNode;
  config: ConfigResponse;
  onSaved: () => Promise<void>;
}) {
  const [busy, setBusy] = useState(false);
  const [form] = Form.useForm();

  const save = async () => {
    const v = await form.validateFields();
    setBusy(true);
    try {
      await updateNode(node.name, {
        name: node.name,
        provider: v.provider,
        backup: v.backup,
        weight: v.weight,
        enabled: v.enabled,
        models: rowsToMap(v.models, '逻辑模型'),
      });
      message.success('已保存');
      await onSaved();
    } catch (err) {
      message.error(errorText(err));
    } finally {
      setBusy(false);
    }
  };

  const remove = async () => {
    setBusy(true);
    try {
      await deleteNode(node.name);
      message.success('已删除');
      await onSaved();
    } catch (err) {
      message.error(errorText(err));
    } finally {
      setBusy(false);
    }
  };

  const providerOptions = config.providers.map((p) => ({ value: p.name, label: p.name }));
  const providerName = Form.useWatch('provider', form) as string | undefined;
  const provider = config.providers.find((p) => p.name === providerName);
  const { models, error: modelsError } = useProviderModels(providerName);
  const modelRows = Form.useWatch('models', form) as MapRow[] | undefined;

  return (
    <Form
      form={form}
      layout="vertical"
      size="small"
      initialValues={{
        provider: node.provider,
        models: mapToRows(node.models),
        backup: node.backup,
        weight: node.weight ?? 1,
        enabled: node.enabled !== false,
      }}
    >
      <Typography.Title level={5} style={{ marginTop: 0 }}>
        {node.name}
      </Typography.Title>
      <Tag color="cyan">node</Tag>
      <Divider />
      <Form.Item name="provider" label="provider" rules={[{ required: true }]}>
        <Select options={providerOptions} />
      </Form.Item>
      <MapField
        name="models"
        label="models（逻辑模型 → 供应商模型）"
        hint='用 * 作为兜底；未命中的逻辑模型会走 * 或直接透传。'
        keyPlaceholder="快速 / *"
        valuePlaceholder="deepseek-v4-flash"
        suggestions={[...logicalModels(config), '*']}
        valueOptions={enabledModels(models).map(modelOption)}
        addLabel="添加模型映射"
      />
      <DerivedTargetAPIHint provider={provider} models={models} rows={modelRows} />
      <ModelCatalogHint models={models} error={modelsError} rows={modelRows} />
      <Space>
        <Form.Item name="backup" label="backup" valuePropName="checked" noStyle>
          <Switch size="small" />
        </Form.Item>
        <Form.Item name="enabled" label="enabled" valuePropName="checked" noStyle>
          <Switch size="small" />
        </Form.Item>
        <Form.Item name="weight" label="weight" noStyle>
          <InputNumber min={0} size="small" style={{ width: 70 }} />
        </Form.Item>
      </Space>
      <Divider />
      <Space>
        <Button type="primary" loading={busy} onClick={save}>
          保存
        </Button>
        <Button danger loading={busy} onClick={remove}>
          删除
        </Button>
      </Space>
    </Form>
  );
}

export function AliasForm({
  alias,
  index,
  config,
  onSaved,
}: {
  alias: Alias;
  index: number;
  config: ConfigResponse;
  onSaved: () => Promise<void>;
}) {
  const [busy, setBusy] = useState(false);
  const [form] = Form.useForm();
  const models = logicalModels(config);

  const save = async () => {
    const v = await form.validateFields();
    setBusy(true);
    try {
      const next = config.aliases.map((a, i) => (i === index ? { match: v.match, model: v.model } : a));
      await putAliases(next);
      message.success('已保存');
      await onSaved();
    } catch (err) {
      message.error(errorText(err));
    } finally {
      setBusy(false);
    }
  };

  const remove = async () => {
    setBusy(true);
    try {
      await putAliases(config.aliases.filter((_, i) => i !== index));
      message.success('已删除');
      await onSaved();
    } catch (err) {
      message.error(errorText(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Form
      form={form}
      layout="vertical"
      size="small"
      initialValues={{ match: alias.match, model: alias.model }}
    >
      <Typography.Title level={5} style={{ marginTop: 0 }}>
        alias #{index}
      </Typography.Title>
      <Form.Item name="match" label="客户端模型 glob" rules={[{ required: true }]}>
        <Input />
      </Form.Item>
      <Form.Item name="model" label="逻辑模型" rules={[{ required: true }]}>
        <AutoComplete options={models.map((m) => ({ value: m }))} placeholder="strong / fast" />
      </Form.Item>
      <Divider />
      <Space>
        <Button type="primary" loading={busy} onClick={save}>
          保存
        </Button>
        <Button danger loading={busy} onClick={remove}>
          删除
        </Button>
      </Space>
    </Form>
  );
}

function EdgeForm({ id, config, onSaved }: { id: string; config: ConfigResponse; onSaved: () => Promise<void> }) {
  const [busy, setBusy] = useState(false);
  if (id.startsWith('alias:')) {
    return (
      <div>
        <Typography.Title level={5} style={{ marginTop: 0 }}>
          别名连线
        </Typography.Title>
        <Typography.Paragraph type="secondary">
          别名连到哪条逻辑模型由 alias 的 model 决定，请在 Alias 面板里修改。
        </Typography.Paragraph>
      </div>
    );
  }
  const parts = id.split('|');
  const source = parts[0] ?? '';
  const target = parts[1] ?? '';

  if (source.startsWith('route:') && target.startsWith('node:')) {
    const routeName = source.slice('route:'.length);
    const nodeName = target.slice('node:'.length);
    const route = config.routes.find((r) => r.name === routeName);
    const node = config.nodes.find((n) => n.name === nodeName);

    const remove = async () => {
      if (!route) return;
      setBusy(true);
      try {
        await updateRoute(route.name, { ...route, nodes: route.nodes.filter((n) => n !== nodeName) });
        message.success('已断开');
        await onSaved();
      } catch (err) {
        message.error(errorText(err));
      } finally {
        setBusy(false);
      }
    };

    return (
      <div>
        <Typography.Title level={5} style={{ marginTop: 0 }}>
          连线
        </Typography.Title>
        <Typography.Paragraph>
          {routeName} → {nodeName}
          {node ? `（${node.provider}）` : ''}
        </Typography.Paragraph>
        <Typography.Paragraph type="secondary" style={{ fontSize: 12 }}>
          模型映射和 backup/weight 都在 Node 面板里改。
        </Typography.Paragraph>
        <Button danger loading={busy} onClick={remove} disabled={!route}>
          断开
        </Button>
      </div>
    );
  }

  if (source.startsWith('node:') && target.startsWith('provider:')) {
    const nodeName = source.slice('node:'.length);
    const providerName = target.slice('provider:'.length);
    return (
      <div>
        <Typography.Title level={5} style={{ marginTop: 0 }}>
          连线
        </Typography.Title>
        <Typography.Paragraph>
          {nodeName} → {providerName}
        </Typography.Paragraph>
        <Typography.Paragraph type="secondary" style={{ fontSize: 12 }}>
          Node 指向哪个 Provider 由 Node 的 provider 字段决定，请在 Node 面板修改。
        </Typography.Paragraph>
      </div>
    );
  }

  return (
    <div>
      <Typography.Title level={5} style={{ marginTop: 0 }}>
        连线
      </Typography.Title>
      <Typography.Paragraph type="secondary">这条连线不可直接编辑。</Typography.Paragraph>
    </div>
  );
}

// ---------------------------------------------------------------- page

export function Edit({ config, onReload }: Props) {
  const isMobile = !Grid.useBreakpoint().md;
  const [selection, setSelection] = useState<Selection>(null);
  const [addOpen, setAddOpen] = useState<null | 'provider' | 'node' | 'route' | 'alias'>(null);
  const [presets, setPresets] = useState<Preset[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');

  const openAdd = async (kind: 'provider' | 'node' | 'route' | 'alias') => {
    setError('');
    if (kind === 'provider' && presets.length === 0) {
      try {
        setPresets(await getPresets());
      } catch (err) {
        setError(errorText(err));
      }
    }
    setAddOpen(kind);
  };

  const connect = async (c: Connection) => {
    if (!c.source || !c.target) return;
    const [skind, sname] = splitId(c.source);
    const [tkind, tname] = splitId(c.target);
    setError('');
    setBusy(true);
    try {
      if (skind === 'route' && tkind === 'node') {
        const route = config.routes.find((r) => r.name === sname);
        if (!route) throw new Error('route not found');
        if (!route.nodes.includes(tname)) {
          await updateRoute(route.name, { ...route, nodes: [...route.nodes, tname] });
        }
      } else if (skind === 'alias' && tkind === 'route') {
        const found = aliasFromNodeId(config.aliases, c.source);
        if (!found) throw new Error('alias not found');
        const index = found.index;
        const target = config.routes.find((r) => r.name === tname);
        if (!target?.match?.model) throw new Error('catch-all 路由不能作为别名目标');
        const next = config.aliases.map((a, i) => (i === index ? { ...a, model: target.match.model! } : a));
        await putAliases(next);
      } else {
        throw new Error('不支持的连接方向');
      }
      await onReload();
    } catch (err) {
      setError(errorText(err));
    } finally {
      setBusy(false);
    }
  };

  const panel = useMemo(() => {
    if (!selection) return <Typography.Text type="secondary">选择节点或连线开始编辑</Typography.Text>;
    if (selection.kind === 'edge') {
      return <EdgeForm id={selection.id} config={config} onSaved={onReload} />;
    }
    const [kind, name] = splitId(selection.id);
    if (kind === 'provider') {
      const p = config.providers.find((x) => x.name === name);
      return p ? <ProviderForm key={p.name} provider={p} onSaved={onReload} /> : null;
    }
    if (kind === 'route') {
      const r = config.routes.find((x) => x.name === name);
      return r ? <RouteForm key={r.name} route={r} config={config} onSaved={onReload} /> : null;
    }
    if (kind === 'node') {
      const n = config.nodes.find((x) => x.name === name);
      return n ? <NodeForm key={n.name} node={n} config={config} onSaved={onReload} /> : null;
    }
    if (kind === 'alias') {
      const found = aliasFromNodeId(config.aliases, selection.id);
      return found ? (
        <AliasForm
          key={found.index}
          alias={found.alias}
          index={found.index}
          config={config}
          onSaved={onReload}
        />
      ) : null;
    }
    return null;
  }, [selection, config, onReload]);

  return (
    <div style={{ display: 'flex', width: '100%', height: '100%' }}>
      <div style={{ flex: 1, minWidth: 0, position: 'relative' }}>
        <div style={{ position: 'absolute', top: 12, left: 12, zIndex: 5 }}>
          <Space wrap>
            <Button icon={<PlusOutlined />} onClick={() => void openAdd('provider')}>
              Provider
            </Button>
            <Button icon={<PlusOutlined />} onClick={() => void openAdd('node')}>
              Node
            </Button>
            <Button icon={<PlusOutlined />} onClick={() => void openAdd('route')}>
              Route
            </Button>
            <Button icon={<PlusOutlined />} onClick={() => void openAdd('alias')}>
              Alias
            </Button>
            {busy ? <Tag color="processing">应用中…</Tag> : null}
          </Space>
          {error ? (
            <Alert style={{ marginTop: 8, maxWidth: 520 }} type="error" message={error} showIcon closable onClose={() => setError('')} />
          ) : null}
        </div>
        <Topology config={config} metrics={null} onSelect={setSelection} editable onConnect={connect} />
      </div>

      {isMobile ? (
        <Drawer
          placement="right"
          open={!!selection}
          onClose={() => setSelection(null)}
          width="92%"
          styles={{ body: { padding: 16 } }}
        >
          {panel}
        </Drawer>
      ) : (
        <aside
          style={{
            width: 340,
            borderLeft: '1px solid #e2e8f0',
            background: '#fff',
            padding: 16,
            overflow: 'auto',
          }}
        >
          {panel}
        </aside>
      )}

      <AddProviderModal
        open={addOpen === 'provider'}
        presets={presets}
        onClose={() => setAddOpen(null)}
        onDone={async () => {
          setAddOpen(null);
          await onReload();
        }}
        onError={setError}
      />
      <AddNodeModal
        open={addOpen === 'node'}
        config={config}
        onClose={() => setAddOpen(null)}
        onDone={async () => {
          setAddOpen(null);
          await onReload();
        }}
        onError={setError}
      />
      <AddRouteModal
        open={addOpen === 'route'}
        config={config}
        onClose={() => setAddOpen(null)}
        onDone={async () => {
          setAddOpen(null);
          await onReload();
        }}
        onError={setError}
      />
      <AddAliasModal
        open={addOpen === 'alias'}
        config={config}
        onClose={() => setAddOpen(null)}
        onDone={async () => {
          setAddOpen(null);
          await onReload();
        }}
        onError={setError}
      />
    </div>
  );
}

// ---------------------------------------------------------------- add modals

function AddProviderModal({
  open,
  presets,
  onClose,
  onDone,
  onError,
}: {
  open: boolean;
  presets: Preset[];
  onClose: () => void;
  onDone: () => Promise<void>;
  onError: (s: string) => void;
}) {
  const [form] = Form.useForm();
  const [busy, setBusy] = useState(false);

  const submit = async () => {
    const v = await form.validateFields();
    setBusy(true);
    try {
      const keys = String(v.keys ?? '')
        .split('\n')
        .map((s) => s.trim())
        .filter(Boolean);
      await createProvider({
        name: v.name,
        type: v.type,
        chat: String(v.chat ?? '').trim(),
        responses: String(v.responses ?? '').trim(),
        anthropic: String(v.anthropic ?? '').trim(),
        gemini: String(v.gemini ?? '').trim(),
        keys,
        models: {},
      });
      form.resetFields();
      await onDone();
    } catch (err) {
      onError(errorText(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal open={open} title="添加 Provider" onCancel={onClose} onOk={submit} confirmLoading={busy} destroyOnClose>
      <Form form={form} layout="vertical" size="small" initialValues={{ type: 'generic' }}>
        <Form.Item name="preset" label="预设">
          <Select
            allowClear
            options={presets.map((p) => ({ value: p.name, label: `${p.name} (${p.type})` }))}
            placeholder="可选"
            onChange={(name?: string) => {
              const p = presets.find((x) => x.name === name);
              if (!p) return;
              form.setFieldsValue(presetFormValues(p));
              void form.validateFields(['name', 'type', 'chat', 'responses', 'anthropic', 'gemini']).catch(() => undefined);
            }}
          />
        </Form.Item>
        <Form.Item name="name" label="名称" rules={[{ required: true }]}>
          <Input placeholder="opencode-go" />
        </Form.Item>
        <Form.Item name="type" label="type" rules={[{ required: true }]}>
          <Select options={PROVIDER_TYPES.map((v) => ({ value: v, label: v }))} />
        </Form.Item>
        <Form.Item name="chat" label="chat base URL" extra="留空表示不提供该 API。">
          <Input placeholder="https://api.example.com/v1" />
        </Form.Item>
        <Form.Item name="responses" label="responses base URL" extra="留空表示不提供该 API。">
          <Input placeholder="https://api.example.com/v1" />
        </Form.Item>
        <Form.Item name="anthropic" label="anthropic base URL" extra="留空表示不提供该 API。">
          <Input placeholder="https://api.example.com/anthropic" />
        </Form.Item>
        <Form.Item name="gemini" label="gemini base URL" extra="留空表示不提供该 API。">
          <Input placeholder="https://generativelanguage.googleapis.com/v1beta" />
        </Form.Item>
        <Form.Item
          noStyle
          shouldUpdate={(prev, cur) =>
            (['chat', 'responses', 'anthropic', 'gemini'] as const).some((k) => prev[k] !== cur[k])
          }
        >
          {({ getFieldsValue }) => {
            const values = getFieldsValue() as Partial<Provider>;
            const apis = configuredAPIs(values);
            return (
              <Form.Item label="已配置 API">
                {apis.length > 0 ? (
                  <Space wrap size={2}>
                    {apis.map((api) => (
                      <Tag key={api} color="blue">
                        {api}
                      </Tag>
                    ))}
                  </Space>
                ) : (
                  <Typography.Text type="warning">至少填写一个 API base URL</Typography.Text>
                )}
              </Form.Item>
            );
          }}
        </Form.Item>
        <Form.Item name="keys" label="keys（每行一个）">
          <Input.TextArea rows={2} />
        </Form.Item>
        <Typography.Paragraph type="secondary" style={{ fontSize: 12 }}>
          创建后在画布上为它建 Node，再在 Node 上配置模型映射。type 与 API base URL 创建后锁定（generic 的 base URL 除外）。
        </Typography.Paragraph>
      </Form>
    </Modal>
  );
}


function AddNodeModal({
  open,
  config,
  onClose,
  onDone,
  onError,
}: {
  open: boolean;
  config: ConfigResponse;
  onClose: () => void;
  onDone: () => Promise<void>;
  onError: (s: string) => void;
}) {
  const [form] = Form.useForm();
  const [busy, setBusy] = useState(false);

  const submit = async () => {
    const v = await form.validateFields();
    setBusy(true);
    try {
      await createNode({
        name: v.name,
        provider: v.provider,
        models: rowsToMap(v.models, '逻辑模型'),
      });
      form.resetFields();
      await onDone();
    } catch (err) {
      onError(errorText(err));
    } finally {
      setBusy(false);
    }
  };

  const providerOptions = config.providers.map((p) => ({ value: p.name, label: p.name }));
  const providerName = Form.useWatch('provider', form) as string | undefined;
  const provider = config.providers.find((p) => p.name === providerName);
  const { models, error: modelsError } = useProviderModels(providerName);
  const modelRows = Form.useWatch('models', form) as MapRow[] | undefined;

  return (
    <Modal open={open} title="添加 Node" onCancel={onClose} onOk={submit} confirmLoading={busy} destroyOnClose>
      <Form form={form} layout="vertical" size="small" initialValues={{ models: [] }}>
        <Form.Item name="name" label="名称" rules={[{ required: true }]}>
          <Input placeholder="deepseek-fast" />
        </Form.Item>
        <Form.Item name="provider" label="provider" rules={[{ required: true }]}>
          <Select options={providerOptions} />
        </Form.Item>
        <MapField
          name="models"
          label="models（逻辑模型 → 供应商模型）"
          hint="用 * 作为兜底。"
          keyPlaceholder="fast / *"
          valuePlaceholder="deepseek-v4-flash"
          suggestions={[...logicalModels(config), '*']}
          valueOptions={enabledModels(models).map(modelOption)}
          addLabel="添加模型映射"
        />
        <DerivedTargetAPIHint provider={provider} models={models} rows={modelRows} />
        <ModelCatalogHint models={models} error={modelsError} rows={modelRows} />
      </Form>
    </Modal>
  );
}

function AddRouteModal({
  open,
  config,
  onClose,
  onDone,
  onError,
}: {
  open: boolean;
  config: ConfigResponse;
  onClose: () => void;
  onDone: () => Promise<void>;
  onError: (s: string) => void;
}) {
  const [form] = Form.useForm();
  const [busy, setBusy] = useState(false);
  const options = config.nodes.map((n) => ({ value: n.name, label: `${n.name} (${n.provider})` }));

  const submit = async () => {
    const v = await form.validateFields();
    setBusy(true);
    try {
      await createRoute({
        name: v.name,
        nodes: v.nodes ?? [],
        strategy: v.strategy,
        match: { model: v.model || '' },
      });
      // new routes append after catch-all; move it before the first catch-all
      const names = config.routes.map((r) => r.name);
      const catchAll = config.routes.findIndex((r) => !r.match?.model);
      names.splice(catchAll < 0 ? names.length : catchAll, 0, v.name);
      await reorderRoutes(names);
      form.resetFields();
      await onDone();
    } catch (err) {
      onError(errorText(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal open={open} title="添加 Route" onCancel={onClose} onOk={submit} confirmLoading={busy} destroyOnClose>
      <Form form={form} layout="vertical" size="small">
        <Form.Item name="name" label="名称" rules={[{ required: true }]}>
          <Input placeholder="strong" />
        </Form.Item>
        <Form.Item name="model" label="逻辑模型（alias 的 model 指向它）">
          <Input placeholder="strong" />
        </Form.Item>
        <Form.Item name="strategy" label="strategy" initialValue="round">
          <Select options={['round', 'fifo', 'random'].map((v) => ({ value: v, label: v }))} />
        </Form.Item>
        <Form.Item name="nodes" label="Nodes（顺序 = 优先级）">
          <Select mode="multiple" options={options} />
        </Form.Item>
      </Form>
    </Modal>
  );
}

function AddAliasModal({
  open,
  config,
  onClose,
  onDone,
  onError,
}: {
  open: boolean;
  config: ConfigResponse;
  onClose: () => void;
  onDone: () => Promise<void>;
  onError: (s: string) => void;
}) {
  const [form] = Form.useForm();
  const [busy, setBusy] = useState(false);
  const models = logicalModels(config);

  const submit = async () => {
    const v = await form.validateFields();
    setBusy(true);
    try {
      await putAliases([...config.aliases, { match: v.match, model: v.model }]);
      form.resetFields();
      await onDone();
    } catch (err) {
      onError(errorText(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal open={open} title="添加 Alias" onCancel={onClose} onOk={submit} confirmLoading={busy} destroyOnClose>
      <Form form={form} layout="vertical" size="small">
        <Form.Item name="match" label="客户端模型 glob" rules={[{ required: true }]}>
          <Input placeholder="claude-opus*" />
        </Form.Item>
        <Form.Item name="model" label="逻辑模型" rules={[{ required: true }]}>
          <AutoComplete options={models.map((m) => ({ value: m }))} placeholder="strong" />
        </Form.Item>
      </Form>
    </Modal>
  );
}
