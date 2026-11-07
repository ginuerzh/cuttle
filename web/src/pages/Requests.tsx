import { useCallback, useEffect, useState } from 'react';
import {
  Alert as AntAlert,
  Button,
  Checkbox,
  Descriptions,
  Drawer,
  Input,
  InputNumber,
  Modal,
  Select,
  Space,
  Tag,
  Typography,
  message,
} from 'antd';
import { createCaptureRule, deleteCaptureRule, getAlerts, getErrorSummary, getRequest, getRequests, listCaptureRules } from '../api/client';
import { translate as t } from '../i18n';
import type { Alert, CaptureRule, ErrorSummary, RequestRow } from '../api/types';
import { useIsMobile } from '../components/useIsMobile';
import { ListCard } from '../components/ListCard';
import { ListPage } from '../components/ListPage';
import { formatDateTime } from '../time';

const RANGES = [
  { label: '15 分钟', value: 15 * 60_000 },
  { label: '1 小时', value: 60 * 60_000 },
  { label: '24 小时', value: 24 * 60 * 60_000 },
];

function Preview({ label, text }: { label: string; text?: string }) {
  if (!text) return null;
  return (
    <div style={{ marginBottom: 12 }}>
      <Typography.Text strong>{label}</Typography.Text>
      <pre
        style={{
          margin: '4px 0 0',
          padding: 8,
          maxHeight: 220,
          overflow: 'auto',
          whiteSpace: 'pre-wrap',
          wordBreak: 'break-all',
          background: 'var(--gw-surface)',
          border: '1px solid var(--gw-border)',
          borderRadius: 6,
          fontSize: 12,
        }}
      >
        {text}
      </pre>
    </div>
  );
}

export function statusLabel(r: RequestRow): string {
  if (r.status === 0) {
    return r.errorClass === 'gateway_timeout' ? t('502 网关超时') : t('502 上游失败');
  }
  return String(r.status);
}

const ERROR_CLASS_LABELS: Record<string, string> = {
  upstream_auth: '上游认证失败',
  upstream_forbidden: '上游拒绝',
  upstream_not_found: '上游 404',
  upstream_quota: '上游限流/额度',
  upstream_bad_request: '上游请求错误',
  upstream_unavailable: '上游不可用',
  upstream_truncated: '上游流截断',
  upstream_error: '上游错误',
  gateway_timeout: '网关超时',
  network: '网络错误',
  rewrite_error: '改写错误',
  client_error: '客户端错误',
  client_disconnect: '客户端断开',
};

export function errorClassLabel(r: RequestRow): string {
  if (!r.errorClass) return '';
  return t(ERROR_CLASS_LABELS[r.errorClass] || r.errorClass);
}

export function gatewayKeyLabel(row: RequestRow): string {
  return row.gatewayKey ? `key:${row.gatewayKey}` : '';
}

export function RequestsPage() {
  const isMobile = useIsMobile();
  const [rangeMs, setRangeMs] = useState(60 * 60_000);
  const [provider, setProvider] = useState('');
  const [route, setRoute] = useState('');
  const [gatewayKey, setGatewayKey] = useState('');
  const [clientModel, setClientModel] = useState('');
  const [errorsOnly, setErrorsOnly] = useState(false);
  const [capturedOnly, setCapturedOnly] = useState(false);
  const [errorClass, setErrorClass] = useState('');
  const [errorSummary, setErrorSummary] = useState<ErrorSummary | null>(null);
  const [alerts, setAlerts] = useState<Alert[]>([]);
  const [rows, setRows] = useState<RequestRow[]>([]);
  const [loading, setLoading] = useState(false);
  const [selected, setSelected] = useState<RequestRow | null>(null);
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
    try {
      const to = new Date();
      const from = new Date(to.getTime() - rangeMs);
      const [list, summary, activeAlerts] = await Promise.all([
        getRequests({
          from,
          to,
          provider: provider.trim() || undefined,
          route: route.trim() || undefined,
          gatewayKey: gatewayKey.trim() || undefined,
          clientModel: clientModel.trim() || undefined,
          errorsOnly,
          capturedOnly,
          errorClass: errorClass || undefined,
          limit: 200,
        }),
        getErrorSummary({ from, to }),
        getAlerts(),
      ]);
      setRows(list);
      setErrorSummary(summary);
      setAlerts(activeAlerts);
    } catch (err) {
      message.error(err instanceof Error ? err.message : String(err));
    } finally {
      setLoading(false);
    }
  }, [rangeMs, provider, route, gatewayKey, clientModel, errorsOnly, capturedOnly, errorClass]);

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

  const openRequest = async (row: RequestRow) => {
    setSelected(row);
    try {
      const full = await getRequest(row.requestId);
      setSelected((current) => (current?.requestId === row.requestId ? full : current));
    } catch (err) {
      message.error(err instanceof Error ? err.message : String(err));
    }
  };

  const copyID = async (id: string) => {
    try {
      await navigator.clipboard.writeText(id);
      message.success(t('已复制'));
    } catch {
      message.error(t('复制失败'));
    }
  };

  const startCapture = async () => {
    setCaptureBusy(true);
    try {
      await createCaptureRule({
        route: captureRoute.trim() || undefined,
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

  const cap = selected?.capture;
  const hasPreview = !!(
    cap?.requestOriginal ||
    cap?.requestRewritten ||
    cap?.responseOriginal ||
    cap?.responseRewritten ||
    cap?.sseFirstOriginal ||
    cap?.sseFirstRewritten ||
    cap?.sseLastOriginal ||
    cap?.sseLastRewritten
  );

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
                {`${t(ERROR_CLASS_LABELS[g.key] || g.key)} ${g.errors}`}
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
                  {`${m.count}× ${t(ERROR_CLASS_LABELS[m.class] || m.class)}: ${m.message}`}
                </Typography.Text>
              ))}
            </div>
          ) : null}
        </div>
      ) : null}
      <Space wrap style={{ marginBottom: 12 }}>
        <Select size="small" value={rangeMs} onChange={setRangeMs} options={RANGES.map((r) => ({ ...r, label: t(r.label) }))} style={{ width: isMobile ? '100%' : 110 }} />
        <Input size="small" placeholder="provider" value={provider} onChange={(e) => setProvider(e.target.value)} style={{ width: isMobile ? '100%' : 140 }} />
        <Input size="small" placeholder="route" value={route} onChange={(e) => setRoute(e.target.value)} style={{ width: isMobile ? '100%' : 120 }} />
        <Input
          size="small"
          placeholder={t('请求密钥')}
          value={gatewayKey}
          onChange={(e) => setGatewayKey(e.target.value)}
          style={{ width: isMobile ? '100%' : 140 }}
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

      {rows.map((r) => {
        const failed = r.status >= 400 || !!r.error;
        const classLabel = errorClassLabel(r);
        const keyLabel = gatewayKeyLabel(r);
        return (
          <ListCard
            key={r.requestId}
            title={r.clientModel || r.model || r.requestId}
            subtitle={`${formatDateTime(r.ts)} · ${r.route || 'unrouted'} · ${r.provider}${r.node ? `/${r.node}` : ''} · ${r.durationMs}ms · ${r.inputTokens}/${r.outputTokens} tok${keyLabel ? ` · ${keyLabel}` : ''}${classLabel ? ` · ${classLabel}` : ''}${r.error ? ` · ${r.error}` : ''}`}
            tags={
              <Space size={4}>
                {r.truncated ? <Tag color="volcano">{t('已截断')}</Tag> : null}
                {r.hasCapture ? <Tag color="blue">{t('已捕获')}</Tag> : null}
                <Tag color={failed ? 'red' : r.status >= 300 ? 'orange' : 'green'}>{statusLabel(r)}</Tag>
              </Space>
            }
            onClick={() => void openRequest(r)}
          />
        );
      })}
      {rows.length === 0 && !loading ? (
        <Typography.Text type="secondary">{t('没有会话')}</Typography.Text>
      ) : null}

      <Drawer width={isMobile ? '100%' : 560} title={t('会话详情')} open={!!selected} onClose={() => setSelected(null)} destroyOnClose>
        {selected ? (
          <>
            <Descriptions size="small" column={1} bordered>
              <Descriptions.Item label="requestId">
                <Space>
                  <span>{selected.requestId}</span>
                  <Button size="small" onClick={() => void copyID(selected.requestId)}>
                    {t('复制')}
                  </Button>
                </Space>
              </Descriptions.Item>
              <Descriptions.Item label="sid">{selected.sid}</Descriptions.Item>
              <Descriptions.Item label={t('时间')}>{formatDateTime(selected.ts)}</Descriptions.Item>
              <Descriptions.Item label="route">{selected.route || '—'}</Descriptions.Item>
              <Descriptions.Item label={t('请求密钥')}>{selected.gatewayKey || '—'}</Descriptions.Item>
              <Descriptions.Item label="provider">{selected.provider}</Descriptions.Item>
              <Descriptions.Item label="client model">{selected.clientModel || '—'}</Descriptions.Item>
              <Descriptions.Item label="provider model">{selected.model}</Descriptions.Item>
              <Descriptions.Item label={t('入站协议')}>{selected.inboundProtocol || '—'}</Descriptions.Item>
              <Descriptions.Item label={t('上游协议')}>{selected.protocol || '—'}</Descriptions.Item>
              <Descriptions.Item label={t('请求路径')}>
                {`${selected.method || '—'} ${selected.uri || '—'}`}
              </Descriptions.Item>
              <Descriptions.Item label="client ip">{selected.clientIp || '—'}</Descriptions.Item>
              <Descriptions.Item label="user agent">{selected.userAgent || '—'}</Descriptions.Item>
              <Descriptions.Item label="upstream">{selected.upstream || '—'}</Descriptions.Item>
              <Descriptions.Item label={t('错误阶段')}>{selected.errorStage || '—'}</Descriptions.Item>
              <Descriptions.Item label={t('错误方向')}>{selected.errorDirection || '—'}</Descriptions.Item>
              <Descriptions.Item label={t('错误分类')}>{errorClassLabel(selected) || '—'}</Descriptions.Item>
              <Descriptions.Item label="SSE phase / index">
                {selected.ssePhase ? `${selected.ssePhase} / ${selected.eventIndex ?? 0}` : '— / —'}
              </Descriptions.Item>
              <Descriptions.Item label="status / error">
                <Typography.Text type={selected.error || selected.status >= 400 ? 'danger' : undefined}>
                  {`${statusLabel(selected)}${selected.clientStatus && selected.clientStatus !== selected.status ? ` (client ${selected.clientStatus})` : ''} ${selected.error || ''}`}
                </Typography.Text>
              </Descriptions.Item>
              <Descriptions.Item label={t('耗时')}>{`${selected.durationMs} ms`}</Descriptions.Item>
              <Descriptions.Item label="tokens">
                {t('入 {input} · 出 {output} · 缓存 {cached}', { input: selected.inputTokens, output: selected.outputTokens, cached: selected.cachedTokens })}
              </Descriptions.Item>
              <Descriptions.Item label="bytes">
                {`${selected.bytesIn} in / ${selected.bytesOut} out`}
              </Descriptions.Item>
              <Descriptions.Item label="stream">{selected.stream ? t('是') : t('否')}</Descriptions.Item>
              <Descriptions.Item label={t('流式终止')}>
                {selected.stream
                  ? `${selected.finishReason || '—'} / done=${selected.sawDone ? t('是') : t('否')}${selected.truncated ? ` / ${t('已截断')}` : ''}`
                  : '—'}
              </Descriptions.Item>
            </Descriptions>

            <Typography.Title level={5} style={{ marginTop: 20 }}>
              {t('捕获详情')}
            </Typography.Title>
            {cap ? (
              <>
                <Descriptions size="small" column={1} bordered style={{ marginBottom: 12 }}>
                  <Descriptions.Item label={t('原因')}>{cap.reason || '—'}</Descriptions.Item>
                  <Descriptions.Item label={t('创建时间')}>{formatDateTime(cap.createdAt)}</Descriptions.Item>
                  <Descriptions.Item label={t('事件数')}>{cap.sseEvents ?? 0}</Descriptions.Item>
                  <Descriptions.Item label="sha256">
                    <Typography.Text code>{`${cap.requestSha256 || '—'} / ${cap.responseSha256 || '—'}`}</Typography.Text>
                  </Descriptions.Item>
                </Descriptions>
                {cap.requestHeaders && Object.keys(cap.requestHeaders).length > 0 ? (
                  <>
                    <Typography.Text strong>{t('请求头')}</Typography.Text>
                    <Descriptions size="small" column={1} bordered style={{ marginBottom: 12 }}>
                      {Object.entries(cap.requestHeaders).map(([key, value]) => (
                        <Descriptions.Item key={key} label={key}>
                          <Typography.Text code>{value}</Typography.Text>
                        </Descriptions.Item>
                      ))}
                    </Descriptions>
                  </>
                ) : null}
                {cap.responseHeaders && Object.keys(cap.responseHeaders).length > 0 ? (
                  <>
                    <Typography.Text strong>{t('响应头')}</Typography.Text>
                    <Descriptions size="small" column={1} bordered style={{ marginBottom: 12 }}>
                      {Object.entries(cap.responseHeaders).map(([key, value]) => (
                        <Descriptions.Item key={key} label={key}>
                          <Typography.Text code>{value}</Typography.Text>
                        </Descriptions.Item>
                      ))}
                    </Descriptions>
                  </>
                ) : null}
                {hasPreview ? (
                  <>
                    <Preview label={t('原始请求')} text={cap.requestOriginal} />
                    <Preview label={t('改写后请求')} text={cap.requestRewritten} />
                    <Preview label={t('原始响应')} text={cap.responseOriginal} />
                    <Preview label={t('改写后响应')} text={cap.responseRewritten} />
                    <Preview label={t('SSE 首事件（原始）')} text={cap.sseFirstOriginal} />
                    <Preview label={t('SSE 首事件（改写后）')} text={cap.sseFirstRewritten} />
                    <Preview label={t('SSE 末事件（原始）')} text={cap.sseLastOriginal} />
                    <Preview label={t('SSE 末事件（改写后）')} text={cap.sseLastRewritten} />
                  </>
                ) : (
                  <Typography.Text type="secondary">{t('预览为空')}</Typography.Text>
                )}
              </>
            ) : (
              <Typography.Text type="secondary">{t('无捕获数据')}</Typography.Text>
            )}
          </>
        ) : null}
      </Drawer>

      <Modal
        title={t('捕获规则')}
        open={captureOpen}
        onCancel={() => setCaptureOpen(false)}
        footer={null}
        width={640}
      >
        <Space direction="vertical" style={{ width: '100%' }}>
          <Input
            placeholder={t('路由（留空表示全部）')}
            value={captureRoute}
            onChange={(e) => setCaptureRoute(e.target.value)}
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
                <span>{`${r.route || '*'} · ${r.clientModel || '*'} · ${r.reason} · ${r.used}/${r.limit}`}</span>
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
