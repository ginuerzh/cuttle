import { useEffect, useMemo, useRef, useState } from 'react';
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
import { ArrowDownOutlined, ArrowUpOutlined, HolderOutlined, MinusCircleOutlined, PlusOutlined, SearchOutlined } from '@ant-design/icons';
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
import type { ConfigResponse, Entrypoint, EntrypointRoute, Node as BindNode, Preset, Provider, ProviderApi, ProviderModel, Route, RouteHealth } from '../api/types';
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
  nodeKeyOptions,
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
import { connectEntrypointRoute, disconnectEntrypointBinding, disconnectNodeProvider, planNodeProviderConnect } from './drafts';
import type { NodeRouteHealth } from './routeHealth';
import { entityLabel, labelMap, configLabels } from '../labels';

interface Props {
  config: ConfigResponse;
  onReload: () => Promise<void>;
  nodeHealth?: NodeRouteHealth;
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
  onChange: (index: number, patch: Partial<ProviderKeyRow>) => void;
  onDelete: (index: number) => void;
  onAdd: () => void;
}) {
  return (
    <Form.Item
      label="keys"
      extra={tr('已有 key 的名称可随时修改，值继续以掩码显示；如需更换请删除后添加新 key。一个 node 可以固定使用其中一个，key 改名不会影响已固定它的 node。')}
      style={{ marginBottom: 12 }}
    >
      {rows.map((row, i) => (
        <div
          key={row.id ?? `new-${i}`}
          style={{ display: 'flex', gap: 4, marginTop: 6, alignItems: 'center' }}
        >
          {row.existing ? (
            <>
              <Input
                aria-label={tr('key 名称')}
                value={row.name}
                onChange={(e) => onChange(i, { name: e.target.value })}
                style={{ flex: 1 }}
              />
              <Tag style={{ flex: 1, marginInlineEnd: 0 }}>{row.preview || '****'}</Tag>
              <Switch
                size="small"
                checked={row.enabled}
                onChange={(checked) => onChange(i, { enabled: checked })}
                aria-label={tr('启用 {name}', { name: row.name })}
              />
            </>
          ) : (
            <>
              <Input
                value={row.name}
                placeholder={tr('key 名称')}
                onChange={(e) => onChange(i, { name: e.target.value })}
              />
              <Input.Password
                value={row.value}
                placeholder="sk-..."
                onChange={(e) => onChange(i, { value: e.target.value })}
              />
            </>
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

/** One name + value pair per provider key; blank rows are ignored on save. */
function KeyField({ label, extra }: { label: string; extra?: string }) {
  return (
    <Form.Item label={label} extra={extra} style={{ marginBottom: 12 }}>
      <Form.List name="keys">
        {(fields, { add, remove }) => (
          <>
            {fields.map((field) => (
              <div key={field.key} style={{ display: 'flex', gap: 4, marginTop: 4, alignItems: 'center' }}>
                <Form.Item name={[field.name, 'name']} noStyle>
                  <Input placeholder={tr('key 名称')} />
                </Form.Item>
                <Form.Item name={[field.name, 'key']} noStyle>
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
              onClick={() => add({ name: '', key: '' })}
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
  required = true,
}: {
  models: ProviderModel[];
  modelsError: string;
  required?: boolean;
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
      <Form.Item label={tr('模型 ID')} required={required} style={{ marginBottom: 12 }}>
        <Space.Compact style={{ width: '100%' }}>
          <Form.Item name="model" noStyle rules={required ? [{ required: true, message: tr('请填写模型') }] : []}>
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
function useProviderModels(providerId?: string) {
  const [models, setModels] = useState<ProviderModel[]>([]);
  const [error, setError] = useState('');

  useEffect(() => {
    let active = true;
    setModels([]);
    setError('');
    if (!providerId) return;
    getProviderModels(providerId)
      .then((list) => {
        if (active) setModels(list);
      })
      .catch((err) => {
        if (active) setError(errorText(err));
      });
    return () => {
      active = false;
    };
  }, [providerId]);

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
  const [keyRows, setKeyRows] = useState<ProviderKeyRow[]>(() => providerKeyRows(provider.keys ?? []));
  const [deletedKeyIDs, setDeletedKeyIDs] = useState<string[]>([]);
  const [headersJSON, setHeadersJSON] = useState(() => headersText(provider.headers));

  useEffect(() => {
    setKeyRows(providerKeyRows(provider.keys ?? []));
    setDeletedKeyIDs([]);
    setHeadersJSON(headersText(provider.headers));
  }, [provider.id, provider.keys, provider.headers]);

  const deleteKey = (i: number) => {
    const row = keyRows[i];
    if (!row) return;
    const id = row.id;
    if (row.existing && id) {
      setDeletedKeyIDs((current) => (current.includes(id) ? current : [...current, id]));
    }
    setKeyRows((current) => current.filter((_, index) => index !== i));
  };

  const save = async () => {
    const incomplete = keyRows.some((row) =>
      row.existing
        ? !row.name.trim()
        : (row.name.trim() || row.value.trim()) && (!row.name.trim() || !row.value.trim()),
    );
    if (incomplete) {
      message.error(tr('key 名称和值都必须填写'));
      return;
    }
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
          name: String(v.name ?? '').trim(),
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
      const keyUpdates = providerKeyUpdates(keyRows, deletedKeyIDs);
      if (keyUpdates.length > 0) payload.keyUpdates = keyUpdates;
      await updateProvider(provider.id, payload);
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
      await deleteProvider(provider.id);
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
        name: provider.name,
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
      <Form.Item
        name="name"
        label={tr('名称')}
        rules={[{ required: true, message: tr('请填写名称') }]}
        extra={tr('仅用于显示，可随时修改；实体 id 不变。')}
      >
        <Input placeholder="opencode-go" />
      </Form.Item>
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
        <Input placeholder="cuttle/..." />
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
        onChange={(i, patch) => setKeyRows((rows) => rows.map((row, index) => (index === i ? { ...row, ...patch } : row)))}
        onDelete={deleteKey}
        onAdd={() => setKeyRows((rows) => [...rows, { name: '', value: '', enabled: true, originalName: '', originalEnabled: true, existing: false }])}
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

export function RouteNodeList({
  value = [],
  onChange,
  nodes,
  providers = [],
  health,
}: {
  value?: string[];
  onChange?: (value: string[]) => void;
  nodes: BindNode[];
  providers?: Provider[];
  health?: RouteHealth;
}) {
  const [dragIndex, setDragIndex] = useState<number | null>(null);
  const labels = labelMap(nodes);
  const providerLabels = labelMap(providers);
  const byId = new Map(nodes.map((n) => [n.id, n]));
  const healthById = new Map((health?.nodes ?? []).map((n) => [n.id, n]));
  const available = nodes.filter((n) => !value.includes(n.id));
  const providerLabel = (id: string) => entityLabel(id, providerLabels);
  const update = (next: string[]) => onChange?.(next);

  return (
    <div>
      {value.length === 0 ? (
        <Typography.Text type="secondary">{tr('还没有 node，先从下面添加')}</Typography.Text>
      ) : null}
      {value.map((id, index) => {
        const node = byId.get(id);
        const h = healthById.get(id);
        const nodeLabel = entityLabel(id, labels);
        return (
          <div
            key={id}
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
                {nodeLabel}
              </Typography.Text>
              <Typography.Text type="secondary" style={{ fontSize: 11 }}>
                {node ? `${providerLabel(node.provider)} · ${node.model}` : 'node'}
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
              aria-label={tr('移除 {name}', { name: nodeLabel })}
              icon={<MinusCircleOutlined />}
              onClick={() => update(value.filter((v) => v !== id))}
            />
          </div>
        );
      })}
      <Select
        size="small"
        showSearch
        value={null}
        placeholder={tr('添加 node')}
        options={available.map((n) => ({ value: n.id, label: `${n.name} (${providerLabel(n.provider)})` }))}
        onSelect={(id) => update(addRouteNode(value, String(id)))}
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
      await updateRoute(route.id, {
        name: String(v.name ?? '').trim(),
        nodes: v.nodes ?? [],
        strategy: v.strategy,
        maxFails: v.maxFails,
        failTimeout: v.failTimeout,
        retries: v.retries,
        retryCodes: v.retryCodes,
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
      await deleteRoute(route.id);
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
        name: route.name,
        strategy: route.strategy ?? 'round',
        maxFails: route.maxFails ?? 1,
        failTimeout: route.failTimeout ?? '10s',
        retries: route.retries ?? 0,
        retryCodes: route.retryCodes ?? '429,5xx',
        nodes: route.nodes,
      }}
    >
      <Typography.Title level={5} style={{ marginTop: 0 }}>
        {route.name}
      </Typography.Title>
      <Form.Item
        name="name"
        label={tr('名称')}
        rules={[{ required: true, message: tr('请填写名称') }]}
        extra={tr('仅用于显示，可随时修改；实体 id 不变。')}
        style={{ marginBottom: 12 }}
      >
        <Input placeholder="strong" />
      </Form.Item>
      <Form.Item name="strategy" label="strategy">
        <Select options={['round', 'fifo', 'random', 'sticky'].map((v) => ({ value: v, label: v }))} />
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
        <Form.Item
          name="retries"
          label="retries"
          tooltip={tr('可重试响应的额外尝试次数。默认 0（不重试），最多 10。')}
          style={{ marginBottom: 8 }}
        >
          <InputNumber min={0} max={10} size="small" style={{ width: 90 }} />
        </Form.Item>
        <Form.Item
          name="retryCodes"
          label="retryCodes"
          tooltip={tr('触发重试的状态码，逗号分隔，支持 5xx。默认 429,5xx。')}
          style={{ marginBottom: 8 }}
        >
          <Input size="small" placeholder="429,5xx" style={{ width: 130 }} />
        </Form.Item>
      </Space>
      <Form.Item
        name="nodes"
        label={tr('Nodes（拖动排序，顺序 = 优先级）')}
        rules={[{ required: true, message: tr('至少选择一个 node') }]}
      >
        <RouteNodeList nodes={config.nodes} providers={config.providers} health={health} />
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
  pendingProvider,
  onSaved,
}: {
  node: BindNode;
  config: ConfigResponse;
  /** A provider a topology drag picked that is not saved yet (no model chosen). */
  pendingProvider?: string;
  onSaved: () => Promise<void>;
}) {
  const [busy, setBusy] = useState(false);
  const [form] = Form.useForm();

  const save = async () => {
    const v = await form.validateFields().catch(() => null);
    if (!v) return;
    setBusy(true);
    try {
      await updateNode(node.id, {
        name: String(v.name ?? '').trim(),
        provider: String(v.provider ?? ''),
        backup: v.backup,
        weight: v.weight,
        enabled: node.enabled,
        model: String(v.model ?? ''),
        api: v.api === 'auto' || !v.api ? '' : v.api,
        key: String(v.key ?? ''),
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
      await deleteNode(node.id);
      message.success(tr('已删除'));
      await onSaved();
    } catch (err) {
      message.error(errorText(err));
    } finally {
      setBusy(false);
    }
  };

  const providerOptions = config.providers.map((p) => ({ value: p.id, label: p.name }));
  const watchedProvider = Form.useWatch('provider', form) as string | undefined;
  const [initialProvider, setInitialProvider] = useState(node.provider || undefined);
  // A topology drag-connect saves the provider outside this form. Re-sync the
  // field so the open panel shows the provider the line was drawn to, without
  // remounting (which would drop the operator's unsaved edits).
  // A staged topology connect fills the provider without saving it, so the
  // operator only has to pick a model.
  useEffect(() => {
    if (!pendingProvider) return;
    setInitialProvider(pendingProvider);
    form.setFieldsValue({ provider: pendingProvider });
  }, [form, pendingProvider]);

  const syncedProvider = useRef(node.provider);
  useEffect(() => {
    if (syncedProvider.current === node.provider) return;
    syncedProvider.current = node.provider;
    setInitialProvider(node.provider || undefined);
    // The connect helper drops a key that the new provider does not own, so the
    // field must follow it or the next save would send a foreign key id.
    form.setFieldsValue({ provider: node.provider || undefined, key: node.key ?? '' });
  }, [form, node.provider]);
  // Only fall back to the stored id before the field is touched: a cleared
  // provider must read as cleared, or the model stays required and the draft
  // can never be saved.
  const providerId = form.isFieldTouched('provider') ? watchedProvider : initialProvider;
  const provider = config.providers.find((p) => p.id === providerId);
  const { models, error: modelsError } = useProviderModels(provider?.id);
  const model = Form.useWatch('model', form) as string | undefined;
  const api = Form.useWatch('api', form) as string | undefined;
  const keyOptions = nodeKeyOptions(provider?.keys ?? []);

  return (
    <Form
      form={form}
      layout="vertical"
      size="small"
      initialValues={{
        name: node.name,
        provider: node.provider || undefined,
        model: node.model,
        api: node.api ?? 'auto',
        key: node.key ?? '',
        backup: node.backup,
        weight: node.weight ?? 1,
      }}
    >
      <Typography.Title level={5} style={{ marginTop: 0 }}>
        {node.name}
      </Typography.Title>
      <Tag color="cyan">node</Tag>
      <Divider />
      <Form.Item
        name="name"
        label={tr('名称')}
        rules={[{ required: true, message: tr('请填写名称') }]}
        extra={tr('仅用于显示，可随时修改；实体 id 不变。')}
        style={{ marginBottom: 12 }}
      >
        <Input placeholder="deepseek-fast" />
      </Form.Item>
      <Form.Item
        name="provider"
        label="provider"
        extra={tr('留空即为草稿：节点先建卡片，之后在拓扑上拖线接提供商。')}
      >
        <Select
          allowClear
          placeholder={tr('未连接（草稿）')}
          options={providerOptions}
          onChange={(v?: string) => {
            if (!v) {
              form.setFieldValue('model', '');
              form.setFieldValue('api', '');
              form.setFieldValue('key', '');
            }
          }}
        />
      </Form.Item>
      <ModelField models={models} modelsError={modelsError} required={!!provider} />
      <Form.Item name="api" label={tr('目标 API（协议）')}>
        <Select options={nodeAPIOptions(provider).map((value) => ({ value, label: value }))} />
      </Form.Item>
      <Form.Item
        name="key"
        label={tr('固定 key')}
        extra={tr('留空表示在所有启用的 key 之间轮询；选择后该 node 只使用这个 key。')}
      >
        <Select options={keyOptions} showSearch optionFilterProp="label" />
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

/** One binding row in the entrypoint editor; every field is optional while typing. */
export interface EntrypointBindingRow {
  route?: string;
  probe?: { command?: string; interval?: string; timeout?: string };
}

/** The default probe schedule the server applies when a field is blank. */
const DEFAULT_PROBE_INTERVAL_MS = 30_000;
const DEFAULT_PROBE_TIMEOUT_MS = 5_000;

const DURATION_PART = /(\d+(?:\.\d+)?)(ns|us|µs|ms|s|m|h)/g;
const DURATION_UNIT_MS: Record<string, number> = {
  ns: 1e-6,
  us: 1e-3,
  'µs': 1e-3,
  ms: 1,
  s: 1_000,
  m: 60_000,
  h: 3_600_000,
};

/**
 * Milliseconds of a Go duration string (`30s`, `1m30s`, `500ms`), or undefined
 * when the text is not one. Progressively summing the unit parts mirrors
 * `time.ParseDuration` closely enough to reject typos before the server does.
 */
export function probeDurationMs(value?: string): number | undefined {
  const text = (value ?? '').trim();
  if (!text) return undefined;
  let total = 0;
  let consumed = 0;
  for (const part of text.matchAll(DURATION_PART)) {
    if (part.index !== consumed) return undefined;
    total += Number(part[1]) * DURATION_UNIT_MS[part[2]];
    consumed += part[0].length;
  }
  return consumed === text.length ? total : undefined;
}

/** The problem with a probe interval, or undefined. A blank field is the default. */
export function probeIntervalError(value?: string): string | undefined {
  const text = (value ?? '').trim();
  if (!text) return undefined;
  const ms = probeDurationMs(text);
  if (ms === undefined) return '探测间隔必须是时长，例如 30s';
  if (ms < 1_000) return '探测间隔不能小于 1s（默认 30s）';
  return undefined;
}

/**
 * The problem with a probe timeout, or undefined. A blank field is the default
 * `5s`, so the comparison uses the effective values: an operator who typed no
 * timeout gets a message instead of a server rejection for a `5s` they never
 * entered.
 */
export function probeTimeoutError(value?: string, interval?: string): string | undefined {
  const text = (value ?? '').trim();
  const ms = text ? probeDurationMs(text) : DEFAULT_PROBE_TIMEOUT_MS;
  if (ms === undefined) return '探测超时必须是时长，例如 5s';
  if (ms < 1_000) return '探测超时不能小于 1s（默认 5s）';
  const intervalMs = ((interval ?? '').trim() ? probeDurationMs(interval) : DEFAULT_PROBE_INTERVAL_MS)
    ?? DEFAULT_PROBE_INTERVAL_MS;
  if (ms >= intervalMs) return '探测超时必须小于探测间隔（默认 30s / 5s）';
  return undefined;
}

function probeRuleResult(message?: string): Promise<void> {
  return message ? Promise.reject(new Error(tr(message))) : Promise.resolve();
}

/**
 * The ordered bindings a form row list saves. Blank rows are dropped so a form
 * with an untouched row saves a draft, and a probe without a command is dropped
 * so the server never sees an empty one (an empty probe would reject the save).
 * The order of the rows is the binding order and is never sorted.
 */
export function entrypointBindingInputs(rows?: EntrypointBindingRow[]): EntrypointRoute[] {
  const bindings: EntrypointRoute[] = [];
  for (const row of rows ?? []) {
    const route = (row?.route ?? '').trim();
    if (!route) continue;
    const command = (row?.probe?.command ?? '').trim();
    if (!command) {
      bindings.push({ route });
      continue;
    }
    const probe: EntrypointRoute['probe'] = { command };
    const interval = (row?.probe?.interval ?? '').trim();
    const timeout = (row?.probe?.timeout ?? '').trim();
    if (interval) probe.interval = interval;
    if (timeout) probe.timeout = timeout;
    bindings.push({ route, probe });
  }
  return bindings;
}

/** The editor rows of an entrypoint; a draft shows one empty row to fill in. */
function entrypointBindingRows(entrypoint?: Entrypoint): EntrypointBindingRow[] {
  const rows = (entrypoint?.routes ?? []).map((binding) => ({
    route: binding.route,
    probe: binding.probe ? { ...binding.probe } : undefined,
  }));
  return rows.length > 0 ? rows : [{}];
}

/**
 * Ordered route bindings with an optional probe. Bindings are inline config:
 * they carry no id, so rows are keyed by their position only. The probe fields
 * stay collapsed behind a toggle because most bindings never have one.
 */
function EntrypointBindingsField({ routes }: { routes: Route[] }) {
  const form = Form.useFormInstance();
  const routeOptions = routes.map((r) => ({ value: r.id, label: r.name }));

  const toggleProbe = (index: number, on: boolean) => {
    const rows: EntrypointBindingRow[] = [
      ...((form.getFieldValue('routes') as EntrypointBindingRow[] | undefined) ?? []),
    ];
    const row = rows[index] ?? {};
    rows[index] = { ...row, probe: on ? { command: '', interval: '', timeout: '' } : undefined };
    form.setFieldValue('routes', rows);
  };

  return (
    <Form.List name="routes">
      {(fields, { add, remove, move }) => (
        <>
          {fields.map((field, index) => (
            <div
              key={field.key}
              style={{ border: '1px solid var(--gw-border)', borderRadius: 6, padding: 8, marginTop: 8 }}
            >
              <div style={{ display: 'flex', gap: 4, alignItems: 'center' }}>
                <Form.Item name={[field.name, 'route']} noStyle>
                  <Select
                    allowClear
                    showSearch
                    optionFilterProp="label"
                    options={routeOptions}
                    placeholder={tr('选择路由')}
                    style={{ flex: 1 }}
                  />
                </Form.Item>
                <Button
                  size="small"
                  aria-label={tr('上移')}
                  icon={<ArrowUpOutlined />}
                  disabled={index === 0}
                  onClick={() => move(index, index - 1)}
                />
                <Button
                  size="small"
                  aria-label={tr('下移')}
                  icon={<ArrowDownOutlined />}
                  disabled={index === fields.length - 1}
                  onClick={() => move(index, index + 1)}
                />
                <Button
                  size="small"
                  danger
                  aria-label={tr('删除绑定')}
                  icon={<MinusCircleOutlined />}
                  onClick={() => remove(field.name)}
                />
              </div>
              <Form.Item noStyle shouldUpdate>
                {() => {
                  const current =
                    (form.getFieldValue('routes') as EntrypointBindingRow[] | undefined) ?? [];
                  if (current[field.name]?.probe === undefined) {
                    return (
                      <Button
                        type="link"
                        size="small"
                        style={{ paddingInline: 0 }}
                        onClick={() => toggleProbe(field.name, true)}
                      >
                        {tr('添加探测命令（可选）')}
                      </Button>
                    );
                  }
                  return (
                    <>
                      <Form.Item
                        name={[field.name, 'probe', 'command']}
                        label={tr('探测命令')}
                        rules={[{ required: true, message: tr('探测命令不能为空') }]}
                        extra={tr('用 sh -c 执行，退出码 0 表示该绑定可用。')}
                        style={{ marginTop: 8, marginBottom: 8 }}
                      >
                        <Input placeholder="curl -fsS http://127.0.0.1:8080/ready" />
                      </Form.Item>
                      <Space size={8} align="start" wrap>
                        <Form.Item
                          name={[field.name, 'probe', 'interval']}
                          label={tr('探测间隔')}
                          rules={[
                            {
                              validator: (_, value: string | undefined) =>
                                probeRuleResult(probeIntervalError(value)),
                            },
                          ]}
                          extra={tr('默认 30s，最小 1s')}
                        >
                          <Input placeholder="30s" style={{ width: 90 }} />
                        </Form.Item>
                        <Form.Item
                          name={[field.name, 'probe', 'timeout']}
                          label={tr('探测超时')}
                          rules={[
                            {
                              validator: (_, value: string | undefined) =>
                                probeRuleResult(
                                  probeTimeoutError(
                                    value,
                                    form.getFieldValue(['routes', field.name, 'probe', 'interval']),
                                  ),
                                ),
                            },
                          ]}
                          extra={tr('默认 5s，最小 1s，且小于间隔')}
                        >
                          <Input placeholder="5s" style={{ width: 90 }} />
                        </Form.Item>
                        <Button type="text" size="small" onClick={() => toggleProbe(field.name, false)}>
                          {tr('移除探测命令')}
                        </Button>
                      </Space>
                    </>
                  );
                }}
              </Form.Item>
            </div>
          ))}
          <Button type="dashed" size="small" block icon={<PlusOutlined />} style={{ marginTop: 8 }} onClick={() => add({})}>
            {tr('添加路由绑定')}
          </Button>
        </>
      )}
    </Form.List>
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
    const v = await form.validateFields().catch(() => null);
    if (!v) return;
    setBusy(true);
    try {
      const next = config.entrypoints.map((e, i) =>
        i === index
          ? {
              ...e,
              name: v.name,
              match: v.match,
              routes: entrypointBindingInputs(v.routes as EntrypointBindingRow[] | undefined),
            }
          : e,
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
      initialValues={{ name: entrypoint.name, match: entrypoint.match, routes: entrypointBindingRows(entrypoint) }}
    >
      <Typography.Title level={5} style={{ marginTop: 0 }}>
        {entrypoint.name}
      </Typography.Title>
      <Tag color="geekblue">entrypoint</Tag>
      <Divider />
      <Form.Item
        name="name"
        label={tr('名称')}
        rules={[{ required: true }]}
        extra={tr('仅用于显示，可随时修改；实体 id 不变。')}
      >
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
      <Form.Item
        label={tr('路由绑定')}
        extra={tr('按顺序尝试，第一个探测通过的绑定生效；没有探测的绑定总是可用，只能放在最后。没有绑定即为草稿：入口仍占用这些模型，但匹配的请求会失败。')}
        style={{ marginBottom: 12 }}
      >
        <EntrypointBindingsField routes={config.routes} />
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

/** The route id of an `entrypoint:<id>->route:<id>` topology edge. */
export function entrypointEdgeRoute(id: string): string | undefined {
  const marker = '->route:';
  const at = id.indexOf(marker);
  if (!id.startsWith('entrypoint:') || at < 0) return undefined;
  return id.slice(at + marker.length) || undefined;
}

/**
 * Appends the binding a drag-connect names. An already-bound route and an
 * unsupported connection return the same list, so the caller can skip the save.
 */
export function connectEntrypointEdge(entrypoints: Entrypoint[], source: string, target: string): Entrypoint[] {
  const found = entrypointFromNodeId(entrypoints, source);
  const routeID = target.startsWith('route:') ? target.slice('route:'.length) : '';
  if (!found || !routeID) return entrypoints;
  const next = connectEntrypointRoute(found.entrypoint, routeID);
  if (next === found.entrypoint) return entrypoints;
  return entrypoints.map((e, i) => (i === found.index ? next : e));
}

/**
 * Removes only the binding an `entrypoint:<id>->route:<id>` edge names. Any
 * other edge, including the draft `->unrouted` edge, leaves the list untouched.
 */
export function disconnectEntrypointEdge(entrypoints: Entrypoint[], id: string): Entrypoint[] {
  const routeID = entrypointEdgeRoute(id);
  if (!routeID) return entrypoints;
  const entrypointID = id.slice('entrypoint:'.length, id.indexOf('->route:'));
  return entrypoints.map((e) => (e.id === entrypointID ? disconnectEntrypointBinding(e, routeID) : e));
}

export function EdgeForm({ id, config, onSaved }: { id: string; config: ConfigResponse; onSaved: () => Promise<void> }) {
  const [busy, setBusy] = useState(false);
  if (id.startsWith('entrypoint:')) {
    const entrypointID = id.slice('entrypoint:'.length).split('->')[0] ?? '';
    const entrypoint = config.entrypoints.find((e) => e.id === entrypointID);
    const routeID = entrypointEdgeRoute(id);

    const remove = async () => {
      if (!entrypoint || !routeID) return;
      setBusy(true);
      try {
        await putEntrypoints(disconnectEntrypointEdge(config.entrypoints, id));
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
          {tr('入口连线')}
        </Typography.Title>
        <Typography.Paragraph type="secondary">
          {tr('一条连线对应一个路由绑定；断开只会移除这一个绑定，其余绑定保持顺序不变。')}
        </Typography.Paragraph>
        <Button danger loading={busy} onClick={remove} disabled={!entrypoint || !routeID}>
          {tr('断开')}
        </Button>
      </div>
    );
  }
  const parts = id.split('|');
  const source = parts[0] ?? '';
  const target = parts[1] ?? '';
  const labels = configLabels(config);

  if (source.startsWith('route:') && target.startsWith('node:')) {
    const routeId = source.slice('route:'.length);
    const nodeId = target.slice('node:'.length);
    const route = config.routes.find((r) => r.id === routeId);
    const provider = config.providers.find((p) => p.id === config.nodes.find((n) => n.id === nodeId)?.provider);

    const remove = async () => {
      if (!route) return;
      setBusy(true);
      try {
        await updateRoute(route.id, { ...route, nodes: route.nodes.filter((n) => n !== nodeId) });
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
          {entityLabel(routeId, labels.routes)} → {entityLabel(nodeId, labels.nodes)}
          {provider ? `（${provider.name}）` : ''}
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
    const nodeId = source.slice('node:'.length);
    const providerId = target.slice('provider:'.length);
    const node = config.nodes.find((n) => n.id === nodeId);

    const remove = async () => {
      if (!node) return;
      setBusy(true);
      try {
        await updateNode(node.id, disconnectNodeProvider(node));
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
          {entityLabel(nodeId, labels.nodes)} → {entityLabel(providerId, labels.providers)}
        </Typography.Paragraph>
        <Typography.Paragraph type="secondary" style={{ fontSize: 12 }}>
          {tr('Node 指向哪个 Provider 由 Node 的 provider 字段决定，请在 Node 面板修改。')}
        </Typography.Paragraph>
        <Button danger loading={busy} onClick={remove} disabled={!node}>
          {tr('断开')}
        </Button>
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

export function Edit({ config, onReload, nodeHealth }: Props) {
  const isMobile = !Grid.useBreakpoint().md;
  const [selection, setSelection] = useState<Selection>(null);
  const [addOpen, setAddOpen] = useState<AddKind | null>(null);
  const [presets, setPresets] = useState<Preset[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  // A drag-connect on a node without a model cannot be saved (the server
  // requires a model once a provider is set), so it is staged in the panel.
  const [pendingProvider, setPendingProvider] = useState<{ nodeId: string; providerId: string } | null>(null);
  const [notice, setNotice] = useState('');

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
    const [skind, sid] = splitId(c.source);
    const [tkind, tid] = splitId(c.target);
    setError('');
    setNotice('');
    setBusy(true);
    try {
      if (skind === 'route' && tkind === 'node') {
        const route = config.routes.find((r) => r.id === sid);
        if (!route) throw new Error('route not found');
        if (!route.nodes.includes(tid)) {
          await updateRoute(route.id, { ...route, nodes: [...route.nodes, tid] });
        }
      } else if (skind === 'entrypoint' && tkind === 'route') {
        const found = entrypointFromNodeId(config.entrypoints, c.source);
        if (!found) throw new Error('entrypoint not found');
        if (!config.routes.some((r) => r.id === tid)) throw new Error('route not found');
        const next = connectEntrypointEdge(config.entrypoints, c.source, c.target);
        // A route the entrypoint already binds keeps the list by identity; do
        // not round-trip an unchanged config to the server.
        if (next !== config.entrypoints) {
          await putEntrypoints(next);
        }
      } else if (skind === 'node' && tkind === 'provider') {
        const node = config.nodes.find((n) => n.id === sid);
        if (!node) throw new Error('node not found');
        const provider = config.providers.find((p) => p.id === tid);
        if (!provider) throw new Error('provider not found');
        const { next, needsModel } = planNodeProviderConnect(node, provider);
        if (needsModel) {
          // A node with a provider must carry a model, so stage the connection in
          // the panel: the provider is filled in and the operator only picks a
          // model before saving.
          setSelection({ kind: 'node', id: c.source });
          setPendingProvider({ nodeId: node.id, providerId: provider.id });
          setNotice(tr('已选好提供商：请在节点面板选择模型后保存，连线才会生效。'));
          return;
        }
        await updateNode(node.id, next);
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
    const [kind, entityId] = splitId(selection.id);
    if (kind === 'provider') {
      const p = config.providers.find((x) => x.id === entityId);
      return p ? <ProviderForm key={p.id} provider={p} onSaved={onReload} /> : null;
    }
    if (kind === 'route') {
      const r = config.routes.find((x) => x.id === entityId);
      return r ? <RouteForm key={r.id} route={r} config={config} onSaved={onReload} /> : null;
    }
    if (kind === 'node') {
      const n = config.nodes.find((x) => x.id === entityId);
      return n ? (
        <NodeForm
          key={n.id}
          node={n}
          config={config}
          pendingProvider={pendingProvider?.nodeId === n.id ? pendingProvider.providerId : undefined}
          onSaved={async () => {
            setPendingProvider(null);
            setNotice('');
            await onReload();
          }}
        />
      ) : null;
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
  }, [selection, config, onReload, pendingProvider]);

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
          {notice ? (
            <Alert style={{ marginTop: 8, maxWidth: 520 }} type="info" message={notice} showIcon closable onClose={() => setNotice('')} />
          ) : null}
        </div>
        <Topology config={config} metrics={null} onSelect={setSelection} editable onConnect={connect} nodeHealth={nodeHealth} />
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
      const rawKeys = (v.keys ?? []) as { name?: string; key?: string }[];
      const keys = rawKeys
        .map((k) => ({ name: String(k.name ?? '').trim(), key: String(k.key ?? '').trim(), enabled: true }))
        .filter((k) => k.name || k.key);
      if (keys.some((k) => !k.name || !k.key)) {
        throw new Error(tr('key 名称和值都必须填写'));
      }
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
      <Form form={form} layout="vertical" size="small" initialValues={{ type: 'generic', keys: [{ name: '', key: '' }] }}>
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
        <KeyField label="keys" extra={tr('每行一个 key；名称和值都留空表示不配置 key。')} />
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

  const providerOptions = config.providers.map((p) => ({ value: p.id, label: p.name }));
  const providerId = Form.useWatch('provider', form) as string | undefined;
  const provider = config.providers.find((p) => p.id === providerId);
  const { models, error: modelsError } = useProviderModels(provider?.id);
  const model = Form.useWatch('model', form) as string | undefined;
  const api = Form.useWatch('api', form) as string | undefined;

  return (
    <Modal open={open} title={tr('添加节点')} onCancel={onClose} onOk={submit} confirmLoading={busy} destroyOnClose>
      <Form form={form} layout="vertical" size="small">
        <Form.Item name="name" label={tr('名称')} rules={[{ required: true }]}>
          <Input placeholder="deepseek-fast" />
        </Form.Item>
        <Form.Item
          name="provider"
          label="provider"
          extra={tr('留空即为草稿：节点先建卡片，之后在拓扑上拖线接提供商。')}
        >
          <Select
            allowClear
            placeholder={tr('未连接（草稿）')}
            options={providerOptions}
            onChange={(v?: string) => {
              if (!v) {
                form.setFieldValue('model', '');
                form.setFieldValue('api', 'auto');
              }
            }}
          />
        </Form.Item>
        <ModelField models={models} modelsError={modelsError} required={!!provider} />
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
        retries: v.retries,
        retryCodes: v.retryCodes,
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
          <Select options={['round', 'fifo', 'random', 'sticky'].map((v) => ({ value: v, label: v }))} />
        </Form.Item>
        <Space size={12} wrap align="start">
          <Form.Item name="maxFails" label="maxFails" initialValue={1} style={{ marginBottom: 8 }}>
            <InputNumber min={1} max={100} size="small" style={{ width: 90 }} />
          </Form.Item>
          <Form.Item name="failTimeout" label="failTimeout" initialValue="10s" style={{ marginBottom: 8 }}>
            <Input size="small" placeholder="10s" style={{ width: 110 }} />
          </Form.Item>
          <Form.Item
            name="retries"
            label="retries"
            tooltip={tr('可重试响应的额外尝试次数。默认 0（不重试），最多 10。')}
            initialValue={0}
            style={{ marginBottom: 8 }}
          >
            <InputNumber min={0} max={10} size="small" style={{ width: 90 }} />
          </Form.Item>
          <Form.Item
            name="retryCodes"
            label="retryCodes"
            tooltip={tr('触发重试的状态码，逗号分隔，支持 5xx。默认 429,5xx。')}
            initialValue="429,5xx"
            style={{ marginBottom: 8 }}
          >
            <Input size="small" placeholder="429,5xx" style={{ width: 130 }} />
          </Form.Item>
        </Space>
        <Form.Item
          name="nodes"
          label={tr('Nodes（拖动排序，顺序 = 优先级）')}
          rules={[{ required: true, message: tr('至少选择一个 node') }]}
        >
          <RouteNodeList nodes={config.nodes} providers={config.providers} />
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
        { name: v.name, match: v.match, routes: entrypointBindingInputs(v.routes as EntrypointBindingRow[] | undefined) },
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
      <Form form={form} layout="vertical" size="small" initialValues={{ routes: [{}] }}>
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
        <Form.Item
          label={tr('路由绑定')}
          extra={tr('可以先留空作为草稿，之后再在入口面板里绑定路由。')}
          style={{ marginBottom: 0 }}
        >
          <EntrypointBindingsField routes={config.routes} />
        </Form.Item>
      </Form>
    </Modal>
  );
}
