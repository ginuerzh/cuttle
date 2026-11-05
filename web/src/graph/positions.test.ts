import { describe, expect, it } from 'vitest';
import { mergePositions } from './positions';

describe('mergePositions', () => {
  it('fills missing nodes while keeping saved positions', () => {
    expect(
      mergePositions(
        { b: { x: 200, y: 40 } },
        [
          { id: 'a', position: { x: 0, y: 0 } },
          { id: 'b', position: { x: 999, y: 999 } },
        ],
      ),
    ).toEqual({
      a: { x: 0, y: 0 },
      b: { x: 200, y: 40 },
    });
  });

  it('returns the original map when every node already has a position', () => {
    const saved = { a: { x: 1, y: 2 } };
    expect(mergePositions(saved, [{ id: 'a', position: { x: 3, y: 4 } }])).toBe(saved);
  });
});
