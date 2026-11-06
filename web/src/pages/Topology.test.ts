import { describe, expect, it } from 'vitest';
import { canEditTopology } from './Topology';

describe('canEditTopology', () => {
  it('keeps monitor mode read-only when editable is omitted', () => {
    expect(canEditTopology(undefined)).toBe(false);
    expect(canEditTopology(false)).toBe(false);
  });

  it('allows connections and dragging only in explicit edit mode', () => {
    expect(canEditTopology(true)).toBe(true);
  });
});
