import { useCallback, useEffect, useMemo, useState, type ReactNode } from 'react';
import {
  Button,
  Descriptions,
  Divider,
  Drawer,
  Dropdown,
  Grid,
  Layout,
  Menu,
  Space,
  Tag,
  Typography,
  message,
} from 'antd';
import {
  ApiOutlined,
  BarChartOutlined,
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
import { getConfig, getHealth, logout, UnauthorizedError } from './api/client';
import type { ConfigResponse, EdgeMetrics, ErrorRow, GraphMetrics, Health, NodeMetrics } from './api/types';
import type { SnapshotMessage } from './api/events';
import { Login } from './pages/Login';
import { EntrypointsPage } from './pages/Entrypoints';
import { ProvidersPage } from './pages/Providers';
import { NodesPage } from './pages/Nodes';
import { RequestsPage } from './pages/Requests';
import { RoutesPage } from './pages/Routes';
import { SettingsPage } from './pages/Settings';
import { Edit } from './pages/Edit';
import { VersionFooter } from './components/VersionFooter';
import { OverviewToolbar } from './components/OverviewToolbar';
import { Topology, type Selection } from './pages/Topology';
import { UsagePage } from './pages/Usage';
import { errorMatchesNodeId } from './pages/selection';
import { useLiveGraph } from './useLiveGraph';
import { useTheme, type ThemeMode } from './theme';
import { LANGUAGES, translate, useI18n, type Lang } from './i18n';
import { NAV_LABELS, NAV_ORDER, type Page } from './nav';
import { formatDateTime, formatDurationMs, formatTokenRate } from './time';

const { Header, Content } = Layout;

const NAV_ICONS: Record<Page, ReactNode> = {
  topology: <PartitionOutlined />,
  usage: <BarChartOutlined />,
  providers: <CloudServerOutlined />,
  nodes: <ApiOutlined />,
  routes: <BranchesOutlined />,
  entrypoints: <TagOutlined />,
  requests: <DatabaseOutlined />,
  settings: <SettingOutlined />,
};

const NAV_ITEMS = NAV_ORDER.map((key) => ({ key, icon: NAV_ICONS[key], label: NAV_LABELS[key] }));

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
type Session = 'unknown' | 'yes' | 'no';

const RANGES = [
  { label: '1m', value: '1m', ms: 60_000 },
  { label: '5m', value: '5m', ms: 5 * 60_000 },
  { label: '15m', value: '15m', ms: 15 * 60_000 },
  { label: '30m', value: '30m', ms: 30 * 60_000 },
  { label: '1h', value: '1h', ms: 60 * 60_000 },
  { label: '6h', value: '6h', ms: 6 * 60 * 60_000 },
  { label: '12h', value: '12h', ms: 12 * 60 * 60_000 },
  { label: '24h', value: '24h', ms: 24 * 60 * 60_000 },
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

export function NodePanel({ id, metrics, errors }: { id: string; metrics?: NodeMetrics; errors: ErrorRow[] }) {
  const mine = useMemo(() => errors.filter((e) => errorMatchesNodeId(id, e)), [errors, id]);

  return (
    <div>
      <Typography.Title level={5} style={{ marginTop: 0 }}>
        {id}
      </Typography.Title>
      <Descriptions size="small" column={1} bordered>
        <Descriptions.Item label={translate('请求')}>{fmt(metrics?.requests)}</Descriptions.Item>
        {metrics && metrics.errors > 0 ? (
          <Descriptions.Item label={translate('错误率')}>
            <span style={{ color: '#dc2626' }}>{rate(metrics)}</span>
          </Descriptions.Item>
        ) : null}
        <Descriptions.Item label="P50 / P95">
          {formatDurationMs(metrics?.p50Ms)} / {formatDurationMs(metrics?.p95Ms)}
        </Descriptions.Item>
        {id.startsWith('node:') && (metrics?.outputTokensPerSecond ?? 0) > 0 ? (
          <Descriptions.Item label={translate('输出速度')}>
            {formatTokenRate(metrics!.outputTokensPerSecond)}
          </Descriptions.Item>
        ) : null}
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
              <Typography.Text type="secondary">{formatDateTime(e.ts)}</Typography.Text>{' '}
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
        {metrics && metrics.errors > 0 ? (
          <Descriptions.Item label={translate('错误')}>
            <span style={{ color: '#dc2626' }}>{fmt(metrics.errors)}（{rate(metrics)}）</span>
          </Descriptions.Item>
        ) : null}
      </Descriptions>
    </div>
  );
}

export function Sidebar({
  page,
  onPage,
  themeMode,
  onThemeMode,
  onLogout,
  health,
}: {
  page: Page;
  onPage: (page: Page) => void;
  themeMode: ThemeMode;
  onThemeMode: (mode: ThemeMode) => void;
  onLogout: () => void;
  health: Health | null;
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
            <Button block icon={themeIcon(themeMode)} style={{ justifyContent: 'center' }}>
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
            <Button block icon={<GlobalOutlined />} style={{ justifyContent: 'center' }}>
              {languageLabel}
            </Button>
          </Dropdown>
          <Button block onClick={onLogout} style={{ justifyContent: 'center' }}>
            {t('退出')}
          </Button>
          <VersionFooter health={health} />
        </Space>
      </div>
    </div>
  );
}

export default function App() {
  const [session, setSession] = useState<Session>('unknown');
  const [config, setConfig] = useState<ConfigResponse | null>(null);
  const [health, setHealth] = useState<Health | null>(null);
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
  const [since, setSince] = useState<string>('');
  const [resetKey, setResetKey] = useState(0);
  const [sessionKey, setSessionKey] = useState(0);

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

  useEffect(() => {
    let cancelled = false;
    void getHealth()
      .then((next) => {
        if (!cancelled) setHealth(next);
      })
      .catch(() => undefined);
    return () => {
      cancelled = true;
    };
  }, []);

  const handleLogout = useCallback(async () => {
    await logout().catch(() => undefined);
    setSession('no');
    setConfig(null);
    setMetrics(null);
    setSince('');
    setUpdatedAt('');
    setStale(false);
    setSessionKey((key) => key + 1);
  }, []);

  useEffect(() => {
    if (session !== 'yes') setNavOpen(false);
  }, [session]);

  const handleSnapshot = useCallback((msg: SnapshotMessage) => {
    setMetrics(msg.metrics);
    setSince(msg.since);
    setUpdatedAt(formatDateTime(msg.to));
    setStale(false);
  }, []);

  const handleStatus = useCallback((connected: boolean) => {
    setStale(!connected);
  }, []);

  const handleAuthError = useCallback(() => {
    setSession('no');
    setMetrics(null);
    setSince('');
    setUpdatedAt('');
    setStale(false);
    setSessionKey((key) => key + 1);
  }, []);

  const handleReset = useCallback(() => {
    setMetrics({ nodes: {}, edges: {}, errors: [] });
    setSince('');
    setUpdatedAt('');
    setResetKey((key) => key + 1);
  }, []);

  useLiveGraph({
    enabled: session === 'yes' && mode === 'monitor' && page === 'topology',
    range,
    resetKey,
    sessionKey,
    onSnapshot: handleSnapshot,
    onStatus: handleStatus,
    onAuthError: handleAuthError,
  });

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
            health={health}
          />
        </Drawer>

        <Content style={{ display: 'flex', minWidth: 0 }}>
          <div style={{ flex: 1, minWidth: 0, height: '100%', display: 'flex', flexDirection: 'column' }}>
            {page === 'topology' ? (
              <div style={{ display: 'flex', flexDirection: 'column', height: '100%' }}>
                <OverviewToolbar
                  mode={mode}
                  onMode={setMode}
                  range={range}
                  onRange={setRange}
                  ranges={RANGES}
                  stale={stale}
                  updatedAt={updatedAt}
                  sinceAt={since ? formatDateTime(since) : ''}
                  onReset={handleReset}
                  isMobile={isMobile}
                />
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
            ) : page === 'usage' ? (
              <UsagePage />
            ) : page === 'providers' ? (
              config ? <ProvidersPage config={config} onReload={loadConfig} /> : null
            ) : page === 'nodes' ? (
              config ? <NodesPage config={config} onReload={loadConfig} /> : null
            ) : page === 'routes' ? (
              config ? <RoutesPage config={config} onReload={loadConfig} /> : null
            ) : page === 'entrypoints' ? (
              config ? <EntrypointsPage config={config} onReload={loadConfig} /> : null
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
