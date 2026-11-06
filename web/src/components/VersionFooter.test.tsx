import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import { VersionFooter } from './VersionFooter';

describe('VersionFooter', () => {
  it('renders nothing when health is unavailable', () => {
    expect(renderToStaticMarkup(<VersionFooter health={null} />)).toBe('');
  });

  it('renders the version label with the full metadata in the title', () => {
    const html = renderToStaticMarkup(
      <VersionFooter health={{ ok: true, version: 'v0.1.0', commit: '694deb53b2c9', builtAt: '2026-11-06T10:00:00' }} />,
    );
    expect(html).toContain('v0.1.0 · 694deb5');
    expect(html).toContain('commit 694deb53b2c9 · built 2026-11-06 10:00:00');
  });
});
