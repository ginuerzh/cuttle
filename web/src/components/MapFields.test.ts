import { describe, expect, it } from 'vitest';
import { setActiveLang } from '../i18n';
import { mapToRows, rowsToMap } from './MapFields';

setActiveLang('zh-CN');

describe('rowsToMap', () => {
  it('round-trips a map through the row editor', () => {
    const map = { '/v1/messages': '/anthropic/v1/messages', '/v1/responses': '/chat/completions' };
    expect(rowsToMap(mapToRows(map), '路径')).toEqual(map);
  });

  it('ignores blank rows and trims keys and values', () => {
    expect(
      rowsToMap([
        { key: ' fast ', value: ' deepseek-v4-flash ' },
        { key: '', value: '' },
      ], '逻辑模型'),
    ).toEqual({ fast: 'deepseek-v4-flash' });
  });

  it('rejects a value without a key', () => {
    expect(() => rowsToMap([{ key: ' ', value: 'm1' }], '逻辑模型')).toThrow('逻辑模型不能为空');
  });

  it('rejects duplicate keys', () => {
    expect(() =>
      rowsToMap([
        { key: 'fast', value: 'm1' },
        { key: 'fast', value: 'm2' },
      ], '逻辑模型'),
    ).toThrow('重复的逻辑模型');
  });

  it('treats undefined as an empty map', () => {
    expect(rowsToMap(undefined)).toEqual({});
  });
});
