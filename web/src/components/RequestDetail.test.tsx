import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import type { RequestRow } from '../api/types';
import { setActiveLang } from '../i18n';
import { RequestDetail } from './RequestDetail';

setActiveLang('zh-CN');

const base: RequestRow = {
  requestId: 'r',
  sid: 'r',
  ts: '2026-11-06T00:00:00Z',
  model: 'm',
  provider: 'p_0000000001',
  providerName: 'p',
  node: '',
  nodeName: '',
  route: '',
  routeName: '',
  gatewayKey: '',
  gatewayKeyName: '',
  protocol: 'chat',
  effort: 'high',
  status: 200,
  durationMs: 12,
  inputTokens: 1500,
  outputTokens: 2500,
  cachedTokens: 0,
  stream: true,
  finishReason: 'stop',
  sawDone: true,
  bytesIn: 100,
  bytesOut: 200,
};

describe('RequestDetail', () => {
  it('shows the request effort and the shared count units', () => {
    const html = renderToStaticMarkup(<RequestDetail request={base} />);

    expect(html).toContain('effort');
    expect(html).toContain('high');
    expect(html).toContain('1.5K');
    expect(html).toContain('2.5K');
  });

  it('renders an unspecified effort as a dash', () => {
    expect(renderToStaticMarkup(<RequestDetail request={{ ...base, effort: '' }} />)).toContain('—');
    expect(renderToStaticMarkup(<RequestDetail request={{ ...base, effort: 'none' }} />)).toContain('—');
  });

  it('labels the route, provider, node and gateway key from the backend names', () => {
    const html = renderToStaticMarkup(
      <RequestDetail
        request={{
          ...base,
          provider: 'p_deleted0001',
          providerName: 'DeepSeek 官方',
          route: 'r_deleted0001',
          routeName: 'strong',
          node: 'n_deleted0001',
          nodeName: 'ds-strong',
          gatewayKey: 'gk_deleted001',
          gatewayKeyName: 'claude-code',
        }}
      />,
    );

    expect(html).toContain('DeepSeek 官方');
    expect(html).toContain('strong');
    expect(html).toContain('ds-strong');
    expect(html).toContain('claude-code');
    expect(html).not.toContain('p_deleted0001');
    expect(html).not.toContain('r_deleted0001');
    expect(html).not.toContain('gk_deleted001');
  });

  it('lists the discarded upstream attempts with their status and message', () => {
    const html = renderToStaticMarkup(
      <RequestDetail
        request={{
          ...base,
          retries: 1,
          attempts: [
            {
              requestId: 'r',
              attempt: 0,
              ts: '2026-11-06T00:00:00Z',
              route: 'r_0000000001',
              routeName: 'strong',
              node: 'n_0000000001',
              nodeName: 'ds-strong',
              provider: 'p_0000000001',
              providerName: 'DeepSeek 官方',
              status: 429,
              error: 'rate limit exceeded',
              errorClass: 'quota',
              durationMs: 40,
              bytesIn: 100,
              bytesOut: 20,
            },
          ],
        }}
      />,
    );

    expect(html).toContain('上游尝试');
    expect(html).toContain('#1');
    expect(html).toContain('429');
    expect(html).toContain('rate limit exceeded');
    expect(html).toContain('40ms');
    expect(html).toContain('DeepSeek 官方/ds-strong');
    expect(html.indexOf('上游尝试')).toBeLessThan(html.indexOf('捕获详情'));
  });

  it('shows a failed dial as a status-less attempt', () => {
    const html = renderToStaticMarkup(
      <RequestDetail
        request={{
          ...base,
          attempts: [
            {
              requestId: 'r',
              attempt: 2,
              ts: '2026-11-06T00:00:00Z',
              routeName: '',
              nodeName: '',
              providerName: '',
              status: 0,
              error: 'dial tcp: connection refused',
              durationMs: 5,
              bytesIn: 0,
              bytesOut: 0,
            },
          ],
        }}
      />,
    );

    expect(html).toContain('#3');
    expect(html).toContain('连接失败');
    expect(html).toContain('dial tcp: connection refused');
    expect(html).toContain('unrouted');
  });

  it('omits the attempt section when the row carries none', () => {
    expect(renderToStaticMarkup(<RequestDetail request={base} />)).not.toContain('上游尝试');
    expect(renderToStaticMarkup(<RequestDetail request={{ ...base, attempts: [] }} />)).not.toContain('上游尝试');
  });
});
