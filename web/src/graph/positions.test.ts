import { describe, expect, it } from 'vitest';
import { createPositionSaver, mergeNodeState, mergePositions, pickInitialPositions } from './positions';

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

describe('mergeNodeState', () => {
  it('keeps React Flow measurements and selection when rebuilding nodes', () => {
    const current = [
      {
        id: 'node:a',
        position: { x: 1, y: 2 },
        measured: { width: 250, height: 96 },
        selected: true,
        data: { label: 'old', metrics: { requests: 4 } },
      },
    ];
    const next = [
      {
        id: 'node:a',
        position: { x: 10, y: 20 },
        data: { label: 'new' },
      },
    ];

    expect(mergeNodeState(current, next)).toEqual([
      {
        id: 'node:a',
        position: { x: 10, y: 20 },
        measured: { width: 250, height: 96 },
        selected: true,
        data: { label: 'new', metrics: { requests: 4 } },
      },
    ]);
  });

  it('adds new nodes and drops removed ones', () => {
    const current = [{ id: 'a', position: { x: 0, y: 0 }, data: {} }];
    const next = [
      { id: 'a', position: { x: 1, y: 1 }, data: {} },
      { id: 'b', position: { x: 2, y: 2 }, data: {} },
    ];

    expect(mergeNodeState(current, next).map((n) => n.id)).toEqual(['a', 'b']);
  });
});

describe('pickInitialPositions', () => {
  it('uses a non-empty server map and ignores the local map', () => {
    const server = { a: { x: 1, y: 2 } };
    const local = { a: { x: 9, y: 9 } };
    expect(pickInitialPositions(server, local, false, false)).toEqual({ positions: server, seed: null });
  });

  it('uses the local map as a seed when the server is empty', () => {
    const local = { a: { x: 3, y: 4 } };
    expect(pickInitialPositions({}, local, false, false)).toEqual({ positions: local, seed: local });
  });

  it('starts empty when both stores are empty', () => {
    expect(pickInitialPositions({}, {}, false, false)).toEqual({ positions: {}, seed: null });
  });

  it('keeps a pending local map over a non-empty server map', () => {
    const server = { a: { x: 1, y: 2 } };
    const local = { a: { x: 9, y: 9 } };
    expect(pickInitialPositions(server, local, true, false)).toEqual({
      positions: local,
      seed: local,
    });
  });

  it('keeps the current map when a local edit raced the server read', () => {
    expect(
      pickInitialPositions({ a: { x: 1, y: 2 } }, { a: { x: 9, y: 9 } }, false, true),
    ).toEqual({ positions: null, seed: null });
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
