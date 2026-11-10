import { useEffect, useState } from 'react';
import { Button, Descriptions, Drawer, Space, Tag, Typography, message } from 'antd';
import { getRequest } from '../api/client';
import type { RequestRow } from '../api/types';
import { isTruncated } from '../capture';
import { formatCount } from '../format';
import { translate as t } from '../i18n';
import { errorClassLabel, statusLabel } from '../requestLabels';
import { formatDateTime } from '../time';
import { useIsMobile } from './useIsMobile';

function Preview({ label, text, truncated }: { label: string; text?: string; truncated?: boolean }) {
  if (!text) return null;
  return (
    <div style={{ marginBottom: 12 }}>
      <Space size={8}>
        <Typography.Text strong>{label}</Typography.Text>
        {truncated ? <Tag color="warning">{t('已截断')}</Tag> : null}
      </Space>
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

async function copyID(id: string) {
  try {
    await navigator.clipboard.writeText(id);
    message.success(t('已复制'));
  } catch {
    message.error(t('复制失败'));
  }
}

/** One request's full detail; shared by the Sessions page and the monitor panel. */
export function RequestDetail({ request }: { request: RequestRow }) {
  const cap = request.capture;
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
    <>
      <Descriptions size="small" column={1} bordered>
        <Descriptions.Item label="requestId">
          <Space>
            <span>{request.requestId}</span>
            <Button size="small" onClick={() => void copyID(request.requestId)}>
              {t('复制')}
            </Button>
          </Space>
        </Descriptions.Item>
        <Descriptions.Item label="sid">{request.sid}</Descriptions.Item>
        <Descriptions.Item label={t('时间')}>{formatDateTime(request.ts)}</Descriptions.Item>
        <Descriptions.Item label="route">{request.route || '—'}</Descriptions.Item>
        <Descriptions.Item label={t('请求密钥')}>{request.gatewayKey || '—'}</Descriptions.Item>
        <Descriptions.Item label="provider">{request.provider}</Descriptions.Item>
        <Descriptions.Item label="client model">{request.clientModel || '—'}</Descriptions.Item>
        <Descriptions.Item label="provider model">{request.model}</Descriptions.Item>
        <Descriptions.Item label="effort">
          {request.effort && request.effort !== 'none' ? request.effort : '—'}
        </Descriptions.Item>
        <Descriptions.Item label={t('入站协议')}>{request.inboundProtocol || '—'}</Descriptions.Item>
        <Descriptions.Item label={t('上游协议')}>{request.protocol || '—'}</Descriptions.Item>
        <Descriptions.Item label={t('请求路径')}>
          {`${request.method || '—'} ${request.uri || '—'}`}
        </Descriptions.Item>
        <Descriptions.Item label="client ip">{request.clientIp || '—'}</Descriptions.Item>
        <Descriptions.Item label="user agent">{request.userAgent || '—'}</Descriptions.Item>
        <Descriptions.Item label="upstream">{request.upstream || '—'}</Descriptions.Item>
        <Descriptions.Item label={t('错误阶段')}>{request.errorStage || '—'}</Descriptions.Item>
        <Descriptions.Item label={t('错误方向')}>{request.errorDirection || '—'}</Descriptions.Item>
        <Descriptions.Item label={t('错误分类')}>{errorClassLabel(request) || '—'}</Descriptions.Item>
        <Descriptions.Item label="SSE phase / index">
          {request.ssePhase ? `${request.ssePhase} / ${request.eventIndex ?? 0}` : '— / —'}
        </Descriptions.Item>
        <Descriptions.Item label="status / error">
          <Typography.Text type={request.error || request.status >= 400 ? 'danger' : undefined}>
            {`${statusLabel(request)}${request.clientStatus && request.clientStatus !== request.status ? ` (client ${request.clientStatus})` : ''} ${request.error || ''}`}
          </Typography.Text>
        </Descriptions.Item>
        <Descriptions.Item label={t('耗时')}>{`${request.durationMs} ms`}</Descriptions.Item>
        <Descriptions.Item label="retries">{request.retries ?? 0}</Descriptions.Item>
        <Descriptions.Item label="tokens">
          {t('入 {input} · 出 {output} · 缓存 {cached}', {
            input: formatCount(request.inputTokens),
            output: formatCount(request.outputTokens),
            cached: formatCount(request.cachedTokens),
          })}
        </Descriptions.Item>
        <Descriptions.Item label="bytes">
          {`${request.bytesIn} in / ${request.bytesOut} out`}
        </Descriptions.Item>
        <Descriptions.Item label="stream">{request.stream ? t('是') : t('否')}</Descriptions.Item>
        <Descriptions.Item label={t('流式终止')}>
          {request.stream
            ? `${request.finishReason || '—'} / done=${request.sawDone ? t('是') : t('否')}${request.truncated ? ` / ${t('已截断')}` : ''}`
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
              <Preview label={t('原始请求')} text={cap.requestOriginal} truncated={isTruncated(cap, 'requestOriginal')} />
              <Preview label={t('改写后请求')} text={cap.requestRewritten} truncated={isTruncated(cap, 'requestRewritten')} />
              <Preview label={t('原始响应')} text={cap.responseOriginal} truncated={isTruncated(cap, 'responseOriginal')} />
              <Preview label={t('改写后响应')} text={cap.responseRewritten} truncated={isTruncated(cap, 'responseRewritten')} />
              <Preview label={t('SSE 首事件（原始）')} text={cap.sseFirstOriginal} truncated={isTruncated(cap, 'sseFirstOriginal')} />
              <Preview label={t('SSE 首事件（改写后）')} text={cap.sseFirstRewritten} truncated={isTruncated(cap, 'sseFirstRewritten')} />
              <Preview label={t('SSE 末事件（原始）')} text={cap.sseLastOriginal} truncated={isTruncated(cap, 'sseLastOriginal')} />
              <Preview label={t('SSE 末事件（改写后）')} text={cap.sseLastRewritten} truncated={isTruncated(cap, 'sseLastRewritten')} />
            </>
          ) : (
            <Typography.Text type="secondary">{t('预览为空')}</Typography.Text>
          )}
        </>
      ) : (
        <Typography.Text type="secondary">{t('无捕获数据')}</Typography.Text>
      )}
    </>
  );
}

/**
 * Detail drawer that loads the full row (including capture data) on open. The
 * list row is shown immediately so switching requests never flashes empty.
 */
export function RequestDetailDrawer({
  request,
  onClose,
}: {
  request: RequestRow | null;
  onClose: () => void;
}) {
  const isMobile = useIsMobile();
  const [detail, setDetail] = useState<RequestRow | null>(request);

  useEffect(() => {
    setDetail(request);
    if (!request) return;
    let active = true;
    getRequest(request.requestId)
      .then((full) => {
        if (active) setDetail(full);
      })
      .catch((err) => {
        if (active) message.error(err instanceof Error ? err.message : String(err));
      });
    return () => {
      active = false;
    };
  }, [request]);

  return (
    <Drawer
      width={isMobile ? '100%' : 560}
      title={t('会话详情')}
      open={!!request}
      onClose={onClose}
      destroyOnClose
    >
      {detail ? <RequestDetail request={detail} /> : null}
    </Drawer>
  );
}
