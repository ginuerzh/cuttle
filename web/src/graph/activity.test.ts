import { describe, expect, it } from 'vitest';
import type { GraphMetrics } from '../api/types';
import { accumulateActivity, diffActivity, emptyActivity, flowDuration, mergeActivity } from './activity';

function metrics(nodes: Record<string, [number, number]>, edges: Record<string, [number, number]> = {}): GraphMetrics {
  const nodeMap = Object.fromEntries(
    Object.entries(nodes).map(([id, [requests, errors]]) => [
      id,
      { requests, errors, p50Ms: 0, p95Ms: 0, inputTokens: 0, outputTokens: 0, cachedTokens: 0, outputTokensPerSecond: 0 },
    ]),
  );
  const edgeMap = Object.fromEntries(
    Object.entries(edges).map(([id, [requests, errors]]) => [id, { requests, errors }]),
  );
  return { nodes: nodeMap, edges: edgeMap, errors: [] };
}

describe('flowDuration', () => {
  it('speeds up with more requests per tick but stays bounded', () => {
    expect(flowDuration(1)).toBeCloseTo(0.9);
    expect(flowDuration(4)).toBeCloseTo(0.45);
    expect(flowDuration(100)).toBeCloseTo(0.45);
  });
});

describe('diffActivity', () => {
  it('does not animate the first snapshot', () => {
    expect(diffActivity(null, metrics({ 'node:a': [10, 0] }), 1)).toEqual(emptyActivity());
  });

  it('marks nodes and edges that gained requests with the current pulse', () => {
    const before = metrics({ 'node:a': [2, 0] }, { 'edge:a': [2, 0] });
    const after = metrics({ 'node:a': [5, 1] }, { 'edge:a': [5, 1] });

    const activity = diffActivity(before, after, 7);
    expect(activity.nodes['node:a']).toMatchObject({ pulse: 7, error: true });
    expect(activity.edges['edge:a']).toMatchObject({ pulse: 7, error: true });
  });

  it('does not animate when the window resets', () => {
    const before = metrics({ 'node:a': [10, 0] });
    const after = metrics({ 'node:a': [1, 0] });

    expect(diffActivity(before, after, 7)).toEqual(emptyActivity());
  });
});

describe('mergeActivity', () => {
  it('keeps the last pulse for untouched nodes and replaces updated ones', () => {
    const current = {
      nodes: { a: { pulse: 1, error: false, duration: 0.9 } },
      edges: {},
    };
    const next = {
      nodes: { b: { pulse: 2, error: true, duration: 0.6 } },
      edges: {},
    };

    expect(mergeActivity(current, next)).toEqual({
      nodes: {
        a: { pulse: 1, error: false, duration: 0.9 },
        b: { pulse: 2, error: true, duration: 0.6 },
      },
      edges: {},
    });
  });

  it('returns the current map when there is no new activity', () => {
    const current = emptyActivity();
    expect(mergeActivity(current, emptyActivity())).toBe(current);
  });
});

describe('accumulateActivity', () => {
  const before = metrics({ 'node:a': [2, 0] });
  const after = metrics({ 'node:a': [5, 1] });

  it('merges new traffic while animations are on', () => {
    const out = accumulateActivity(emptyActivity(), before, after, true, 7);
    expect(out.nodes['node:a']).toMatchObject({ pulse: 7, error: true });
  });

  it('clears activity while animations are off', () => {
    const current = {
      nodes: { 'node:a': { pulse: 1, error: false, duration: 0.9 } },
      edges: {},
    };
    expect(accumulateActivity(current, before, after, false, 7)).toEqual(emptyActivity());
  });

  it('does not replay activity recorded while animations were off', () => {
    const cleared = accumulateActivity(emptyActivity(), before, after, false, 7);
    const reenabled = accumulateActivity(cleared, after, after, true, 8);
    expect(reenabled).toEqual(emptyActivity());
  });

  it('does not animate the first snapshot', () => {
    expect(accumulateActivity(emptyActivity(), null, after, true, 7)).toEqual(emptyActivity());
  });
});
