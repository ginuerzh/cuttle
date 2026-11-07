import { renderToStaticMarkup } from 'react-dom/server';
import { beforeEach, describe, expect, it } from 'vitest';
import { NodePanel } from './App';
import { setActiveLang } from './i18n';

beforeEach(() => setActiveLang('zh-CN'));

const metrics = {
  requests: 10, errors: 0, p50Ms: 3, p95Ms: 5,
  inputTokens: 0, outputTokens: 80, cachedTokens: 0,
  outputTokensPerSecond: 42.34,
};

describe('NodePanel', () => {
  it('shows output speed for a binding node', () => {
    const html = renderToStaticMarkup(
      <NodePanel id="node:fast" metrics={metrics} errors={[]} />,
    );
    expect(html).toContain('输出速度');
    expect(html).toContain('42.3 tok/s');
  });

  it('omits output speed for non-binding nodes', () => {
    const html = renderToStaticMarkup(
      <NodePanel id="provider:p" metrics={metrics} errors={[]} />,
    );
    expect(html).not.toContain('输出速度');
  });

  it('renders recent errors as cards with status and message', () => {
    const html = renderToStaticMarkup(
      <NodePanel
        id="node:fast"
        metrics={metrics}
        errors={[
          {
            requestId: 'r1',
            ts: '2026-11-07T10:20:30Z',
            route: 'fast',
            node: 'fast',
            provider: 'p',
            status: 502,
            message: 'upstream connect failed',
          },
        ]}
      />,
    );

    expect(html).toContain('ant-card');
    expect(html).toContain('502');
    expect(html).toContain('upstream connect failed');
  });
});
