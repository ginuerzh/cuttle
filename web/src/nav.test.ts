import { describe, expect, it } from 'vitest';
import { NAV_LABELS, NAV_ORDER } from './nav';

describe('NAV_ORDER', () => {
  it('places usage directly after requests', () => {
    expect(NAV_ORDER).toEqual([
      'topology',
      'providers',
      'nodes',
      'routes',
      'entrypoints',
      'requests',
      'usage',
      'settings',
    ]);
  });
});

describe('NAV_LABELS', () => {
  it('calls the topology page Overview', () => {
    expect(NAV_LABELS.topology).toBe('总览');
  });
});
