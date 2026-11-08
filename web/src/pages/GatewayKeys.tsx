import { useCallback, useEffect, useState } from 'react';
import {
  Alert,
  Button,
  Drawer,
  Input,
  Modal,
  Popconfirm,
  Segmented,
  Space,
  Switch,
  Tag,
  Typography,
  message,
} from 'antd';
import { PlusOutlined, ReloadOutlined } from '@ant-design/icons';
import {
  createGatewayKey,
  deleteGatewayKey,
  getGatewayKeyUsage,
  getRequests,
  listGatewayKeys,
  rotateGatewayKey,
  updateGatewayKey,
} from '../api/client';
import type {
  GatewayKey,
  GatewayKeyUsage,
  GatewayKeyUsageResponse,
  RequestRow,
} from '../api/types';
import { ListCard } from '../components/ListCard';
import { ListPage } from '../components/ListPage';
import { formatCount } from '../format';
import { translate as t } from '../i18n';
import { formatDateTime } from '../time';

const RANGES = [
  { label: '24 小时', value: 24 * 3_600_000 },
  { label: '7 天', value: 7 * 24 * 3_600_000 },
  { label: '30 天', value: 30 * 24 * 3_600_000 },
];

const SESSION_WINDOW_MS = 24 * 3_600_000;

/** Classify the configured key list for the page-level alerts. */
export function gatewayKeyState(keys: Pick<GatewayKey, 'enabled'>[]): 'none' | 'all-disabled' | 'ok' {
  if (keys.length === 0) return 'none';
  return keys.some((key) => key.enabled) ? 'ok' : 'all-disabled';
}

function usageLine(usage?: GatewayKeyUsage): string | undefined {
  if (!usage) return undefined;
  return [
    `${t('会话')} ${formatCount(usage.requests)}`,
    `${t('错误')} ${formatCount(usage.errors)}`,
    `${t('输入')} ${formatCount(usage.inputTokens)}`,
    `${t('输出')} ${formatCount(usage.outputTokens)}`,
    `${t('缓存')} ${formatCount(usage.cachedTokens)}`,
  ].join(' · ');
}

export function GatewayKeyCard({
  item,
  usage,
  onSessions,
  onToggle,
  onRotate,
  onDelete,
}: {
  item: GatewayKey;
  usage?: GatewayKeyUsage;
  onSessions?: () => void;
  onToggle?: (enabled: boolean) => void;
  onRotate?: () => void;
  onDelete?: () => void;
}) {
  const historical = usage?.configured === false;
  const line = usageLine(usage);
  const preview = item.keyPreview || usage?.keyPreview || '—';

  return (
    <ListCard
      title={
        <>
          {item.name}
          {historical ? <Tag style={{ marginInlineStart: 8 }}>{t('已删除')}</Tag> : null}
          {!historical && !item.enabled ? (
            <Tag style={{ marginInlineStart: 8 }}>{t('已禁用')}</Tag>
          ) : null}
        </>
      }
      subtitle={
        <>
          <Typography.Text code>{preview}</Typography.Text>
          {line ? <span> · {line}</span> : null}
        </>
      }
      tags={
        historical ? undefined : (
          <Switch
            checked={item.enabled}
            aria-label={t('启用 {name}', { name: item.name })}
            onChange={(checked) => onToggle?.(checked)}
          />
        )
      }
      actions={
        historical ? undefined : (
          <>
            <Button size="small" onClick={onSessions}>
              {t('会话')}
            </Button>
            {onRotate ? (
              <Popconfirm
                title={t('轮换 {name} 的密钥?', { name: item.name })}
                onConfirm={onRotate}
              >
                <Button size="small">{t('轮换')}</Button>
              </Popconfirm>
            ) : null}
            {onDelete ? (
              <Popconfirm
                title={t('删除网关密钥 {name}?', { name: item.name })}
                onConfirm={onDelete}
              >
                <Button size="small" danger>
                  {t('删除')}
                </Button>
              </Popconfirm>
            ) : null}
          </>
        )
      }
    />
  );
}

/** Body of the one-time secret dialog; rendered directly by SSR tests. */
export function GatewaySecretContent({ secret }: { secret: { name: string; key: string } }) {
  return (
    <>
      <Alert
        type="warning"
        showIcon
        message={t('该密钥只显示一次，请立即复制保存。')}
        style={{ marginBottom: 12 }}
      />
      <Typography.Paragraph strong style={{ marginBottom: 4 }}>
        {secret.name}
      </Typography.Paragraph>
      <Typography.Paragraph copyable={{ text: secret.key }} code style={{ wordBreak: 'break-all' }}>
        {secret.key}
      </Typography.Paragraph>
    </>
  );
}

export function GatewaySecretDialog({
  secret,
  onClose,
}: {
  secret: { name: string; key: string } | null;
  onClose: () => void;
}) {
  return (
    <Modal
      title={t('密钥已生成')}
      open={!!secret}
      onCancel={onClose}
      onOk={onClose}
      okText={t('关闭')}
      cancelButtonProps={{ style: { display: 'none' } }}
      destroyOnClose
    >
      {secret ? <GatewaySecretContent secret={secret} /> : null}
    </Modal>
  );
}

export function GatewayKeysPage() {
  const [rangeMs, setRangeMs] = useState(RANGES[0].value);
  const [keys, setKeys] = useState<GatewayKey[]>([]);
  const [usage, setUsage] = useState<GatewayKeyUsageResponse | null>(null);
  const [keysError, setKeysError] = useState('');
  const [usageError, setUsageError] = useState('');
  const [loaded, setLoaded] = useState(false);
  const [loading, setLoading] = useState(false);
  const [addOpen, setAddOpen] = useState(false);
  const [addName, setAddName] = useState('');
  const [addEnabled, setAddEnabled] = useState(true);
  const [addBusy, setAddBusy] = useState(false);
  const [secret, setSecret] = useState<{ name: string; key: string } | null>(null);
  const [sessionsKey, setSessionsKey] = useState('');
  const [sessions, setSessions] = useState<RequestRow[]>([]);
  const [sessionsLoading, setSessionsLoading] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    const to = new Date();
    const from = new Date(to.getTime() - rangeMs);
    const [keyResult, usageResult] = await Promise.allSettled([
      listGatewayKeys(),
      getGatewayKeyUsage({ from, to }),
    ]);

    if (keyResult.status === 'fulfilled') {
      setKeys(keyResult.value);
      setKeysError('');
    } else {
      const detail =
        keyResult.reason instanceof Error ? keyResult.reason.message : String(keyResult.reason);
      setKeysError(detail);
      message.error(detail);
    }

    if (usageResult.status === 'fulfilled') {
      setUsage(usageResult.value);
      setUsageError('');
    } else {
      setUsageError(
        usageResult.reason instanceof Error
          ? usageResult.reason.message
          : String(usageResult.reason),
      );
    }

    setLoaded(true);
    setLoading(false);
  }, [rangeMs]);

  useEffect(() => {
    void load();
  }, [load]);

  const openSessions = useCallback(async (name: string) => {
    setSessionsKey(name);
    setSessions([]);
    setSessionsLoading(true);
    try {
      const to = new Date();
      const from = new Date(to.getTime() - SESSION_WINDOW_MS);
      setSessions(await getRequests({ from, to, gatewayKey: name, limit: 100 }));
    } catch (err) {
      message.error(err instanceof Error ? err.message : String(err));
    } finally {
      setSessionsLoading(false);
    }
  }, []);

  const submitAdd = async () => {
    const name = addName.trim();
    if (!name) {
      message.error(t('必填'));
      return;
    }
    setAddBusy(true);
    try {
      const created = await createGatewayKey({ name, enabled: addEnabled });
      setAddOpen(false);
      setAddName('');
      setAddEnabled(true);
      setSecret(created);
      await load();
    } catch (err) {
      message.error(err instanceof Error ? err.message : String(err));
    } finally {
      setAddBusy(false);
    }
  };

  const toggle = async (name: string, enabled: boolean) => {
    try {
      await updateGatewayKey(name, { enabled });
      await load();
    } catch (err) {
      message.error(err instanceof Error ? err.message : String(err));
    }
  };

  const rotate = async (name: string) => {
    try {
      setSecret(await rotateGatewayKey(name));
      await load();
    } catch (err) {
      message.error(err instanceof Error ? err.message : String(err));
    }
  };

  const remove = async (name: string) => {
    try {
      await deleteGatewayKey(name);
      message.success(t('已删除'));
      await load();
    } catch (err) {
      message.error(err instanceof Error ? err.message : String(err));
    }
  };

  const state = gatewayKeyState(keys);
  const usageByName = new Map((usage?.keys ?? []).map((item) => [item.name, item]));
  const historical = (usage?.keys ?? []).filter((item) => !item.configured);

  return (
    <ListPage>
      <Space wrap style={{ width: '100%', justifyContent: 'space-between', marginBottom: 12 }}>
        <Space wrap>
          <Typography.Title level={4} style={{ margin: 0 }}>
            {t('网关密钥')}
          </Typography.Title>
          <Segmented
            value={rangeMs}
            onChange={(value) => setRangeMs(Number(value))}
            options={RANGES.map((r) => ({ ...r, label: t(r.label) }))}
          />
          <Button icon={<ReloadOutlined />} loading={loading} onClick={() => void load()}>
            {t('刷新')}
          </Button>
        </Space>
        <Button type="primary" icon={<PlusOutlined />} onClick={() => setAddOpen(true)}>
          {t('添加网关密钥')}
        </Button>
      </Space>

      {loaded && !keysError && state === 'none' ? (
        <Alert
          type="info"
          showIcon
          style={{ marginBottom: 12 }}
          message={t('未配置网关密钥，数据面当前不鉴权')}
          action={
            <Button size="small" onClick={() => setAddOpen(true)}>
              {t('添加网关密钥')}
            </Button>
          }
        />
      ) : null}
      {loaded && !keysError && state === 'all-disabled' ? (
        <Alert
          type="warning"
          showIcon
          style={{ marginBottom: 12 }}
          message={t('所有网关密钥均已禁用，所有数据面请求都会被拒绝')}
        />
      ) : null}
      {keysError ? (
        <Alert
          type="error"
          showIcon
          style={{ marginBottom: 12 }}
          message={t('加载失败')}
          description={keysError}
        />
      ) : null}
      {usageError ? (
        <Alert
          type="warning"
          showIcon
          style={{ marginBottom: 12 }}
          message={t('加载失败')}
          description={usageError}
        />
      ) : null}

      {keys.map((item) => (
        <GatewayKeyCard
          key={item.name}
          item={item}
          usage={usageByName.get(item.name)}
          onSessions={() => void openSessions(item.name)}
          onToggle={(enabled) => void toggle(item.name, enabled)}
          onRotate={() => void rotate(item.name)}
          onDelete={() => void remove(item.name)}
        />
      ))}
      {historical.map((item) => (
        <GatewayKeyCard
          key={`history-${item.name}`}
          item={{ name: item.name, keyPreview: item.keyPreview ?? '', enabled: false }}
          usage={item}
        />
      ))}

      <Modal
        title={t('添加网关密钥')}
        open={addOpen}
        onCancel={() => setAddOpen(false)}
        onOk={() => void submitAdd()}
        confirmLoading={addBusy}
        okText={t('添加')}
        destroyOnClose
      >
        <Space direction="vertical" size={12} style={{ width: '100%' }}>
          <Input
            placeholder={t('名称')}
            aria-label={t('名称')}
            value={addName}
            onChange={(e) => setAddName(e.target.value)}
            onPressEnter={() => void submitAdd()}
          />
          <Space>
            <Switch
              checked={addEnabled}
              aria-label={t('启用')}
              onChange={(checked) => setAddEnabled(checked)}
            />
            <Typography.Text>{t('启用')}</Typography.Text>
          </Space>
        </Space>
      </Modal>

      <GatewaySecretDialog secret={secret} onClose={() => setSecret(null)} />

      <Drawer
        width={520}
        title={`${t('会话')} · ${sessionsKey}`}
        open={sessionsKey !== ''}
        onClose={() => {
          setSessionsKey('');
          setSessions([]);
        }}
        destroyOnClose
      >
        {sessionsLoading ? (
          <Typography.Text type="secondary">{t('加载中…')}</Typography.Text>
        ) : sessions.length === 0 ? (
          <Typography.Text type="secondary">{t('没有会话')}</Typography.Text>
        ) : (
          <Space direction="vertical" size={6} style={{ width: '100%' }}>
            {sessions.map((row) => {
              const failed = row.status === 0 || row.status >= 400 || !!row.error;
              return (
                <ListCard
                  key={row.requestId}
                  dense
                  title={row.clientModel || row.model || row.requestId}
                  subtitle={`${formatDateTime(row.ts)} · ${row.route || 'unrouted'} · ${formatCount(row.inputTokens)}/${formatCount(row.outputTokens)} tok`}
                  tags={
                    <Tag color={failed ? 'red' : row.status >= 300 ? 'orange' : 'green'}>
                      {row.status}
                    </Tag>
                  }
                />
              );
            })}
          </Space>
        )}
      </Drawer>
    </ListPage>
  );
}
