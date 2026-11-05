import { useCallback, useEffect, useMemo, useState } from 'react';
import {
  Badge,
  Button,
  Descriptions,
  Divider,
  Drawer,
  Dropdown,
  Grid,
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
  ApiOutlined,
  BranchesOutlined,
  CloudServerOutlined,
  DatabaseOutlined,
  DesktopOutlined,
  GlobalOutlined,
  MenuOutlined,
  MoonOutlined,
  PartitionOutlined,
  SettingOutlined,
  SunOutlined,
  TagOutlined,
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
import { useTheme, type ThemeMode } from './theme';
import { LANGUAGES, translate, useI18n, type Lang } from './i18n';

const { Header, Content } = Layout;

const NAV_ITEMS = [
  { key: 'topology', icon: <PartitionOutlined />, label: '拓扑' },
  { key: 'providers', icon: <CloudServerOutlined />, label: '提供商' },
  { key: 'nodes', icon: <ApiOutlined />, label: '节点' },
  { key: 'routes', icon: <BranchesOutlined />, label: '路由' },
  { key: 'aliases', icon: <TagOutlined />, label: '别名' },
  { key: 'requests', icon: <DatabaseOutlined />, label: '请求' },
  { key: 'settings', icon: <SettingOutlined />, label: '设置' },
];

const THEME_ITEMS = [
  { key: 'system' as const, icon: <DesktopOutlined />, label: '跟随系统' },
  { key: 'light' as const, icon: <SunOutlined />, label: '浅色' },
  { key: 'dark' as const, icon: <MoonOutlined />, label: '深色' },
];

function themeIcon(mode: ThemeMode) {
  if (mode === 'light') return <SunOutlined />;
  if (mode === 'dark') return <MoonOutlined />;
  return <DesktopOutlined />;
}

type Mode = 'monitor' | 'edit';
type Page = 'topology' | 'providers' | 'nodes' | 'routes' | 'aliases' | 'requests' | 'settings';
type Session = 'unknown' | 'yes' | 'no';

const RANGES = [
  { label: '最近 1 分钟', value: '1m', ms: 60_000 },
  { label: '最近 5 分钟', value: '5m', ms: 5 * 60_000 },
  { label: '最近 15 分钟', value: '15m', ms: 15 * 60_000 },
  { label: '最近 30 分钟', value: '30m', ms: 30 * 60_000 },
  { label: '最近 1 小时', value: '1h', ms: 60 * 60_000 },
  { label: '最近 6 小时', value: '6h', ms: 6 * 60 * 60_000 },
  { label: '最近 12 小时', value: '12h', ms: 12 * 60 * 60_000 },
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
        <Descriptions.Item label={translate('请求')}>{fmt(metrics?.requests)}</Descriptions.Item>
        <Descriptions.Item label={translate('错误率')}>{rate(metrics)}</Descriptions.Item>
        <Descriptions.Item label="P50 / P95">
          {fmt(metrics?.p50Ms)}ms / {fmt(metrics?.p95Ms)}ms
        </Descriptions.Item>
        <Descriptions.Item label="tokens">
          {translate('入 {input} · 出 {output} · 缓存 {cached}', {
            input: fmt(metrics?.inputTokens),
            output: fmt(metrics?.outputTokens),
            cached: fmt(metrics?.cachedTokens),
          })}
        </Descriptions.Item>
      </Descriptions>

      <Divider orientation="left" plain>
        {translate('最近错误')}
      </Divider>
      {mine.length === 0 ? (
        <Typography.Text type="secondary">{translate('最近窗口内没有错误')}</Typography.Text>
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
        {translate('连线')}
      </Typography.Title>
      <Descriptions size="small" column={1} bordered>
        <Descriptions.Item label={translate('路径')}>{parts.join(' → ')}</Descriptions.Item>
        <Descriptions.Item label={translate('请求')}>{fmt(metrics?.requests)}</Descriptions.Item>
        <Descriptions.Item label={translate('错误')}>
          {fmt(metrics?.errors)}（{rate(metrics)}）
        </Descriptions.Item>
      </Descriptions>
    </div>
  );
}

function Sidebar({
  page,
  onPage,
  themeMode,
  onThemeMode,
  onLogout,
}: {
  page: Page;
  onPage: (page: Page) => void;
  themeMode: ThemeMode;
  onThemeMode: (mode: ThemeMode) => void;
  onLogout: () => void;
}) {
  const { t, lang, setLang } = useI18n();
  const themeLabel = t(THEME_ITEMS.find((item) => item.key === themeMode)?.label ?? '跟随系统');
  const themeItems = THEME_ITEMS.map((item) => ({ ...item, label: t(item.label) }));
  const navItems = NAV_ITEMS.map((item) => ({ ...item, label: t(item.label) }));
  const languageLabel = LANGUAGES.find((item) => item.id === lang)?.label ?? 'English';
  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100%' }}>
      <div style={{ flex: 1, minHeight: 0, overflowY: 'auto' }}>
        <Menu
          mode="inline"
          selectedKeys={[page]}
          style={{ borderInlineEnd: 'none', background: 'transparent' }}
          onClick={(e) => onPage(e.key as Page)}
          items={navItems}
        />
      </div>
      <div style={{ borderTop: '1px solid var(--gw-border)', padding: 12 }}>
        <Space direction="vertical" size={8} style={{ width: '100%' }}>
          <Dropdown
            trigger={['click']}
            placement="topLeft"
            menu={{ items: themeItems, selectable: true, selectedKeys: [themeMode], onClick: ({ key }) => onThemeMode(key as ThemeMode) }}
          >
            <Button block icon={themeIcon(themeMode)} style={{ justifyContent: 'flex-start' }}>
              {themeLabel}
            </Button>
          </Dropdown>
          <Dropdown
            trigger={['click']}
            placement="topLeft"
            menu={{
              items: LANGUAGES.map((item) => ({ key: item.id, label: item.label })),
              selectable: true,
              selectedKeys: [lang],
              onClick: ({ key }) => setLang(key as Lang),
            }}
          >
            <Button block icon={<GlobalOutlined />} style={{ justifyContent: 'flex-start' }}>
              {languageLabel}
            </Button>
          </Dropdown>
          <Button block onClick={onLogout}>
            {t('退出')}
          </Button>
        </Space>
      </div>
    </div>
  );
}

export default function App() {
  const [session, setSession] = useState<Session>('unknown');
  const [config, setConfig] = useState<ConfigResponse | null>(null);
  const [metrics, setMetrics] = useState<GraphMetrics | null>(null);
  const [mode, setMode] = useState<Mode>('monitor');
  const [range, setRange] = useState('1h');
  const { mode: themeMode, setMode: setThemeMode } = useTheme();
  const { t } = useI18n();
  const screens = Grid.useBreakpoint();
  const isMobile = !screens.md;
  const [navOpen, setNavOpen] = useState(false);
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
        message.error(err instanceof Error ? err.message : t('加载配置失败'));
      }
    }
  }, []);

  useEffect(() => {
    void loadConfig();
  }, [loadConfig]);

  const handleLogout = useCallback(async () => {
    await logout().catch(() => undefined);
    setSession('no');
    setConfig(null);
  }, []);

  useEffect(() => {
    if (session !== 'yes') setNavOpen(false);
  }, [session]);

  useEffect(() => {
    if (session !== 'yes' || mode !== 'monitor' || page !== 'topology') {
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
  }, [session, mode, range, page]);

  useEffect(() => {
    if (mode === 'edit') {
      setMetrics(null);
      setSelection(null);
    }
  }, [mode]);

  if (session === 'unknown') {
    return <div style={{ padding: 48 }}>{t('加载中…')}</div>;
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
    <Layout style={{ height: '100vh', background: 'var(--gw-bg)' }}>
      <Header
        style={{
          display: 'flex',
          alignItems: 'center',
          gap: isMobile ? 8 : 16,
          height: 'auto',
          lineHeight: 'normal',
          paddingBlock: 8,
          background: 'var(--gw-surface)',
          borderBottom: '1px solid var(--gw-border)',
          paddingInline: isMobile ? 12 : 20,
        }}
      >
        <Button
          icon={<MenuOutlined />}
          onClick={() => setNavOpen(true)}
          aria-label="menu"
          aria-expanded={navOpen}
        />
        <Typography.Text strong style={{ fontSize: 16 }}>
          llm-gw
        </Typography.Text>
      </Header>

      <Layout style={{ background: 'var(--gw-bg)' }}>
        <Drawer
          placement="left"
          open={navOpen}
          onClose={() => setNavOpen(false)}
          width={230}
          styles={{ body: { padding: 0 } }}
        >
          <Sidebar
            page={page}
            onPage={(next) => {
              setPage(next);
              setNavOpen(false);
            }}
            themeMode={themeMode}
            onThemeMode={setThemeMode}
            onLogout={() => void handleLogout()}
          />
        </Drawer>

        <Content style={{ display: 'flex', minWidth: 0 }}>
          <div style={{ flex: 1, minWidth: 0, height: '100%', display: 'flex', flexDirection: 'column' }}>
            {page === 'topology' ? (
              <div style={{ display: 'flex', flexDirection: 'column', height: '100%' }}>
                <div
                  style={{
                    display: 'flex',
                    flexWrap: 'wrap',
                    alignItems: 'center',
                    gap: 8,
                    padding: '8px 12px',
                    background: 'var(--gw-surface)',
                    borderBottom: '1px solid var(--gw-border)',
                  }}
                >
                  <Segmented
                    value={mode}
                    onChange={(v) => setMode(v as Mode)}
                    options={[
                      { label: t('监控'), value: 'monitor' },
                      { label: t('编辑'), value: 'edit' },
                    ]}
                  />
                  {mode === 'monitor' ? (
                    <Space wrap size={8}>
                      <Select
                        size="small"
                        value={range}
                        onChange={setRange}
                        options={RANGES.map(({ label, value }) => ({ label: t(label), value }))}
                        style={{ width: 140 }}
                      />
                      {!isMobile ? (
                        <Badge
                          status={stale ? 'warning' : 'success'}
                          text={stale ? t('数据延迟') : updatedAt ? t('更新 {time}', { time: updatedAt }) : t('等待数据')}
                        />
                      ) : null}
                    </Space>
                  ) : (
                    <Tag color="orange">{t('编辑模式 · 统计已暂停')}</Tag>
                  )}
                </div>
                <div style={{ flex: 1, minHeight: 0, position: 'relative' }}>
                  {mode === 'edit' ? (
                    config ? (
                      <Edit config={config} onReload={loadConfig} />
                    ) : null
                  ) : config ? (
                    <Topology config={config} metrics={metrics} onSelect={setSelection} />
                  ) : null}
                </div>
              </div>
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
                  onLogout={() => void handleLogout()}
                />
              ) : null
            )}
          </div>

          {page === 'topology' && mode === 'monitor' && selection ? (
            isMobile ? (
              <Drawer
                placement="right"
                open
                onClose={() => setSelection(null)}
                width="92%"
                styles={{ body: { padding: 16 } }}
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
              </Drawer>
            ) : (
              <aside
                style={{
                  width: 320,
                  borderLeft: '1px solid var(--gw-border)',
                  background: 'var(--gw-surface)',
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
            )
          ) : null}
        </Content>
      </Layout>
    </Layout>
  );
}
