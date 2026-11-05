import { describe, expect, it } from 'vitest';
import { selectionStyle } from './nodes';

describe('selectionStyle', () => {
  it('draws selection inside the box without changing its size', () => {
    const selected = selectionStyle(true, '#16a34a');
    expect(selected.borderColor).toBe('#0ea5e9');
    expect(selected.boxShadow).toContain('inset');
    expect(selected).not.toHaveProperty('borderWidth');
  });
});
