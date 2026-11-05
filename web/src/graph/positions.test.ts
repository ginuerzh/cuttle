import { describe, expect, it } from 'vitest';
import { createPositionSaver, mergePositions, pickInitialPositions } from './positions';

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

describe('pickInitialPositions', () => {
  it('uses a non-empty server map and ignores the local map', () => {
    const server = { a: { x: 1, y: 2 } };
    const local = { a: { x: 9, y: 9 } };
    expect(pickInitialPositions(server, local)).toEqual({ positions: server, seed: null });
  });

  it('uses the local map as a seed when the server is empty', () => {
    const local = { a: { x: 3, y: 4 } };
    expect(pickInitialPositions({}, local)).toEqual({ positions: local, seed: local });
  });

  it('starts empty when both stores are empty', () => {
    expect(pickInitialPositions({}, {})).toEqual({ positions: {}, seed: null });
  });
});

describe('createPositionSaver', () => {
  it('runs saves in call order', async () => {
    const calls: string[] = [];
    const save = async (p: Record<string, { x: number; y: number }>) => {
      calls.push(Object.keys(p)[0]);
    };
    const saver = createPositionSaver(save);
    await Promise.all([saver({ a: { x: 1, y: 1 } }), saver({ b: { x: 2, y: 2 } })]);
    expect(calls).toEqual(['a', 'b']);
  });
});
