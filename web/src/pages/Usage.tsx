import { useCallback, useEffect, useState } from 'react';
import {
  Alert,
  Button,
  Card,
  Col,
  Descriptions,
  Empty,
  Progress,
  Row,
  Segmented,
  Skeleton,
  Space,
  Statistic,
  Tag,
  Typography,
} from 'antd';
import { ReloadOutlined } from '@ant-design/icons';
import { getProviderUsage } from '../api/client';
import type { AccountUsage, BalancePart, ProviderUsage, ProviderUsageResponse, QuotaWindow } from '../api/types';
import { useIsMobile } from '../components/useIsMobile';
import { ListPage } from '../components/ListPage';
import { translate as t } from '../i18n';

const RANGES = [
  { label: '24 小时', value: 24 * 3_600_000 },
  { label: '7 天', value: 7 * 24 * 3_600_000 },
  { label: '30 天', value: 30 * 24 * 3_600_000 },
];

function formatCount(n?: number): string {
  if (n === undefined || n === null) return '—';
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}k`;
  return String(n);
}

function currencySymbol(currency?: string): string {
  switch ((currency ?? '').toUpperCase()) {
    case 'USD':
      return '$';
    case 'CNY':
    case 'RMB':
      return '¥';
    case 'EUR':
      return '€';
    case 'GBP':
      return '£';
    default:
      return currency ?? '';
  }
}

function formatMoney(b: BalancePart): string {
  const symbol = currencySymbol(b.currency);
  let out = `${symbol}${b.total.toFixed(2)}`;
  const parts: string[] = [];
  if (b.granted) parts.push(`${t('赠金')} ${symbol}${b.granted.toFixed(2)}`);
  if (b.toppedUp) parts.push(`${t('充值')} ${symbol}${b.toppedUp.toFixed(2)}`);
  if (parts.length > 0) out += ` · ${parts.join(' · ')}`;
  return out;
}

function windowLabel(name: string): string {
  if (name === '5h') return t('5 小时');
  if (name === 'weekly') return t('每周');
  if (name === 'monthly') return t('每月');
  return name;
}

function WindowRow({ window }: { window: QuotaWindow }) {
  const percent = Math.round(window.usedPercent);
  const color = percent >= 90 ? '#dc2626' : percent >= 70 ? '#d97706' : '#16a34a';
  return (
    <div style={{ marginTop: 10 }}>
      <Space style={{ width: '100%', justifyContent: 'space-between' }}>
        <span>{windowLabel(window.name)}</span>
        <Typography.Text type="secondary">
          {`${percent}%`}
          {window.resetsAt ? ` · ${t('重置')} ${new Date(window.resetsAt).toLocaleString()}` : ''}
        </Typography.Text>
      </Space>
      <Progress percent={percent} showInfo={false} strokeColor={color} />
    </div>
  );
}

function AccountRow({ account, first }: { account: AccountUsage; first: boolean }) {
  return (
    <div
      style={{
        borderTop: first ? undefined : '1px solid var(--gw-border)',
        paddingTop: first ? 0 : 12,
        marginTop: 12,
      }}
    >
      <Space wrap>
        <Typography.Text code>{account.keyPreview || '—'}</Typography.Text>
        {account.stale ? <Tag color="orange">{t('旧数据')}</Tag> : null}
        {account.checkedAt ? (
          <Typography.Text type="secondary">{new Date(account.checkedAt).toLocaleString()}</Typography.Text>
        ) : null}
      </Space>
      {account.balance?.map((balance, i) => (
        <div key={i} style={{ marginTop: 8, fontSize: 16, fontWeight: 600 }}>
          {formatMoney(balance)}
        </div>
      ))}
      {account.windows?.map((window) => (
        <WindowRow key={window.name} window={window} />
      ))}
      {!account.balance?.length && !account.windows?.length && !account.error ? (
        <Typography.Text type="secondary">—</Typography.Text>
      ) : null}
      {account.error ? (
        <Alert
          style={{ marginTop: 8 }}
          type={account.stale ? 'warning' : 'error'}
          showIcon
          message={account.error}
        />
      ) : null}
    </div>
  );
}

function ProviderCard({ provider, isMobile }: { provider: ProviderUsage; isMobile: boolean }) {
  const local = provider.local;
  return (
    <Card
      size="small"
      style={{ marginBottom: 12 }}
      title={
        <Space wrap>
          <span>{provider.name}</span>
          <Tag>{provider.type}</Tag>
          {!provider.enabled ? <Tag>{t('已禁用')}</Tag> : null}
          {provider.cached ? <Tag color="blue">{t('缓存')}</Tag> : null}
        </Space>
      }
      extra={
        provider.checkedAt ? (
          <Typography.Text type="secondary">{new Date(provider.checkedAt).toLocaleTimeString()}</Typography.Text>
        ) : null
      }
    >
      <Descriptions size="small" column={isMobile ? 1 : 5} bordered>
        <Descriptions.Item label={t('请求')}>{formatCount(local?.requests)}</Descriptions.Item>
        <Descriptions.Item label={t('错误')}>{formatCount(local?.errors)}</Descriptions.Item>
        <Descriptions.Item label={t('输入 tokens')}>{formatCount(local?.inputTokens)}</Descriptions.Item>
        <Descriptions.Item label={t('输出 tokens')}>{formatCount(local?.outputTokens)}</Descriptions.Item>
        <Descriptions.Item label={t('缓存 tokens')}>{formatCount(local?.cachedTokens)}</Descriptions.Item>
      </Descriptions>

      {provider.note ? (
        <Alert
          style={{ marginTop: 12 }}
          type={provider.enabled ? 'info' : 'warning'}
          showIcon
          message={provider.note}
        />
      ) : null}
      {provider.supported && provider.accounts.length === 0 && !provider.note ? (
        <Typography.Text type="secondary">{t('没有可显示的账户')}</Typography.Text>
      ) : null}
      {provider.accounts.map((account, i) => (
        <AccountRow key={`${account.keyPreview ?? 'key'}-${i}`} account={account} first={i === 0} />
      ))}
    </Card>
  );
}

export function UsagePage() {
  const isMobile = useIsMobile();
  const [rangeMs, setRangeMs] = useState(RANGES[0].value);
  const [data, setData] = useState<ProviderUsageResponse | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');

  const load = useCallback(
    async (refresh = false) => {
      setLoading(true);
      setError('');
      try {
        const to = new Date();
        const from = new Date(to.getTime() - rangeMs);
        setData(await getProviderUsage({ from, to, refresh }));
      } catch (err) {
        setError(err instanceof Error ? err.message : String(err));
      } finally {
        setLoading(false);
      }
    },
    [rangeMs],
  );

  useEffect(() => {
    void load();
  }, [load]);

  const totals = data?.totals;
  const stats = [
    { label: t('请求'), value: formatCount(totals?.requests) },
    { label: t('错误'), value: formatCount(totals?.errors) },
    { label: t('输入 tokens'), value: formatCount(totals?.inputTokens) },
    { label: t('输出 tokens'), value: formatCount(totals?.outputTokens) },
    { label: t('缓存 tokens'), value: formatCount(totals?.cachedTokens) },
  ];

  return (
    <ListPage>
      <Space wrap style={{ width: '100%', justifyContent: 'space-between', marginBottom: 12 }}>
        <Space wrap>
          <Typography.Title level={4} style={{ margin: 0 }}>
            {t('用量')}
          </Typography.Title>
          <Segmented
            value={rangeMs}
            onChange={(value) => setRangeMs(Number(value))}
            options={RANGES.map((r) => ({ ...r, label: t(r.label) }))}
          />
          <Button icon={<ReloadOutlined />} loading={loading} onClick={() => void load(true)}>
            {t('刷新')}
          </Button>
        </Space>
        {data ? (
          <Typography.Text type="secondary">
            {t('更新于 {time}', { time: new Date(data.checkedAt).toLocaleTimeString() })}
          </Typography.Text>
        ) : null}
      </Space>

      {error ? (
        <Alert type="error" showIcon message={t('加载失败')} description={error} style={{ marginBottom: 12 }} />
      ) : null}
      {data?.localError ? (
        <Alert type="warning" showIcon message={data.localError} style={{ marginBottom: 12 }} />
      ) : null}

      <Row gutter={[12, 12]} style={{ marginBottom: 16 }}>
        {stats.map((stat) => (
          <Col key={stat.label} xs={12} sm={8} md={4}>
            <Card size="small">
              <Statistic title={stat.label} value={stat.value} />
            </Card>
          </Col>
        ))}
      </Row>

      {loading && !data ? <Skeleton active /> : null}
      {data?.providers.length === 0 ? <Empty description={t('没有可显示的账户')} /> : null}
      {data?.providers.map((provider) => (
        <ProviderCard key={provider.name} provider={provider} isMobile={isMobile} />
      ))}
    </ListPage>
  );
}
