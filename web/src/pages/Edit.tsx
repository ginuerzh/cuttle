import { useEffect, useMemo, useState } from 'react';
import {
  Alert,
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
import { HolderOutlined, MinusCircleOutlined, PlusOutlined, SearchOutlined } from '@ant-design/icons';
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
  putEntrypoints,
  updateProvider,
  updateNode,
  updateRoute,
} from '../api/client';
import type { ConfigResponse, Entrypoint, Node as BindNode, Preset, Provider, ProviderApi, ProviderModel, Route, RouteHealth } from '../api/types';
import { translate as tr } from '../i18n';
import { entrypointFromNodeId, splitId } from './selection';
import { addRouteNode, reorderNodes } from './routeNodes';
import {
  configuredAPIs,
  derivedTargetAPI,
  disabledTargets,
  enabledModels,
  genericCanonicalNote,
  headersText,
  isBaseURLLocked,
  nodeAPIOptions,
  parseHeadersText,
  presetFormValues,
  providerKeyRows,
  providerKeyUpdates,
  providerPayload,
  type ProviderKeyRow,
  PROVIDER_TYPES,
  unknownModels,
} from './presets';
import { ModelCard } from '../components/ModelCard';
import { Topology, type Selection } from './Topology';

interface Props {
  config: ConfigResponse;
  onReload: () => Promise<void>;
}


function errorText(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

/** Per-key editor: existing keys stay masked; blank input keeps them. */
function ProviderKeyEditor({
  rows,
  onChange,
  onDelete,
  onAdd,
}: {
  rows: ProviderKeyRow[];
  onChange: (index: number, value: string) => void;
  onDelete: (index: number) => void;
  onAdd: () => void;
}) {
  return (
    <Form.Item
      label="keys"
      extra={tr('已有 key 只读显示掩码；如需更换请删除后添加新 key。')}
      style={{ marginBottom: 12 }}
    >
      {rows.map((row, i) => (
        <div key={`${row.index ?? 'new'}-${i}`} style={{ display: 'flex', gap: 4, marginTop: 6, alignItems: 'center' }}>
          {row.index !== undefined ? (
            <Tag style={{ flex: 1, marginInlineEnd: 0 }}>{row.preview || '****'}</Tag>
          ) : (
            <Input.Password
              value={row.value}
              placeholder="sk-..."
              onChange={(e) => onChange(i, e.target.value)}
            />
          )}
          <Button
            type="text"
            size="small"
            icon={<MinusCircleOutlined />}
            onClick={() => onDelete(i)}
            aria-label={tr('删除 key')}
          />
        </div>
      ))}
      <Button type="dashed" size="small" block icon={<PlusOutlined />} style={{ marginTop: 6 }} onClick={onAdd}>
        {tr('添加 key')}
      </Button>
    </Form.Item>
  );
}

/** One input per provider key; blank rows are ignored on save. */
function KeyField({ label, extra }: { label: string; extra?: string }) {
  return (
    <Form.Item label={label} extra={extra} style={{ marginBottom: 12 }}>
      <Form.List name="keys">
        {(fields, { add, remove }) => (
          <>
            {fields.map((field) => (
              <div key={field.key} style={{ display: 'flex', gap: 4, marginTop: 4, alignItems: 'center' }}>
                <Form.Item name={field.name} noStyle>
                  <Input.Password placeholder="sk-..." />
                </Form.Item>
                <Button
                  type="text"
                  size="small"
                  icon={<MinusCircleOutlined />}
                  onClick={() => remove(field.name)}
                  aria-label={tr('删除 key')}
                />
              </div>
            ))}
            <Button
              type="dashed"
              size="small"
              block
              icon={<PlusOutlined />}
              style={{ marginTop: 6 }}
              onClick={() => add('')}
            >
              {tr('添加 key')}
            </Button>
          </>
        )}
      </Form.List>
    </Form.Item>
  );
}

/**
 * One provider model per node. The value stays editable, but the picker opens
 * the same card list used by the Provider model modal.
 */
function ModelField({
  models,
  modelsError,
}: {
  models: ProviderModel[];
  modelsError: string;
}) {
  const form = Form.useFormInstance();
  const isMobile = !Grid.useBreakpoint().md;
  const [picker, setPicker] = useState(false);
  const [query, setQuery] = useState('');
  const choices = enabledModels(models).filter((m) => {
    const q = query.trim().toLowerCase();
    return !q || m.id.toLowerCase().includes(q) || (m.name ?? '').toLowerCase().includes(q);
  });
  const close = () => {
    setPicker(false);
    setQuery('');
  };
  const choose = (model: ProviderModel) => {
    form.setFieldValue('model', model.id);
    close();
  };

  return (
    <>
      <Form.Item label={tr('模型 ID')} required style={{ marginBottom: 12 }}>
        <Space.Compact style={{ width: '100%' }}>
          <Form.Item name="model" noStyle rules={[{ required: true, message: tr('请填写模型') }]}>
            <Input placeholder="deepseek-v4-flash" />
          </Form.Item>
          <Button
            icon={<SearchOutlined />}
            onClick={() => {
              setQuery('');
              setPicker(true);
            }}
            aria-label={tr('选择模型')}
          />
        </Space.Compact>
      </Form.Item>
      <Modal
        title={tr('选择模型')}
        open={picker}
        onCancel={close}
        footer={null}
        width={isMobile ? '94%' : 640}
        destroyOnClose
        styles={{
          content: { padding: 0 },
          header: { padding: '16px 24px 0' },
          body: { padding: 0, maxHeight: 'min(480px, 60dvh)', overflowY: 'auto' },
        }}
      >
        <div style={{ padding: '12px 24px 16px' }}>
          <Input.Search
            allowClear
            placeholder={tr('搜索模型')}
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            style={{ marginBottom: 12 }}
          />
          {modelsError ? (
            <Alert
              type="error"
              showIcon
              message={tr('模型目录加载失败')}
              description={modelsError}
              style={{ marginBottom: 12 }}
            />
          ) : null}
          {choices.map((model) => (
            <ModelCard key={model.id} model={model} onClick={() => choose(model)} />
          ))}
          {choices.length === 0 && !modelsError ? (
            <Typography.Text type="secondary">{tr('供应商没有返回模型')}</Typography.Text>
          ) : null}
        </div>
      </Modal>
    </>
  );
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

/** Warning shown below the model field. */
function ModelCatalogHint({
  models,
  error,
  model,
}: {
  models: ProviderModel[];
  error: string;
  model?: string;
}) {
  const rows = model ? [{ value: model }] : [];
  const disabled = disabledTargets(rows, models);
  const missing = unknownModels(rows, models).filter((id) => !disabled.includes(id));
  return (
    <>
      {error ? (
        <Typography.Paragraph type="warning" style={{ fontSize: 12, marginBottom: 8 }}>
          {tr('无法加载模型目录：{error}（仍可手动输入）', { error })}
        </Typography.Paragraph>
      ) : null}
      {disabled.length > 0 ? (
        <Alert
          type="warning"
          showIcon
          style={{ marginBottom: 8 }}
          message={tr('目标模型已在 Provider 中禁用，路由会跳过该 node：{models}', { models: disabled.join(', ') })}
          description={tr('可在 Provider 的模型列表中重新启用；禁用期间该映射不会参与路由。')}
        />
      ) : null}
      {missing.length > 0 ? (
        <Alert
          type="warning"
          showIcon
          style={{ marginBottom: 8 }}
          message={tr('未知模型或 API 待定：{models}', { models: missing.join(', ') })}
          description={tr('目录中没有该模型，或目录尚未提供它的 API。仍可保存，网关会按默认 API 顺序解析。')}
        />
      ) : null}
    </>
  );
}

/** Read-only hint for the API the gateway will use for the node's model. */
function DerivedTargetAPIHint({
  provider,
  models,
  model,
  api: selected,
}: {
  provider?: Provider;
  models: ProviderModel[];
  model?: string;
  api?: string;
}) {
  if (!provider) return null;
  const apis = configuredAPIs(provider);
  const byID = new Map(models.map((m) => [m.id, m]));
  const target = (model ?? '').trim();
  const fallback = target ? derivedTargetAPI(provider.type, apis, byID.get(target)?.apis) : '';
  const disabled = target ? byID.get(target)?.disabled === true : false;
  const note = genericCanonicalNote(provider.type, apis);
  const pinned = (selected ?? '').trim();
  const fixed = pinned !== '' && pinned !== 'auto';
  const missing = fixed && !apis.includes(pinned as ProviderApi);
  if (!target && !note) return null;
  return (
    <>
      {target ? (
        <Typography.Paragraph type={missing ? 'warning' : 'secondary'} style={{ fontSize: 12, marginBottom: 8 }}>
          {fixed
            ? `${tr('目标 API：{api}（固定）', { api: pinned })}${missing ? tr('（未配置 base URL）') : ''}`
            : tr('目标 API：auto（按客户端协议匹配；默认 {api}）', {
                api: fallback === 'unknown' ? tr('未知（无可用 base URL）') : fallback,
              })}
          {disabled ? tr('（已禁用）') : ''}
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
  const [keyRows, setKeyRows] = useState<ProviderKeyRow[]>(() => providerKeyRows(provider.keyPreviews));
  const [deletedKeys, setDeletedKeys] = useState<number[]>([]);
  const [headersJSON, setHeadersJSON] = useState(() => headersText(provider.headers));

  useEffect(() => {
    setKeyRows(providerKeyRows(provider.keyPreviews));
    setDeletedKeys([]);
    setHeadersJSON(headersText(provider.headers));
  }, [provider.name, provider.keyPreviews, provider.headers]);

  const deleteKey = (i: number) => {
    const row = keyRows[i];
    if (!row) return;
    if (row.index !== undefined) {
      setDeletedKeys((current) => (current.includes(row.index!) ? current : [...current, row.index!]));
    }
    setKeyRows((current) => current.filter((_, index) => index !== i));
  };

  const save = async () => {
    const v = await form.validateFields();
    let headers: Record<string, string>;
    try {
      headers = parseHeadersText(headersJSON);
    } catch (err) {
      message.error(errorText(err));
      return;
    }
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
          userAgent: String(v.userAgent ?? '').trim(),
          headers,
        },
        { models: provider.models ?? {} },
      );
      const keyUpdates = providerKeyUpdates(keyRows, deletedKeys);
      if (keyUpdates.length > 0) payload.keyUpdates = keyUpdates;
      await updateProvider(provider.name, payload);
      message.success(tr('已保存'));
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
      message.success(tr('已删除'));
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
        userAgent: provider.userAgent ?? '',
      }}
    >
      <Typography.Title level={5} style={{ marginTop: 0 }}>
        {provider.name}
      </Typography.Title>
      <Divider />
      <Form.Item name="type" label="type" rules={[{ required: true }]} extra={tr('创建后不可修改')}>
        <Select disabled options={PROVIDER_TYPES.map((v) => ({ value: v, label: v }))} />
      </Form.Item>
      {baseURLLocked ? (
        <Typography.Paragraph type="secondary" style={{ fontSize: 12, marginBottom: 8 }}>
          {tr('非 generic Provider 的 API base URL 创建后锁定，只有 generic 类型可以编辑；如需更换请删除后重建。')}
        </Typography.Paragraph>
      ) : null}
      <Form.Item
        name="chat"
        label="chat base URL"
        extra={tr('OpenAI Chat Completions 端点，通常以 /v1 结尾；留空表示不提供。')}
      >
        <Input disabled={baseURLLocked} placeholder="https://api.example.com/v1" />
      </Form.Item>
      <Form.Item
        name="responses"
        label="responses base URL"
        extra={tr('OpenAI Responses 端点，通常以 /v1 结尾；留空表示不提供。')}
      >
        <Input disabled={baseURLLocked} placeholder="https://api.example.com/v1" />
      </Form.Item>
      <Form.Item
        name="anthropic"
        label="anthropic base URL"
        extra={tr('Anthropic Messages 端点，通常是根路径；留空表示不提供。')}
      >
        <Input disabled={baseURLLocked} placeholder="https://api.example.com/anthropic" />
      </Form.Item>
      <Form.Item
        name="gemini"
        label="gemini base URL"
        extra={tr('Gemini API 端点，通常以 /v1beta 结尾；留空表示不提供。')}
      >
        <Input disabled={baseURLLocked} placeholder="https://generativelanguage.googleapis.com/v1beta" />
      </Form.Item>
      <Form.Item
        name="userAgent"
        label="upstream User-Agent"
        extra={tr('留空使用 gateway.userAgent；headers 中的 User-Agent 会覆盖它。')}
      >
        <Input placeholder="llm-gw/..." />
      </Form.Item>
      <Form.Item
        label={tr('upstream headers（JSON）')}
        extra={tr('额外请求头，例如 {"X-Custom":"value"}；Host、Content-Length、Accept-Encoding 由网关管理。')}
      >
        <Input.TextArea
          rows={4}
          value={headersJSON}
          onChange={(e) => setHeadersJSON(e.target.value)}
          placeholder='{"X-Custom":"value"}'
        />
      </Form.Item>
      <ProviderKeyEditor
        rows={keyRows}
        onChange={(i, value) => setKeyRows((rows) => rows.map((row, index) => (index === i ? { ...row, value } : row)))}
        onDelete={deleteKey}
        onAdd={() => setKeyRows((rows) => [...rows, { value: '' }])}
      />
      <Typography.Paragraph type="secondary" style={{ fontSize: 12 }}>
        {tr('至少填写一个 API base URL。模型映射与 backup/weight 在 Node 上配置；同一个 Provider 可以被多个 Node 引用。')}
      </Typography.Paragraph>
      <Divider />
      <Space>
        <Button type="primary" loading={busy} onClick={save}>
          {tr('保存')}
        </Button>
        <Button danger loading={busy} onClick={remove}>
          {tr('删除')}
        </Button>
      </Space>
    </Form>
  );
}

function RouteNodeList({
  value = [],
  onChange,
  nodes,
  health,
}: {
  value?: string[];
  onChange?: (value: string[]) => void;
  nodes: BindNode[];
  health?: RouteHealth;
}) {
  const [dragIndex, setDragIndex] = useState<number | null>(null);
  const byName = new Map(nodes.map((n) => [n.name, n]));
  const healthByName = new Map((health?.nodes ?? []).map((n) => [n.name, n]));
  const available = nodes.filter((n) => !value.includes(n.name));
  const update = (next: string[]) => onChange?.(next);

  return (
    <div>
      {value.length === 0 ? (
        <Typography.Text type="secondary">{tr('还没有 node，先从下面添加')}</Typography.Text>
      ) : null}
      {value.map((name, index) => {
        const node = byName.get(name);
        const h = healthByName.get(name);
        return (
          <div
            key={name}
            draggable
            onDragStart={(e) => {
              setDragIndex(index);
              e.dataTransfer.effectAllowed = 'move';
            }}
            onDragOver={(e) => {
              e.preventDefault();
              e.dataTransfer.dropEffect = 'move';
            }}
            onDrop={(e) => {
              e.preventDefault();
              if (dragIndex !== null) update(reorderNodes(value, dragIndex, index));
              setDragIndex(null);
            }}
            onDragEnd={() => setDragIndex(null)}
            style={{
              display: 'flex',
              alignItems: 'center',
              gap: 8,
              padding: '6px 8px',
              marginBottom: 4,
              border: '1px solid var(--gw-border)',
              borderRadius: 6,
              background: dragIndex === index ? 'var(--gw-bg)' : 'var(--gw-surface)',
              cursor: 'grab',
            }}
          >
            <HolderOutlined style={{ color: 'var(--gw-text-muted)' }} />
            <Typography.Text type="secondary" style={{ width: 18, textAlign: 'right' }}>
              {index + 1}
            </Typography.Text>
            <div style={{ flex: 1, minWidth: 0 }}>
              <Typography.Text strong style={{ display: 'block', wordBreak: 'break-all' }}>
                {name}
              </Typography.Text>
              <Typography.Text type="secondary" style={{ fontSize: 11 }}>
                {node ? `${node.provider} · ${node.model}` : 'node'}
              </Typography.Text>
            </div>
            {h ? (
              <Tag
                color={h.dead ? 'red' : h.failCount > 0 ? 'orange' : 'green'}
                title={
                  h.dead && h.deadUntil
                    ? tr('dead until {time}', { time: h.deadUntil })
                    : h.lastFailure
                      ? tr('last failure {time}', { time: h.lastFailure })
                      : undefined
                }
              >
                {h.dead
                  ? tr('dead ({count})', { count: h.failCount })
                  : h.failCount > 0
                    ? tr('失败 ({count})', { count: h.failCount })
                    : tr('正常')}
              </Tag>
            ) : null}
            <Button
              type="text"
              size="small"
              danger
              aria-label={tr('移除 {name}', { name })}
              icon={<MinusCircleOutlined />}
              onClick={() => update(value.filter((v) => v !== name))}
            />
          </div>
        );
      })}
      <Select
        size="small"
        showSearch
        value={null}
        placeholder={tr('添加 node')}
        options={available.map((n) => ({ value: n.name, label: `${n.name} (${n.provider})` }))}
        onSelect={(name) => update(addRouteNode(value, String(name)))}
        style={{ width: '100%', marginTop: 6 }}
      />
      <Typography.Paragraph type="secondary" style={{ fontSize: 12, margin: '6px 0 0' }}>
        {tr('拖动行调整顺序；fifo/round 会按这里的顺序选择节点。')}
      </Typography.Paragraph>
    </div>
  );
}

export function RouteForm({ route, config, onSaved, health }: { route: Route; config: ConfigResponse; onSaved: () => Promise<void>; health?: RouteHealth }) {
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
        maxFails: v.maxFails,
        failTimeout: v.failTimeout,
      });
      message.success(tr('已保存'));
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
      message.success(tr('已删除'));
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
      initialValues={{
        strategy: route.strategy ?? 'round',
        maxFails: route.maxFails ?? 1,
        failTimeout: route.failTimeout ?? '10s',
        nodes: route.nodes,
      }}
    >
      <Typography.Title level={5} style={{ marginTop: 0 }}>
        {route.name}
      </Typography.Title>
      <Form.Item name="strategy" label="strategy">
        <Select options={['round', 'fifo', 'random'].map((v) => ({ value: v, label: v }))} />
      </Form.Item>
      <Space size={12} wrap align="start">
        <Form.Item
          name="maxFails"
          label="maxFails"
          tooltip={tr('连续失败达到该次数后标记为 dead。默认 1。')}
          rules={[{ required: true, message: tr('必填') }]}
          style={{ marginBottom: 8 }}
        >
          <InputNumber min={1} max={100} size="small" style={{ width: 90 }} />
        </Form.Item>
        <Form.Item
          name="failTimeout"
          label="failTimeout"
          tooltip={tr('dead 节点被跳过的时长，例如 10s、30s、1m。默认 10s。')}
          rules={[{ required: true, message: tr('必填') }]}
          style={{ marginBottom: 8 }}
        >
          <Input size="small" placeholder="10s" style={{ width: 110 }} />
        </Form.Item>
      </Space>
      <Form.Item
        name="nodes"
        label={tr('Nodes（拖动排序，顺序 = 优先级）')}
        rules={[{ required: true, message: tr('至少选择一个 node') }]}
      >
        <RouteNodeList nodes={config.nodes} health={health} />
      </Form.Item>
      <Divider />
      <Space>
        <Button type="primary" loading={busy} onClick={save}>
          {tr('保存')}
        </Button>
        <Button danger loading={busy} onClick={remove}>
          {tr('删除')}
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
        enabled: node.enabled,
        model: v.model,
        api: v.api === 'auto' ? '' : v.api,
      });
      message.success(tr('已保存'));
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
      message.success(tr('已删除'));
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
  const model = Form.useWatch('model', form) as string | undefined;
  const api = Form.useWatch('api', form) as string | undefined;

  return (
    <Form
      form={form}
      layout="vertical"
      size="small"
      initialValues={{
        provider: node.provider,
        model: node.model,
        api: node.api ?? 'auto',
        backup: node.backup,
        weight: node.weight ?? 1,
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
      <ModelField models={models} modelsError={modelsError} />
      <Form.Item name="api" label={tr('目标 API（协议）')}>
        <Select options={nodeAPIOptions(provider).map((value) => ({ value, label: value }))} />
      </Form.Item>
      <DerivedTargetAPIHint provider={provider} models={models} model={model} api={api} />
      <ModelCatalogHint models={models} error={modelsError} model={model} />
      <Typography.Paragraph type="secondary" style={{ fontSize: 12, marginBottom: 8 }}>
        {tr('backup：主节点失败时作为备用；weight：轮询权重。')}
      </Typography.Paragraph>
      <Space size={16} wrap align="start">
        <Form.Item name="backup" label="backup" valuePropName="checked" style={{ marginBottom: 0 }}>
          <Switch size="small" />
        </Form.Item>
        <Form.Item name="weight" label="weight" style={{ marginBottom: 0 }}>
          <InputNumber min={0} size="small" style={{ width: 70 }} />
        </Form.Item>
      </Space>
      <Divider />
      <Space>
        <Button type="primary" loading={busy} onClick={save}>
          {tr('保存')}
        </Button>
        <Button danger loading={busy} onClick={remove}>
          {tr('删除')}
        </Button>
      </Space>
    </Form>
  );
}

export function EntrypointForm({
  entrypoint,
  index,
  config,
  onSaved,
}: {
  entrypoint: Entrypoint;
  index: number;
  config: ConfigResponse;
  onSaved: () => Promise<void>;
}) {
  const [busy, setBusy] = useState(false);
  const [form] = Form.useForm();

  const save = async () => {
    const v = await form.validateFields();
    setBusy(true);
    try {
      const next = config.entrypoints.map((e, i) =>
        i === index ? { name: v.name, match: v.match, route: v.route } : e,
      );
      await putEntrypoints(next);
      message.success(tr('已保存'));
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
      await putEntrypoints(config.entrypoints.filter((_, i) => i !== index));
      message.success(tr('已删除'));
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
      initialValues={{ name: entrypoint.name, match: entrypoint.match, route: entrypoint.route }}
    >
      <Typography.Title level={5} style={{ marginTop: 0 }}>
        {entrypoint.name}
      </Typography.Title>
      <Tag color="geekblue">entrypoint</Tag>
      <Divider />
      <Form.Item name="name" label={tr('名称')} rules={[{ required: true }]}>
        <Input placeholder="fast" />
      </Form.Item>
      <Form.Item
        name="match"
        label={tr('客户端模型 glob')}
        rules={[{ required: true, message: tr('至少填写一个模型 glob') }]}
        extra={tr('顺序见入口列表；同一个入口内多个 glob 等价。')}
      >
        <Select mode="tags" tokenSeparators={[',']} placeholder="claude-opus*" />
      </Form.Item>
      <Form.Item name="route" label={tr('路由')} rules={[{ required: true }]}>
        <Select
          showSearch
          optionFilterProp="label"
          options={config.routes.map((r) => ({ value: r.name, label: r.name }))}
          placeholder="strong / fast"
        />
      </Form.Item>
      <Divider />
      <Space>
        <Button type="primary" loading={busy} onClick={save}>
          {tr('保存')}
        </Button>
        <Button danger loading={busy} onClick={remove}>
          {tr('删除')}
        </Button>
      </Space>
    </Form>
  );
}

function EdgeForm({ id, config, onSaved }: { id: string; config: ConfigResponse; onSaved: () => Promise<void> }) {
  const [busy, setBusy] = useState(false);
  if (id.startsWith('entrypoint:')) {
    return (
      <div>
        <Typography.Title level={5} style={{ marginTop: 0 }}>
          {tr('入口连线')}
        </Typography.Title>
        <Typography.Paragraph type="secondary">
          {tr('入口指向哪条 route 由 entrypoint 的 route 决定，请在入口面板里修改。')}
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
        message.success(tr('已断开'));
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
          {tr('连线')}
        </Typography.Title>
        <Typography.Paragraph>
          {routeName} → {nodeName}
          {node ? `（${node.provider}）` : ''}
        </Typography.Paragraph>
        <Typography.Paragraph type="secondary" style={{ fontSize: 12 }}>
          {tr('模型映射和 backup/weight 都在 Node 面板里改。')}
        </Typography.Paragraph>
        <Button danger loading={busy} onClick={remove} disabled={!route}>
          {tr('断开')}
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
          {tr('连线')}
        </Typography.Title>
        <Typography.Paragraph>
          {nodeName} → {providerName}
        </Typography.Paragraph>
        <Typography.Paragraph type="secondary" style={{ fontSize: 12 }}>
          {tr('Node 指向哪个 Provider 由 Node 的 provider 字段决定，请在 Node 面板修改。')}
        </Typography.Paragraph>
      </div>
    );
  }

  return (
    <div>
      <Typography.Title level={5} style={{ marginTop: 0 }}>
        {tr('连线')}
      </Typography.Title>
      <Typography.Paragraph type="secondary">{tr('这条连线不可直接编辑。')}</Typography.Paragraph>
    </div>
  );
}

// ---------------------------------------------------------------- page

export type AddKind = 'provider' | 'node' | 'route' | 'entrypoint';

const ADD_LABELS: Record<AddKind, string> = {
  provider: '提供商',
  node: '节点',
  route: '路由',
  entrypoint: '入口',
};

/** The four add actions shown over the editable canvas. */
export function AddButtons({ onAdd }: { onAdd: (kind: AddKind) => void }) {
  return (
    <>
      {(Object.keys(ADD_LABELS) as AddKind[]).map((kind) => (
        <Button key={kind} icon={<PlusOutlined />} onClick={() => onAdd(kind)}>
          {tr(ADD_LABELS[kind])}
        </Button>
      ))}
    </>
  );
}

export function Edit({ config, onReload }: Props) {
  const isMobile = !Grid.useBreakpoint().md;
  const [selection, setSelection] = useState<Selection>(null);
  const [addOpen, setAddOpen] = useState<AddKind | null>(null);
  const [presets, setPresets] = useState<Preset[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');

  const openAdd = async (kind: AddKind) => {
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
      } else if (skind === 'entrypoint' && tkind === 'route') {
        const found = entrypointFromNodeId(config.entrypoints, c.source);
        if (!found) throw new Error('entrypoint not found');
        if (!config.routes.some((r) => r.name === tname)) throw new Error('route not found');
        const next = config.entrypoints.map((e, i) =>
          i === found.index ? { ...e, route: tname } : e,
        );
        await putEntrypoints(next);
      } else {
        throw new Error(tr('不支持的连接方向'));
      }
      await onReload();
    } catch (err) {
      setError(errorText(err));
    } finally {
      setBusy(false);
    }
  };

  const panel = useMemo(() => {
    if (!selection) return null;
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
    if (kind === 'entrypoint') {
      const found = entrypointFromNodeId(config.entrypoints, selection.id);
      return found ? (
        <EntrypointForm
          key={found.index}
          entrypoint={found.entrypoint}
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
            <AddButtons onAdd={(kind) => void openAdd(kind)} />
            {busy ? <Tag color="processing">{tr('应用中…')}</Tag> : null}
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
      ) : selection ? (
        <aside
          style={{
            width: 340,
            borderLeft: '1px solid var(--gw-border)',
            background: 'var(--gw-surface)',
            padding: 16,
            overflow: 'auto',
          }}
        >
          {panel}
        </aside>
      ) : null}

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
      <AddEntrypointModal
        open={addOpen === 'entrypoint'}
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

export function AddProviderModal({
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
      const keys = ((v.keys ?? []) as string[])
        .map((s) => String(s ?? '').trim())
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
    <Modal open={open} title={tr('添加提供商')} onCancel={onClose} onOk={submit} confirmLoading={busy} destroyOnClose>
      <Form form={form} layout="vertical" size="small" initialValues={{ type: 'generic', keys: [''] }}>
        <Form.Item name="preset" label={tr('预设')}>
          <Select
            allowClear
            options={presets.map((p) => ({ value: p.name, label: `${p.name} (${p.type})` }))}
            placeholder={tr('可选')}
            onChange={(name?: string) => {
              const p = presets.find((x) => x.name === name);
              if (!p) return;
              form.setFieldsValue(presetFormValues(p));
              void form.validateFields(['name', 'type', 'chat', 'responses', 'anthropic', 'gemini']).catch(() => undefined);
            }}
          />
        </Form.Item>
        <Form.Item name="name" label={tr('名称')} rules={[{ required: true }]}>
          <Input placeholder="opencode-go" />
        </Form.Item>
        <Form.Item name="type" label="type" rules={[{ required: true }]}>
          <Select options={PROVIDER_TYPES.map((v) => ({ value: v, label: v }))} />
        </Form.Item>
        <Form.Item name="chat" label="chat base URL" extra={tr('留空表示不提供该 API。')}>
          <Input placeholder="https://api.example.com/v1" />
        </Form.Item>
        <Form.Item name="responses" label="responses base URL" extra={tr('留空表示不提供该 API。')}>
          <Input placeholder="https://api.example.com/v1" />
        </Form.Item>
        <Form.Item name="anthropic" label="anthropic base URL" extra={tr('留空表示不提供该 API。')}>
          <Input placeholder="https://api.example.com/anthropic" />
        </Form.Item>
        <Form.Item name="gemini" label="gemini base URL" extra={tr('留空表示不提供该 API。')}>
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
              <Form.Item label={tr('已配置 API')}>
                {apis.length > 0 ? (
                  <Space wrap size={2}>
                    {apis.map((api) => (
                      <Tag key={api} color="blue">
                        {api}
                      </Tag>
                    ))}
                  </Space>
                ) : (
                  <Typography.Text type="warning">{tr('至少填写一个 API base URL')}</Typography.Text>
                )}
              </Form.Item>
            );
          }}
        </Form.Item>
        <KeyField label="keys" extra={tr('每个输入框一个 key；留空表示不配置 key。')} />
        <Typography.Paragraph type="secondary" style={{ fontSize: 12 }}>
          {tr('创建后在画布上为它建 Node，再在 Node 上配置模型映射。type 与 API base URL 创建后锁定（generic 的 base URL 除外）。')}
        </Typography.Paragraph>
      </Form>
    </Modal>
  );
}


export function AddNodeModal({
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
        model: v.model,
        api: v.api === 'auto' ? '' : v.api,
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
  const model = Form.useWatch('model', form) as string | undefined;
  const api = Form.useWatch('api', form) as string | undefined;

  return (
    <Modal open={open} title={tr('添加节点')} onCancel={onClose} onOk={submit} confirmLoading={busy} destroyOnClose>
      <Form form={form} layout="vertical" size="small">
        <Form.Item name="name" label={tr('名称')} rules={[{ required: true }]}>
          <Input placeholder="deepseek-fast" />
        </Form.Item>
        <Form.Item name="provider" label="provider" rules={[{ required: true }]}>
          <Select options={providerOptions} />
        </Form.Item>
        <ModelField models={models} modelsError={modelsError} />
        <Form.Item name="api" label={tr('目标 API（协议）')} initialValue="auto">
          <Select options={nodeAPIOptions(provider).map((value) => ({ value, label: value }))} />
        </Form.Item>
        <DerivedTargetAPIHint provider={provider} models={models} model={model} api={api} />
        <ModelCatalogHint models={models} error={modelsError} model={model} />
      </Form>
    </Modal>
  );
}

export function AddRouteModal({
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
      await createRoute({
        name: v.name,
        nodes: v.nodes ?? [],
        strategy: v.strategy,
        maxFails: v.maxFails,
        failTimeout: v.failTimeout,
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
    <Modal open={open} title={tr('添加路由')} onCancel={onClose} onOk={submit} confirmLoading={busy} destroyOnClose>
      <Form form={form} layout="vertical" size="small">
        <Form.Item name="name" label={tr('名称')} rules={[{ required: true }]}>
          <Input placeholder="strong" />
        </Form.Item>
        <Form.Item name="strategy" label="strategy" initialValue="round">
          <Select options={['round', 'fifo', 'random'].map((v) => ({ value: v, label: v }))} />
        </Form.Item>
        <Space size={12} wrap align="start">
          <Form.Item name="maxFails" label="maxFails" initialValue={1} style={{ marginBottom: 8 }}>
            <InputNumber min={1} max={100} size="small" style={{ width: 90 }} />
          </Form.Item>
          <Form.Item name="failTimeout" label="failTimeout" initialValue="10s" style={{ marginBottom: 8 }}>
            <Input size="small" placeholder="10s" style={{ width: 110 }} />
          </Form.Item>
        </Space>
        <Form.Item
          name="nodes"
          label={tr('Nodes（拖动排序，顺序 = 优先级）')}
          rules={[{ required: true, message: tr('至少选择一个 node') }]}
        >
          <RouteNodeList nodes={config.nodes} />
        </Form.Item>
      </Form>
    </Modal>
  );
}

export function AddEntrypointModal({
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
      await putEntrypoints([
        ...config.entrypoints,
        { name: v.name, match: v.match, route: v.route },
      ]);
      form.resetFields();
      await onDone();
    } catch (err) {
      onError(errorText(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal
      open={open}
      title={tr('添加入口')}
      onCancel={onClose}
      onOk={submit}
      confirmLoading={busy}
      destroyOnClose
    >
      <Form form={form} layout="vertical" size="small">
        <Form.Item name="name" label={tr('名称')} rules={[{ required: true }]}>
          <Input placeholder="fast" />
        </Form.Item>
        <Form.Item
          name="match"
          label={tr('客户端模型 glob')}
          rules={[{ required: true, message: tr('至少填写一个模型 glob') }]}
        >
          <Select mode="tags" tokenSeparators={[',']} placeholder="claude-opus*" />
        </Form.Item>
        <Form.Item name="route" label={tr('路由')} rules={[{ required: true }]}>
          <Select
            showSearch
            optionFilterProp="label"
            options={config.routes.map((r) => ({ value: r.name, label: r.name }))}
            placeholder="strong / fast"
          />
        </Form.Item>
      </Form>
    </Modal>
  );
}
