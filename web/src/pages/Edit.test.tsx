import { renderToStaticMarkup } from 'react-dom/server';
import { beforeEach, describe, expect, it } from 'vitest';
import { AddButtons } from './Edit';
import { setActiveLang } from '../i18n';

beforeEach(() => setActiveLang('zh-CN'));

describe('AddButtons', () => {
  it('uses the Chinese source labels', () => {
    const html = renderToStaticMarkup(<AddButtons onAdd={() => undefined} />);

    expect(html).toContain('添加提供商');
    expect(html).toContain('添加节点');
    expect(html).toContain('添加路由');
    expect(html).toContain('添加入口');
  });

  it('translates every add action', () => {
    setActiveLang('en-US');
    const html = renderToStaticMarkup(<AddButtons onAdd={() => undefined} />);

    expect(html).toContain('Add Provider');
    expect(html).toContain('Add Node');
    expect(html).toContain('Add Route');
    expect(html).toContain('Add Entrypoint');
    expect(html).not.toContain('添加提供商');
  });
});
