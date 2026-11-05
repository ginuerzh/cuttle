import { useCallback, useEffect, useState } from 'react';
import {
  Button,
  Checkbox,
  Descriptions,
  Drawer,
  Input,
  Select,
  Space,
  Table,
  Tag,
  Typography,
  message,
} from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { getRequests } from '../api/client';
import { translate as t } from '../i18n';
import type { RequestRow } from '../api/types';
import { useIsMobile } from '../components/useIsMobile';
import { MobileCard } from '../components/MobileCard';

const RANGES = [
  { label: '15 分钟', value: 15 * 60_000 },
  { label: '1 小时', value: 60 * 60_000 },
  { label: '24 小时', value: 24 * 60 * 60_000 },
];

export function RequestsPage() {
  const isMobile = useIsMobile();
  const [rangeMs, setRangeMs] = useState(60 * 60_000);
  const [provider, setProvider] = useState('');
  const [route, setRoute] = useState('');
  const [clientModel, setClientModel] = useState('');
  const [errorsOnly, setErrorsOnly] = useState(false);
  const [rows, setRows] = useState<RequestRow[]>([]);
  const [loading, setLoading] = useState(false);
  const [selected, setSelected] = useState<RequestRow | null>(null);
  const [tick, setTick] = useState(0);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const to = new Date();
      const from = new Date(to.getTime() - rangeMs);
      setRows(
        await getRequests({
          from,
          to,
          provider: provider.trim() || undefined,
          route: route.trim() || undefined,
          clientModel: clientModel.trim() || undefined,
          errorsOnly,
          limit: 200,
        }),
      );
    } catch (err) {
      message.error(err instanceof Error ? err.message : String(err));
    } finally {
      setLoading(false);
    }
  }, [rangeMs, provider, route, clientModel, errorsOnly]);

  useEffect(() => {
    void load();
  }, [load, tick]);

  const columns: ColumnsType<RequestRow> = [
    {
      title: t('时间'),
      dataIndex: 'ts',
      width: 150,
      render: (v: string) => new Date(v).toLocaleString(),
    },
    { title: 'route', dataIndex: 'route', width: 100, responsive: ['md'], render: (v) => v || <Tag>unrouted</Tag> },
    { title: 'provider', dataIndex: 'provider', width: 130, responsive: ['lg'] },
    { title: 'client model', dataIndex: 'clientModel', width: 170, ellipsis: true, responsive: ['lg'] },
    { title: 'provider model', dataIndex: 'model', width: 170, ellipsis: true, responsive: ['xl'] },
    {
      title: 'status',
      dataIndex: 'status',
      width: 80,
      render: (v: number) => <Tag color={v >= 400 ? 'red' : v >= 300 ? 'orange' : 'green'}>{v || '—'}</Tag>,
    },
    { title: 'effort', dataIndex: 'effort', width: 80, responsive: ['lg'], render: (v) => v || '—' },
    { title: t('耗时'), dataIndex: 'durationMs', width: 90, responsive: ['md'], render: (v: number) => `${v}ms` },
    {
      title: 'tokens',
      width: 120,
      responsive: ['lg'],
      render: (_, r) => `${r.inputTokens}/${r.outputTokens}`,
    },
    { title: t('流式'), dataIndex: 'stream', width: 60, responsive: ['xl'], render: (v: boolean) => (v ? t('是') : t('否')) },
  ];

  return (
    <div style={{ padding: isMobile ? 12 : 20, width: '100%', overflowX: 'hidden' }}>
      <Typography.Title level={4} style={{ marginTop: 0 }}>
        Requests
      </Typography.Title>
      <Space wrap style={{ marginBottom: 12 }}>
        <Select size="small" value={rangeMs} onChange={setRangeMs} options={RANGES.map((r) => ({ ...r, label: t(r.label) }))} style={{ width: isMobile ? '100%' : 110 }} />
        <Input size="small" placeholder="provider" value={provider} onChange={(e) => setProvider(e.target.value)} style={{ width: isMobile ? '100%' : 140 }} />
        <Input size="small" placeholder="route" value={route} onChange={(e) => setRoute(e.target.value)} style={{ width: isMobile ? '100%' : 120 }} />
        <Input
          size="small"
          placeholder="client model"
          value={clientModel}
          onChange={(e) => setClientModel(e.target.value)}
          style={{ width: isMobile ? '100%' : 160 }}
        />
        <Checkbox checked={errorsOnly} onChange={(e) => setErrorsOnly(e.target.checked)}>
          {t('仅错误')}
        </Checkbox>
        <Button size="small" loading={loading} onClick={() => setTick((t) => t + 1)}>
          {t('刷新')}
        </Button>
        <Typography.Text type="secondary">{t('{count} 条', { count: rows.length })}</Typography.Text>
      </Space>

      {isMobile ? (
        <div>
          {rows.map((r) => (
            <MobileCard
              key={r.requestId}
              title={r.clientModel || r.model || r.requestId}
              subtitle={`${new Date(r.ts).toLocaleString()} · ${r.route || 'unrouted'} · ${r.provider}${r.node ? `/${r.node}` : ''} · ${r.durationMs}ms · ${r.inputTokens}/${r.outputTokens} tok`}
              tags={
                <Tag color={r.status >= 400 ? 'red' : r.status >= 300 ? 'orange' : 'green'}>
                  {r.status || '—'}
                </Tag>
              }
              onClick={() => setSelected(r)}
            />
          ))}
          {rows.length === 0 && !loading ? (
            <Typography.Text type="secondary">{t('没有请求')}</Typography.Text>
          ) : null}
        </div>
      ) : (
        <Table
          rowKey="requestId"
          size="small"
          loading={loading}
          columns={columns}
          dataSource={rows}
          pagination={{ pageSize: 50 }}
          onRow={(r) => ({ onClick: () => setSelected(r), style: { cursor: 'pointer' } })}
        />
      )}

      <Drawer width={isMobile ? '100%' : 480} title={t('请求详情')} open={!!selected} onClose={() => setSelected(null)} destroyOnClose>
        {selected ? (
          <Descriptions size="small" column={1} bordered>
            <Descriptions.Item label="requestId">{selected.requestId}</Descriptions.Item>
            <Descriptions.Item label="sid">{selected.sid}</Descriptions.Item>
            <Descriptions.Item label={t('时间')}>{new Date(selected.ts).toLocaleString()}</Descriptions.Item>
            <Descriptions.Item label="route">{selected.route || '—'}</Descriptions.Item>
            <Descriptions.Item label="provider">{selected.provider}</Descriptions.Item>
            <Descriptions.Item label="protocol / effort">{`${selected.protocol} / ${selected.effort}`}</Descriptions.Item>
            <Descriptions.Item label="client → logical">{`${selected.clientModel || '—'} → ${selected.logicalModel || '—'}`}</Descriptions.Item>
            <Descriptions.Item label="provider model">{selected.model}</Descriptions.Item>
            <Descriptions.Item label="status / error">{`${selected.status} ${selected.error || ''}`}</Descriptions.Item>
            <Descriptions.Item label={t('耗时')}>{`${selected.durationMs} ms`}</Descriptions.Item>
            <Descriptions.Item label="tokens">
              {t('入 {input} · 出 {output} · 缓存 {cached}', { input: selected.inputTokens, output: selected.outputTokens, cached: selected.cachedTokens })}
            </Descriptions.Item>
            <Descriptions.Item label="bytes">
              {`${selected.bytesIn} in / ${selected.bytesOut} out`}
            </Descriptions.Item>
            <Descriptions.Item label="stream">{selected.stream ? t('是') : t('否')}</Descriptions.Item>
          </Descriptions>
        ) : null}
      </Drawer>
    </div>
  );
}
