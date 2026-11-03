import { useMemo, useState } from 'react';
import {
  Alert,
  AutoComplete,
  Button,
  Divider,
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
  putAliases,
  reorderRoutes,
  updateProvider,
  updateNode,
  updateRoute,
} from '../api/client';
import type { Alias, ConfigResponse, Node as BindNode, Preset, Provider, Route } from '../api/types';
import { Topology, type Selection } from './Topology';

interface Props {
  config: ConfigResponse;
  onReload: () => Promise<void>;
}

function splitId(id: string): [string, string] {
  const i = id.indexOf(':');
  if (i < 0) return [id, ''];
  return [id.slice(0, i), id.slice(i + 1)];
}

function logicalModels(config: ConfigResponse): string[] {
  return config.routes.map((r) => r.match?.model || '').filter(Boolean);
}

function parseJSON<T>(text: string, fallback: T): T {
  const s = text.trim();
  if (!s) return fallback;
  return JSON.parse(s) as T;
}

function errorText(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

// ---------------------------------------------------------------- forms

export function ProviderForm({ provider, onSaved }: { provider: Provider; onSaved: () => Promise<void> }) {
  const [busy, setBusy] = useState(false);
  const [form] = Form.useForm();

  const save = async () => {
    const v = await form.validateFields();
    setBusy(true);
    try {
      const payload: Record<string, unknown> = {
        name: provider.name,
        baseURL: v.baseURL,
        protocol: v.protocol,
        pathMap: parseJSON(v.pathMap, {}),
        auth: provider.auth ?? {},
        health: provider.health ?? {},
        tls: provider.tls ?? {},
        enabled: v.enabled,
      };
      const keys = String(v.keys ?? '')
        .split('\n')
        .map((s) => s.trim())
        .filter(Boolean);
      if (keys.length > 0) payload.keys = keys;
      await updateProvider(provider.name, payload);
      message.success('已保存');
      await onSaved();
    } catch (err) {
      if (err instanceof SyntaxError) message.error(`JSON 格式错误：${err.message}`);
      else message.error(errorText(err));
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

  return (
    <Form
      form={form}
      layout="vertical"
      size="small"
      initialValues={{
        baseURL: provider.baseURL,
        protocol: provider.protocol,
        keys: '',
        pathMap: JSON.stringify(provider.pathMap ?? {}, null, 2),
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
      <Form.Item name="baseURL" label="baseURL" rules={[{ required: true }]}>
        <Input />
      </Form.Item>
      <Form.Item name="protocol" label="protocol">
        <Select options={['openai', 'anthropic', 'gemini'].map((v) => ({ value: v, label: v }))} />
      </Form.Item>
      <Form.Item name="keys" label="keys（留空保持不变，每行一个）">
        <Input.TextArea rows={2} placeholder="sk-..." />
      </Form.Item>
      <Form.Item name="pathMap" label="pathMap (JSON)">
        <Input.TextArea rows={3} />
      </Form.Item>
      <Form.Item name="enabled" label="enabled" valuePropName="checked">
        <Switch size="small" />
      </Form.Item>
      <Typography.Paragraph type="secondary" style={{ fontSize: 12 }}>
        模型映射与 backup/weight 在 Node 上配置；同一个 Provider 可以被多个 Node 引用。
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
    label: `${n.name} (${n.provider}${n.tier ? ` · ${n.tier}` : ''})`,
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
        tier: v.tier ?? '',
        backup: v.backup,
        weight: v.weight,
        enabled: v.enabled,
        models: parseJSON(v.models, {}),
        effortModels: parseJSON(v.effortModels, {}),
      });
      message.success('已保存');
      await onSaved();
    } catch (err) {
      if (err instanceof SyntaxError) message.error(`JSON 格式错误：${err.message}`);
      else message.error(errorText(err));
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

  return (
    <Form
      form={form}
      layout="vertical"
      size="small"
      initialValues={{
        provider: node.provider,
        tier: node.tier || '',
        models: JSON.stringify(node.models ?? {}, null, 2),
        effortModels: JSON.stringify(node.effortModels ?? {}, null, 2),
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
      <Form.Item name="tier" label="effort tier">
        <Select
          allowClear
          options={[
            { value: 'high', label: 'high' },
            { value: 'medium', label: 'medium' },
            { value: 'low', label: 'low' },
          ]}
          placeholder="base（默认池）"
        />
      </Form.Item>
      <Form.Item name="models" label="models（逻辑模型 → 供应商模型）">
        <Input.TextArea rows={4} />
      </Form.Item>
      <Form.Item name="effortModels" label="effortModels (JSON)">
        <Input.TextArea rows={2} />
      </Form.Item>
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
          {node ? `（${node.provider}${node.tier ? ` · ${node.tier}` : ''}）` : ''}
        </Typography.Paragraph>
        <Typography.Paragraph type="secondary" style={{ fontSize: 12 }}>
          tier、模型映射和 backup/weight 都在 Node 面板里改。
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
        const index = Number(sname);
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
      const i = Number(name);
      const a = config.aliases[i];
      return a ? <AliasForm key={i} alias={a} index={i} config={config} onSaved={onReload} /> : null;
    }
    return null;
  }, [selection, config, onReload]);

  return (
    <div style={{ display: 'flex', width: '100%', height: '100%' }}>
      <div style={{ flex: 1, minWidth: 0, position: 'relative' }}>
        <div style={{ position: 'absolute', top: 12, left: 12, zIndex: 5 }}>
          <Space>
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

      <aside style={{ width: 340, borderLeft: '1px solid #e2e8f0', background: '#fff', padding: 16, overflow: 'auto' }}>
        {panel}
      </aside>

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
    const preset = presets.find((p) => p.name === v.preset);
    setBusy(true);
    try {
      const keys = String(v.keys ?? '')
        .split('\n')
        .map((s) => s.trim())
        .filter(Boolean);
      await createProvider({
        name: v.name,
        baseURL: preset?.baseURL ?? v.baseURL,
        protocol: preset?.protocol ?? v.protocol,
        pathMap: preset?.pathMap ?? {},
        auth: preset?.auth ?? {},
        health: preset?.healthPath ? { path: preset.healthPath } : {},
        keys,
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
      <Form form={form} layout="vertical" size="small">
        <Form.Item name="preset" label="预设">
          <Select
            allowClear
            options={presets.map((p) => ({ value: p.name, label: `${p.name} (${p.wire})` }))}
            placeholder="可选"
          />
        </Form.Item>
        <Form.Item name="name" label="名称" rules={[{ required: true }]}>
          <Input placeholder="opencode-go" />
        </Form.Item>
        <Form.Item name="baseURL" label="baseURL（未选预设时必填）">
          <Input placeholder="https://api.example.com" />
        </Form.Item>
        <Form.Item name="protocol" label="protocol">
          <Select options={['openai', 'anthropic', 'gemini'].map((v) => ({ value: v, label: v }))} />
        </Form.Item>
        <Form.Item name="keys" label="keys（每行一个）">
          <Input.TextArea rows={2} />
        </Form.Item>
        <Typography.Paragraph type="secondary" style={{ fontSize: 12 }}>
          创建后在画布上为它建 Node，再在 Node 上配置模型映射。
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
        tier: v.tier ?? '',
        models: parseJSON(v.models, {}),
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

  return (
    <Modal open={open} title="添加 Node" onCancel={onClose} onOk={submit} confirmLoading={busy} destroyOnClose>
      <Form form={form} layout="vertical" size="small">
        <Form.Item name="name" label="名称" rules={[{ required: true }]}>
          <Input placeholder="deepseek-fast" />
        </Form.Item>
        <Form.Item name="provider" label="provider" rules={[{ required: true }]}>
          <Select options={providerOptions} />
        </Form.Item>
        <Form.Item name="tier" label="effort tier">
          <Select
            allowClear
            options={[
              { value: 'high', label: 'high' },
              { value: 'medium', label: 'medium' },
              { value: 'low', label: 'low' },
            ]}
            placeholder="base（默认池）"
          />
        </Form.Item>
        <Form.Item name="models" label="models（逻辑模型 → 供应商模型）">
          <Input.TextArea rows={3} placeholder='{"fast":"deepseek-v4-flash","*":"deepseek-v4-flash"}' />
        </Form.Item>
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
