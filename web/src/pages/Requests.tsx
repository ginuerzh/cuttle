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
import type { RequestRow } from '../api/types';

const RANGES = [
  { label: '15 分钟', value: 15 * 60_000 },
  { label: '1 小时', value: 60 * 60_000 },
  { label: '24 小时', value: 24 * 60 * 60_000 },
];

export function RequestsPage() {
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
      title: '时间',
      dataIndex: 'ts',
      width: 150,
      render: (v: string) => new Date(v).toLocaleString(),
    },
    { title: 'route', dataIndex: 'route', width: 100, render: (v) => v || <Tag>unrouted</Tag> },
    { title: 'provider', dataIndex: 'provider', width: 130 },
    { title: 'client model', dataIndex: 'clientModel', width: 170, ellipsis: true },
    { title: 'provider model', dataIndex: 'model', width: 170, ellipsis: true },
    {
      title: 'status',
      dataIndex: 'status',
      width: 80,
      render: (v: number) => <Tag color={v >= 400 ? 'red' : v >= 300 ? 'orange' : 'green'}>{v || '—'}</Tag>,
    },
    { title: 'effort', dataIndex: 'effort', width: 80, render: (v) => v || '—' },
    { title: '耗时', dataIndex: 'durationMs', width: 90, render: (v: number) => `${v}ms` },
    {
      title: 'tokens',
      width: 120,
      render: (_, r) => `${r.inputTokens}/${r.outputTokens}`,
    },
    { title: '流式', dataIndex: 'stream', width: 60, render: (v: boolean) => (v ? '是' : '否') },
  ];

  return (
    <div style={{ padding: 20, width: '100%', overflow: 'auto' }}>
      <Typography.Title level={4} style={{ marginTop: 0 }}>
        Requests
      </Typography.Title>
      <Space wrap style={{ marginBottom: 12 }}>
        <Select size="small" value={rangeMs} onChange={setRangeMs} options={RANGES} style={{ width: 110 }} />
        <Input size="small" placeholder="provider" value={provider} onChange={(e) => setProvider(e.target.value)} style={{ width: 140 }} />
        <Input size="small" placeholder="route" value={route} onChange={(e) => setRoute(e.target.value)} style={{ width: 120 }} />
        <Input
          size="small"
          placeholder="client model"
          value={clientModel}
          onChange={(e) => setClientModel(e.target.value)}
          style={{ width: 160 }}
        />
        <Checkbox checked={errorsOnly} onChange={(e) => setErrorsOnly(e.target.checked)}>
          仅错误
        </Checkbox>
        <Button size="small" loading={loading} onClick={() => setTick((t) => t + 1)}>
          刷新
        </Button>
        <Typography.Text type="secondary">{rows.length} 条</Typography.Text>
      </Space>

      <Table
        rowKey="requestId"
        size="small"
        loading={loading}
        columns={columns}
        dataSource={rows}
        pagination={{ pageSize: 50 }}
        onRow={(r) => ({ onClick: () => setSelected(r), style: { cursor: 'pointer' } })}
      />

      <Drawer width={480} title="请求详情" open={!!selected} onClose={() => setSelected(null)} destroyOnClose>
        {selected ? (
          <Descriptions size="small" column={1} bordered>
            <Descriptions.Item label="requestId">{selected.requestId}</Descriptions.Item>
            <Descriptions.Item label="sid">{selected.sid}</Descriptions.Item>
            <Descriptions.Item label="时间">{new Date(selected.ts).toLocaleString()}</Descriptions.Item>
            <Descriptions.Item label="route / tier">{`${selected.route || '—'} / ${selected.tier || 'base'}`}</Descriptions.Item>
            <Descriptions.Item label="provider">{selected.provider}</Descriptions.Item>
            <Descriptions.Item label="protocol / effort">{`${selected.protocol} / ${selected.effort}`}</Descriptions.Item>
            <Descriptions.Item label="client → logical">{`${selected.clientModel || '—'} → ${selected.logicalModel || '—'}`}</Descriptions.Item>
            <Descriptions.Item label="provider model">{selected.model}</Descriptions.Item>
            <Descriptions.Item label="status / error">{`${selected.status} ${selected.error || ''}`}</Descriptions.Item>
            <Descriptions.Item label="耗时">{`${selected.durationMs} ms`}</Descriptions.Item>
            <Descriptions.Item label="tokens">
              {`入 ${selected.inputTokens} · 出 ${selected.outputTokens} · 缓存 ${selected.cachedTokens}`}
            </Descriptions.Item>
            <Descriptions.Item label="bytes">
              {`${selected.bytesIn} in / ${selected.bytesOut} out`}
            </Descriptions.Item>
            <Descriptions.Item label="stream">{selected.stream ? '是' : '否'}</Descriptions.Item>
          </Descriptions>
        ) : null}
      </Drawer>
    </div>
  );
}
