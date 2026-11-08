import { renderToStaticMarkup } from 'react-dom/server';
import { beforeEach, describe, expect, it } from 'vitest';
import { setActiveLang } from '../i18n';
import { OverviewToolbar } from './OverviewToolbar';

const RANGES = [
  { label: '1h', value: '1h' },
  { label: '6h', value: '6h' },
];

beforeEach(() => {
  setActiveLang('zh-CN');
});

describe('OverviewToolbar', () => {
  it('uses one edit toggle and right-aligns a narrower range selector', () => {
    const html = renderToStaticMarkup(
      <OverviewToolbar
        mode="monitor"
        onMode={() => undefined}
        range="1h"
        onRange={() => undefined}
        ranges={RANGES}
        stale={false}
        updatedAt="2026-11-06 12:00:00"
        sinceAt="2026-11-06 11:00:00"
        onReset={() => undefined}
        isMobile={false}
        animate={true}
        onAnimate={() => undefined}
      />,
    );

    expect(html).toContain('justify-content:space-between');
    expect(html).toContain('>编辑<');
    expect(html).not.toContain('>监控<');
    expect(html).toContain('width:96px');
    expect(html).toContain('ant-select');
    expect(html).toContain('ant-switch');
    expect(html).toContain('aria-checked="true"');
    expect(html).toContain('>动画<');
    expect(html).toContain('>重置统计<');
    expect(html).toContain('更新 2026-11-06 12:00:00 · 统计自 2026-11-06 11:00:00');

    const animationsOff = renderToStaticMarkup(
      <OverviewToolbar
        mode="monitor"
        onMode={() => undefined}
        range="1h"
        onRange={() => undefined}
        ranges={RANGES}
        stale={false}
        updatedAt="2026-11-06 12:00:00"
        sinceAt="2026-11-06 11:00:00"
        onReset={() => undefined}
        isMobile={false}
        animate={false}
        onAnimate={() => undefined}
      />,
    );

    expect(animationsOff).toContain('aria-checked="false"');
  });

  it('turns the toggle off in edit mode and hides the paused range selector', () => {
    const html = renderToStaticMarkup(
      <OverviewToolbar
        mode="edit"
        onMode={() => undefined}
        range="1h"
        onRange={() => undefined}
        ranges={RANGES}
        stale={false}
        updatedAt="2026-11-06 12:00:00"
        sinceAt="2026-11-06 11:00:00"
        onReset={() => undefined}
        isMobile={false}
        animate={true}
        onAnimate={() => undefined}
      />,
    );

    expect(html).toContain('>退出编辑<');
    expect(html).toContain('编辑模式 · 统计已暂停');
    expect(html).not.toContain('ant-select');
    expect(html).not.toContain('ant-switch');
    expect(html).not.toContain('重置统计');
  });
});
