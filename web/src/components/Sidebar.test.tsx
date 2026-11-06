import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import { Sidebar } from '../App';
import { I18nProvider } from '../i18n';

describe('Sidebar', () => {
  it('centers all three footer button labels', () => {
    const html = renderToStaticMarkup(
      <I18nProvider>
        <Sidebar
          page="topology"
          onPage={() => undefined}
          themeMode="system"
          onThemeMode={() => undefined}
          onLogout={() => undefined}
          health={null}
        />
      </I18nProvider>,
    );
    expect(html.match(/justify-content:center/g)).toHaveLength(3);
  });
});
