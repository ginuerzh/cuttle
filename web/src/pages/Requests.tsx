import { useCallback, useEffect, useState } from 'react';
import {
  Alert as AntAlert,
  Button,
  Checkbox,
  Input,
  InputNumber,
  Modal,
  Select,
  Space,
  Tag,
  Typography,
  message,
} from 'antd';
import { createCaptureRule, deleteCaptureRule, getAlerts, getErrorSummary, getRequests, getSeries, listCaptureRules, listGatewayKeys } from '../api/client';
import { translate as t } from '../i18n';
import type { Alert, CaptureRule, ConfigResponse, ErrorSummary, GatewayKey, RequestRow, SeriesResponse } from '../api/types';
import { RequestDetailDrawer } from '../components/RequestDetail';
import { useIsMobile } from '../components/useIsMobile';
import { ListCard } from '../components/ListCard';
import { SessionChart } from '../components/SessionChart';
import { bucketFor } from '../charts/sessionBars';
import { ListPage } from '../components/ListPage';
import { formatCount } from '../format';
import { configLabels, entityLabel } from '../labels';
import { errorClassLabel, errorClassText, gatewayKeyLabel, retryLabel, statusLabel } from '../requestLabels';
import { formatDateTime } from '../time';

export { errorClassLabel, gatewayKeyLabel, retryLabel, statusLabel };

const RANGES = [
  { label: '15 分钟', value: 15 * 60_000 },
  { label: '1 小时', value: 60 * 60_000 },
  { label: '24 小时', value: 24 * 60 * 60_000 },
];

export function RequestsPage({ config }: { config: ConfigResponse }) {
  const isMobile = useIsMobile();
  const labels = configLabels(config);
  const [rangeMs, setRangeMs] = useState(60 * 60_000);
  const [provider, setProvider] = useState('');
  const [route, setRoute] = useState('');
  const [node, setNode] = useState('');
  const [entrypoint, setEntrypoint] = useState('');
  const [gatewayKey, setGatewayKey] = useState('');
  const [gatewayKeys, setGatewayKeys] = useState<GatewayKey[]>([]);
  const [clientModel, setClientModel] = useState('');
  const [errorsOnly, setErrorsOnly] = useState(false);
  const [capturedOnly, setCapturedOnly] = useState(false);
  const [errorClass, setErrorClass] = useState('');
  const [errorSummary, setErrorSummary] = useState<ErrorSummary | null>(null);
  const [alerts, setAlerts] = useState<Alert[]>([]);
  const [rows, setRows] = useState<RequestRow[]>([]);
  const [loading, setLoading] = useState(false);
  const [selected, setSelected] = useState<RequestRow | null>(null);
  const [series, setSeries] = useState<SeriesResponse | null>(null);
  const [seriesFailed, setSeriesFailed] = useState(false);
  // A bar click narrows the whole page to that bucket; the chart hands back the
  // bucket start and the list and series both use this window.
  const [zoom, setZoom] = useState<{ from: number; to: number } | null>(null);
  const [chartWindow, setChartWindow] = useState<{ from: number; to: number; bucket: number } | null>(null);
  const [tick, setTick] = useState(0);
  const [captureOpen, setCaptureOpen] = useState(false);
  const [rules, setRules] = useState<CaptureRule[]>([]);
  const [captureBusy, setCaptureBusy] = useState(false);
  const [captureRoute, setCaptureRoute] = useState('');
  const [captureModel, setCaptureModel] = useState('');
  const [captureLimit, setCaptureLimit] = useState(10);
  const [captureTTL, setCaptureTTL] = useState(300);
  const [captureReason, setCaptureReason] = useState('investigate');

  const load = useCallback(async () => {
    setLoading(true);
    setSeriesFailed(false);
    try {
      const to = zoom ? new Date(zoom.to) : new Date();
      const from = zoom ? new Date(zoom.from) : new Date(to.getTime() - rangeMs);
      const bucket = bucketFor(to.getTime() - from.getTime());
      setChartWindow({ from: from.getTime(), to: to.getTime(), bucket });
      const filters = {
        provider: provider || undefined,
        route: route || undefined,
        node: node || undefined,
        entrypoint: entrypoint || undefined,
        gatewayKey: gatewayKey || undefined,
        clientModel: clientModel.trim() || undefined,
        errorsOnly,
        capturedOnly,
        errorClass: errorClass || undefined,
      };
      const [list, summary, activeAlerts, trend] = await Promise.all([
        getRequests({ from, to, ...filters, limit: 200 }),
        getErrorSummary({ from, to }),
        getAlerts(),
        // A failed trend must not blank the list.
        getSeries({ from, to, bucket, ...filters }).catch(() => null),
      ]);
      setRows(list);
      setErrorSummary(summary);
      setAlerts(activeAlerts);
      setSeries(trend);
      setSeriesFailed(trend === null);
    } catch (err) {
      message.error(err instanceof Error ? err.message : String(err));
    } finally {
      setLoading(false);
    }
  }, [rangeMs, provider, route, node, entrypoint, gatewayKey, clientModel, errorsOnly, capturedOnly, errorClass, zoom]);

  const loadCaptures = useCallback(async () => {
    try {
      setRules(await listCaptureRules());
    } catch (err) {
      message.error(err instanceof Error ? err.message : String(err));
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load, tick]);

  useEffect(() => {
    // The key filter selects by id; a failed listing leaves the filter empty.
    let active = true;
    listGatewayKeys()
      .then((keys) => {
        if (active) setGatewayKeys(keys);
      })
      .catch(() => undefined);
    return () => {
      active = false;
    };
  }, []);

  const startCapture = async () => {
    setCaptureBusy(true);
    try {
      await createCaptureRule({
        route: captureRoute || undefined,
        clientModel: captureModel.trim() || undefined,
        reason: captureReason.trim() || 'investigate',
        limit: captureLimit,
        ttlSeconds: captureTTL,
      });
      message.success(t('捕获已开启'));
      await loadCaptures();
    } catch (err) {
      message.error(err instanceof Error ? err.message : String(err));
    } finally {
      setCaptureBusy(false);
    }
  };

  const removeRule = async (id: string) => {
    try {
      await deleteCaptureRule(id);
      await loadCaptures();
    } catch (err) {
      message.error(err instanceof Error ? err.message : String(err));
    }
  };

  return (
    <ListPage>
      <Typography.Title level={4} style={{ marginTop: 0 }}>
        {t('会话')}
      </Typography.Title>
      {alerts.map((a) => (
        <AntAlert
          key={a.id}
          type={a.severity === 'critical' ? 'error' : 'warning'}
          showIcon
          message={a.title}
          description={a.detail}
          style={{ marginBottom: 8 }}
        />
      ))}
      {errorSummary && errorSummary.errors > 0 ? (
        <div style={{ marginBottom: 12 }}>
          <Space wrap>
            <Typography.Text strong>{t('错误概览')}</Typography.Text>
            <Tag color="red">
              {t('{errors} 错误 / {total} 会话', { errors: errorSummary.errors, total: errorSummary.total })}
            </Tag>
            <Tag color="orange">{`${(errorSummary.errorRate * 100).toFixed(1)}%`}</Tag>
            {errorSummary.byClass.slice(0, 6).map((g) => (
              <Tag
                key={g.key}
                color={errorClass === g.key ? 'blue' : undefined}
                style={{ cursor: 'pointer' }}
                onClick={() => setErrorClass(errorClass === g.key ? '' : g.key)}
              >
                {`${errorClassText(g.key)} ${g.errors}`}
              </Tag>
            ))}
            {errorClass ? (
              <Button size="small" type="link" onClick={() => setErrorClass('')}>
                {t('清除筛选')}
              </Button>
            ) : null}
          </Space>
          {errorSummary.topMessages.length > 0 ? (
            <div style={{ marginTop: 6 }}>
              {errorSummary.topMessages.slice(0, 3).map((m, i) => (
                <Typography.Text
                  key={`${m.class}-${i}`}
                  type="secondary"
                  style={{ display: 'block', fontSize: 12 }}
                >
                  {`${m.count}× ${errorClassText(m.class)}: ${m.message}`}
                </Typography.Text>
              ))}
            </div>
          ) : null}
        </div>
      ) : null}
      <Space wrap style={{ marginBottom: 12 }}>
        <Select
          size="small"
          value={rangeMs}
          // A preset change is a new window, so it also leaves any bar zoom.
          onChange={(value: number) => {
            setZoom(null);
            setRangeMs(value);
          }}
          options={RANGES.map((r) => ({ ...r, label: t(r.label) }))}
          style={{ width: isMobile ? '100%' : 110 }}
        />
        <Select
          size="small"
          allowClear
          showSearch
          optionFilterProp="label"
          placeholder={t('提供商')}
          aria-label={t('提供商')}
          value={provider || undefined}
          onChange={(value?: string) => setProvider(value ?? '')}
          options={config.providers.map((p) => ({ value: p.id, label: p.name }))}
          style={{ width: isMobile ? '100%' : 160 }}
        />
        <Select
          size="small"
          allowClear
          showSearch
          optionFilterProp="label"
          placeholder={t('路由')}
          aria-label={t('路由')}
          value={route || undefined}
          onChange={(value?: string) => setRoute(value ?? '')}
          options={config.routes.map((r) => ({ value: r.id, label: r.name }))}
          style={{ width: isMobile ? '100%' : 140 }}
        />
        <Select
          size="small"
          allowClear
          showSearch
          optionFilterProp="label"
          placeholder={t('节点')}
          aria-label={t('节点')}
          value={node || undefined}
          onChange={(value?: string) => setNode(value ?? '')}
          options={config.nodes.map((n) => ({ value: n.id, label: n.name }))}
          style={{ width: isMobile ? '100%' : 140 }}
        />
        <Select
          size="small"
          allowClear
          showSearch
          optionFilterProp="label"
          placeholder={t('入口')}
          aria-label={t('入口')}
          value={entrypoint || undefined}
          onChange={(value?: string) => setEntrypoint(value ?? '')}
          options={config.entrypoints.map((e) => ({ value: e.id, label: e.name }))}
          style={{ width: isMobile ? '100%' : 140 }}
        />
        <Select
          size="small"
          allowClear
          showSearch
          optionFilterProp="label"
          placeholder={t('网关密钥')}
          aria-label={t('网关密钥')}
          value={gatewayKey || undefined}
          onChange={(value?: string) => setGatewayKey(value ?? '')}
          options={gatewayKeys.map((k) => ({ value: k.id, label: k.name }))}
          style={{ width: isMobile ? '100%' : 160 }}
        />
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
        <Checkbox checked={capturedOnly} onChange={(e) => setCapturedOnly(e.target.checked)}>
          {t('仅捕获')}
        </Checkbox>
        <Button size="small" loading={loading} onClick={() => setTick((t) => t + 1)}>
          {t('刷新')}
        </Button>
        <Button
          size="small"
          onClick={() => {
            setCaptureOpen(true);
            void loadCaptures();
          }}
        >
          {t('捕获')}
        </Button>
        <Typography.Text type="secondary">{t('{count} 条', { count: rows.length })}</Typography.Text>
      </Space>

      {chartWindow ? (
        <div style={{ marginBottom: 12 }}>
          <Space wrap style={{ marginBottom: 4 }}>
            <Typography.Text strong>{t('会话趋势')}</Typography.Text>
            {zoom ? (
              <Button size="small" type="link" onClick={() => setZoom(null)}>
                {t('清除缩放')}
              </Button>
            ) : null}
          </Space>
          <SessionChart
            points={series?.points ?? []}
            from={chartWindow.from}
            to={chartWindow.to}
            bucket={chartWindow.bucket}
            selected={zoom?.from}
            failed={seriesFailed}
            onSelect={(bucket) =>
              setZoom(bucket === undefined ? null : { from: bucket, to: bucket + chartWindow.bucket })
            }
          />
        </div>
      ) : null}
      {rows.map((r) => {
        const failed = r.status >= 400 || !!r.error;
        const classLabel = errorClassLabel(r);
        const keyLabel = gatewayKeyLabel(r);
        const retry = retryLabel(r);
        const routeLabel = r.route ? r.routeName || r.route : 'unrouted';
        const providerLabel = r.providerName || r.provider;
        const nodeLabel = r.node ? `/${r.nodeName || r.node}` : '';
        return (
          <ListCard
            key={r.requestId}
            title={r.clientModel || r.model || r.requestId}
            subtitle={`${formatDateTime(r.ts)} · ${routeLabel} · ${providerLabel}${nodeLabel} · ${r.durationMs}ms · ${formatCount(r.inputTokens)}/${formatCount(r.outputTokens)} tok${r.effort && r.effort !== 'none' ? ` · effort ${r.effort}` : ''}${keyLabel ? ` · ${keyLabel}` : ''}${classLabel ? ` · ${classLabel}` : ''}${r.error ? ` · ${r.error}` : ''}`}
            tags={
              <Space size={4}>
                {r.truncated ? <Tag color="volcano">{t('已截断')}</Tag> : null}
                {r.hasCapture ? <Tag color="blue">{t('已捕获')}</Tag> : null}
                {retry ? <Tag color="geekblue">{retry}</Tag> : null}
                <Tag color={failed ? 'red' : r.status >= 300 ? 'orange' : 'green'}>{statusLabel(r)}</Tag>
              </Space>
            }
            onClick={() => setSelected(r)}
          />
        );
      })}
      {rows.length === 0 && !loading ? (
        <Typography.Text type="secondary">{t('没有会话')}</Typography.Text>
      ) : null}

      <RequestDetailDrawer request={selected} onClose={() => setSelected(null)} />

      <Modal
        title={t('捕获规则')}
        open={captureOpen}
        onCancel={() => setCaptureOpen(false)}
        footer={null}
        width={640}
      >
        <Space direction="vertical" style={{ width: '100%' }}>
          <Select
            allowClear
            showSearch
            optionFilterProp="label"
            placeholder={t('路由（留空表示全部）')}
            value={captureRoute || undefined}
            onChange={(value?: string) => setCaptureRoute(value ?? '')}
            options={config.routes.map((r) => ({ value: r.id, label: r.name }))}
            style={{ width: '100%' }}
          />
          <Input
            placeholder={t('客户端模型 glob（留空表示全部）')}
            value={captureModel}
            onChange={(e) => setCaptureModel(e.target.value)}
          />
          <Space wrap>
            <InputNumber
              min={1}
              max={1000}
              value={captureLimit}
              onChange={(v) => setCaptureLimit(Number(v ?? 1))}
              addonBefore={t('数量上限')}
            />
            <InputNumber
              min={1}
              max={3600}
              value={captureTTL}
              onChange={(v) => setCaptureTTL(Number(v ?? 1))}
              addonBefore={t('有效期（秒）')}
            />
          </Space>
          <Input
            placeholder={t('原因')}
            value={captureReason}
            onChange={(e) => setCaptureReason(e.target.value)}
          />
          <Button type="primary" loading={captureBusy} onClick={() => void startCapture()}>
            {t('开始捕获')}
          </Button>

          <Typography.Title level={5}>{t('活动规则')}</Typography.Title>
          {rules.length === 0 ? (
            <Typography.Text type="secondary">{t('没有活动规则')}</Typography.Text>
          ) : (
            rules.map((r) => (
              <Space key={r.id} style={{ display: 'flex', justifyContent: 'space-between' }}>
                <span>{`${entityLabel(r.route, labels.routes) || '*'} · ${r.clientModel || '*'} · ${r.reason} · ${r.used}/${r.limit}`}</span>
                <Button size="small" danger onClick={() => void removeRule(r.id)}>
                  {t('删除')}
                </Button>
              </Space>
            ))
          )}
        </Space>
      </Modal>
    </ListPage>
  );
}
