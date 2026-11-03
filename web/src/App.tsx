import { useCallback, useEffect, useMemo, useState } from 'react';
import {
  Badge,
  Button,
  Descriptions,
  Divider,
  Layout,
  Menu,
  Segmented,
  Select,
  Space,
  Tag,
  Typography,
  message,
} from 'antd';
import {
  ApartmentOutlined,
  ApiOutlined,
  DatabaseOutlined,
  PartitionOutlined,
  SettingOutlined,
  SwapOutlined,
  UnorderedListOutlined,
} from '@ant-design/icons';
import { getConfig, getGraph, logout, UnauthorizedError } from './api/client';
import type { ConfigResponse, EdgeMetrics, ErrorRow, GraphMetrics, NodeMetrics } from './api/types';
import { Login } from './pages/Login';
import { AliasesPage } from './pages/Aliases';
import { ProvidersPage } from './pages/Providers';
import { NodesPage } from './pages/Nodes';
import { RequestsPage } from './pages/Requests';
import { RoutesPage } from './pages/Routes';
import { SettingsPage } from './pages/Settings';
import { Edit } from './pages/Edit';
import { Topology, type Selection } from './pages/Topology';

const { Header, Sider, Content } = Layout;

type Mode = 'monitor' | 'edit';
type Page = 'topology' | 'providers' | 'nodes' | 'routes' | 'aliases' | 'requests' | 'settings';
type Session = 'unknown' | 'yes' | 'no';

const RANGES = [
  { label: '最近 15 分钟', value: '15m', ms: 15 * 60_000 },
  { label: '最近 1 小时', value: '1h', ms: 60 * 60_000 },
  { label: '最近 24 小时', value: '24h', ms: 24 * 60 * 60_000 },
];

function fmt(n = 0): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}k`;
  return `${n}`;
}

function rate(m?: { requests: number; errors: number }): string {
  if (!m || m.requests === 0) return '—';
  return `${((m.errors / m.requests) * 100).toFixed(1)}%`;
}

function NodePanel({ id, metrics, errors }: { id: string; metrics?: NodeMetrics; errors: ErrorRow[] }) {
  const [kind, name] = id.split(/:(.*)/s);
  const mine = useMemo(
    () =>
      errors.filter((e) =>
        kind === 'provider'
          ? e.provider === name
          : kind === 'route'
            ? e.route === name
            : kind === 'node'
              ? e.node === name
              : false,
      ),
    [errors, kind, name],
  );

  return (
    <div>
      <Typography.Title level={5} style={{ marginTop: 0 }}>
        {id}
      </Typography.Title>
      <Descriptions size="small" column={1} bordered>
        <Descriptions.Item label="请求">{fmt(metrics?.requests)}</Descriptions.Item>
        <Descriptions.Item label="错误率">{rate(metrics)}</Descriptions.Item>
        <Descriptions.Item label="P50 / P95">
          {fmt(metrics?.p50Ms)}ms / {fmt(metrics?.p95Ms)}ms
        </Descriptions.Item>
        <Descriptions.Item label="tokens">
          入 {fmt(metrics?.inputTokens)} · 出 {fmt(metrics?.outputTokens)} · 缓存{' '}
          {fmt(metrics?.cachedTokens)}
        </Descriptions.Item>
      </Descriptions>

      <Divider orientation="left" plain>
        最近错误
      </Divider>
      {mine.length === 0 ? (
        <Typography.Text type="secondary">最近窗口内没有错误</Typography.Text>
      ) : (
        <Space direction="vertical" size={6} style={{ width: '100%' }}>
          {mine.slice(0, 5).map((e) => (
            <div key={e.requestId} style={{ fontSize: 12 }}>
              <Tag color="red">{e.status || 'ERR'}</Tag>
              <Typography.Text type="secondary">{new Date(e.ts).toLocaleTimeString()}</Typography.Text>{' '}
              {e.message}
            </div>
          ))}
        </Space>
      )}
    </div>
  );
}

function EdgePanel({ id, metrics }: { id: string; metrics?: EdgeMetrics }) {
  const parts = id.split('|');
  return (
    <div>
      <Typography.Title level={5} style={{ marginTop: 0 }}>
        连线
      </Typography.Title>
      <Descriptions size="small" column={1} bordered>
        <Descriptions.Item label="路径">{parts.join(' → ')}</Descriptions.Item>
        <Descriptions.Item label="请求">{fmt(metrics?.requests)}</Descriptions.Item>
        <Descriptions.Item label="错误">{fmt(metrics?.errors)}（{rate(metrics)}）</Descriptions.Item>
      </Descriptions>
    </div>
  );
}

export default function App() {
  const [session, setSession] = useState<Session>('unknown');
  const [config, setConfig] = useState<ConfigResponse | null>(null);
  const [metrics, setMetrics] = useState<GraphMetrics | null>(null);
  const [mode, setMode] = useState<Mode>('monitor');
  const [range, setRange] = useState('1h');
  const [page, setPage] = useState<Page>('topology');
  const [selection, setSelection] = useState<Selection>(null);
  const [stale, setStale] = useState(false);
  const [updatedAt, setUpdatedAt] = useState<string>('');

  const loadConfig = useCallback(async () => {
    try {
      const cfg = await getConfig();
      setConfig(cfg);
      setSession('yes');
    } catch (err) {
      if (err instanceof UnauthorizedError) {
        setSession('no');
      } else {
        message.error(err instanceof Error ? err.message : '加载配置失败');
      }
    }
  }, []);

  useEffect(() => {
    void loadConfig();
  }, [loadConfig]);

  useEffect(() => {
    if (session !== 'yes' || mode !== 'monitor') {
      return;
    }
    let cancelled = false;
    const ms = RANGES.find((r) => r.value === range)?.ms ?? 3_600_000;

    const load = async () => {
      if (document.hidden) return;
      const to = new Date();
      const from = new Date(to.getTime() - ms);
      try {
        const m = await getGraph(from, to);
        if (cancelled) return;
        setMetrics(m);
        setStale(false);
        setUpdatedAt(to.toLocaleTimeString());
      } catch (err) {
        if (err instanceof UnauthorizedError) {
          setSession('no');
          return;
        }
        if (!cancelled) setStale(true);
      }
    };

    void load();
    const timer = window.setInterval(load, 2000);
    const onVisible = () => {
      if (!document.hidden) void load();
    };
    document.addEventListener('visibilitychange', onVisible);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
      document.removeEventListener('visibilitychange', onVisible);
    };
  }, [session, mode, range]);

  useEffect(() => {
    if (mode === 'edit') {
      setMetrics(null);
      setSelection(null);
    }
  }, [mode]);

  if (session === 'unknown') {
    return <div style={{ padding: 48 }}>加载中…</div>;
  }
  if (session === 'no') {
    return (
      <Login
        onSuccess={() => {
          void loadConfig();
        }}
      />
    );
  }

  return (
    <Layout style={{ height: '100vh' }}>
      <Header
        style={{
          display: 'flex',
          alignItems: 'center',
          gap: 16,
          background: '#fff',
          borderBottom: '1px solid #e2e8f0',
          paddingInline: 20,
        }}
      >
        <Typography.Text strong style={{ fontSize: 16 }}>
          llm-gw
        </Typography.Text>
        <Segmented
          value={mode}
          onChange={(v) => setMode(v as Mode)}
          options={[
            { label: '监控', value: 'monitor' },
            { label: '编辑', value: 'edit' },
          ]}
        />
        {mode === 'monitor' ? (
          <Space size={8}>
            <Select
              size="small"
              value={range}
              onChange={setRange}
              options={RANGES.map(({ label, value }) => ({ label, value }))}
              style={{ width: 140 }}
            />
            <Badge
              status={stale ? 'warning' : 'success'}
              text={stale ? '数据延迟' : updatedAt ? `更新 ${updatedAt}` : '等待数据'}
            />
          </Space>
        ) : (
          <Tag color="orange">编辑模式 · 统计已暂停</Tag>
        )}
        <div style={{ flex: 1 }} />
        <Button
          size="small"
          onClick={async () => {
            await logout().catch(() => undefined);
            setSession('no');
            setConfig(null);
          }}
        >
          退出
        </Button>
      </Header>

      <Layout>
        <Sider width={190} style={{ background: '#fff', borderRight: '1px solid #e2e8f0' }}>
          <Menu
            mode="inline"
            selectedKeys={[page]}
            style={{ borderInlineEnd: 'none' }}
            onClick={(e) => setPage(e.key as Page)}
            items={[
              { key: 'topology', icon: <PartitionOutlined />, label: '拓扑' },
              { key: 'providers', icon: <ApiOutlined />, label: 'Providers' },
              { key: 'nodes', icon: <ApartmentOutlined />, label: 'Nodes' },
              { key: 'routes', icon: <SwapOutlined />, label: 'Routes' },
              { key: 'aliases', icon: <UnorderedListOutlined />, label: 'Aliases' },
              { key: 'requests', icon: <DatabaseOutlined />, label: 'Requests' },
              { key: 'settings', icon: <SettingOutlined />, label: 'Settings' },
            ]}
          />
        </Sider>

        <Content style={{ display: 'flex', minWidth: 0 }}>
          <div style={{ flex: 1, minWidth: 0, position: 'relative' }}>
            {page === 'topology' ? (
              mode === 'edit' ? (
                config ? (
                  <Edit config={config} onReload={loadConfig} />
                ) : null
              ) : config ? (
                <Topology config={config} metrics={metrics} onSelect={setSelection} />
              ) : null
            ) : page === 'providers' ? (
              config ? <ProvidersPage config={config} onReload={loadConfig} /> : null
            ) : page === 'nodes' ? (
              config ? <NodesPage config={config} onReload={loadConfig} /> : null
            ) : page === 'routes' ? (
              config ? <RoutesPage config={config} onReload={loadConfig} /> : null
            ) : page === 'aliases' ? (
              config ? <AliasesPage config={config} onReload={loadConfig} /> : null
            ) : page === 'requests' ? (
              <RequestsPage />
            ) : (
              config ? (
                <SettingsPage
                  config={config}
                  onLogout={() => {
                    setSession('no');
                    setConfig(null);
                  }}
                />
              ) : null
            )}
          </div>

          {page === 'topology' && mode === 'monitor' && selection ? (
            <aside
              style={{
                width: 320,
                borderLeft: '1px solid #e2e8f0',
                background: '#fff',
                padding: 16,
                overflow: 'auto',
              }}
            >
              {selection.kind === 'node' ? (
                <NodePanel
                  id={selection.id}
                  metrics={metrics?.nodes[selection.id]}
                  errors={metrics?.errors ?? []}
                />
              ) : (
                <EdgePanel id={selection.id} metrics={metrics?.edges[selection.id]} />
              )}
            </aside>
          ) : null}
        </Content>
      </Layout>
    </Layout>
  );
}
