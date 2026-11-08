import { describe, expect, it } from 'vitest';
import { translateFor } from './i18n';
import { NAV_LABELS, NAV_ORDER } from './nav';

describe('NAV_ORDER', () => {
  it('places gateway keys before sessions and usage directly after them', () => {
    expect(NAV_ORDER).toEqual([
      'topology',
      'providers',
      'nodes',
      'routes',
      'entrypoints',
      'gateway-keys',
      'requests',
      'usage',
      'settings',
    ]);
  });
});

describe('gateway keys navigation', () => {
  it('labels the page in Chinese and English', () => {
    expect(NAV_LABELS['gateway-keys']).toBe('密钥');
    expect(translateFor('en-US', NAV_LABELS['gateway-keys'])).toBe('Key');
  });
});

describe('NAV_LABELS', () => {
  it('calls the topology page Overview', () => {
    expect(NAV_LABELS.topology).toBe('总览');
  });

  it('calls the requests page Sessions', () => {
    expect(NAV_LABELS.requests).toBe('会话');
    expect(translateFor('en-US', NAV_LABELS.requests)).toBe('Sessions');
  });
});
