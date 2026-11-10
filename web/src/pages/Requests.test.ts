import { describe, expect, it } from 'vitest';
import type { RequestRow } from '../api/types';
import { setActiveLang, translateFor } from '../i18n';
import { errorClassLabel, gatewayKeyLabel, retryLabel, statusLabel } from './Requests';

setActiveLang('zh-CN');

const base: RequestRow = {
  requestId: 'r',
  sid: 'r',
  ts: '2026-11-06T00:00:00Z',
  model: 'm',
  provider: 'p',
  protocol: 'chat',
  effort: '',
  status: 200,
  durationMs: 0,
  inputTokens: 0,
  outputTokens: 0,
  cachedTokens: 0,
  stream: false,
  bytesIn: 0,
  bytesOut: 0,
};

describe('request error labels', () => {
  it('renders a gateway timeout instead of status 0', () => {
    expect(statusLabel({ ...base, status: 0, errorClass: 'gateway_timeout' })).toBe('502 网关超时');
    expect(statusLabel({ ...base, status: 0, errorClass: 'network' })).toBe('502 上游失败');
  });

  it('keeps real status codes', () => {
    expect(statusLabel({ ...base, status: 401 })).toBe('401');
  });

  it('maps error classes to readable labels', () => {
    expect(errorClassLabel({ ...base, errorClass: 'upstream_auth' })).toBe('上游认证失败');
    expect(errorClassLabel({ ...base, errorClass: 'custom_class' })).toBe('custom_class');
    expect(errorClassLabel(base)).toBe('');
  });

  it('labels the gateway key only when present', () => {
    expect(gatewayKeyLabel(base)).toBe('');
    expect(gatewayKeyLabel({ ...base, gatewayKey: 'claude-code' })).toBe('key:claude-code');
  });

  it('labels retried sessions only when retries happened', () => {
    expect(retryLabel(base)).toBe('');
    expect(retryLabel({ ...base, retries: 0 })).toBe('');
    expect(retryLabel({ ...base, retries: 2 })).toBe('重试 2 次');
    expect(translateFor('en-US', '重试 {count} 次', { count: 2 })).toBe('retried 2×');
  });
});
