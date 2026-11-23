import { describe, expect, it } from 'vitest';

import { formatBytes, isActive } from './format';

describe('formatBytes', () => {
  it('picks a unit and keeps a sensible precision', () => {
    expect(formatBytes(0)).toBe('0 B');
    expect(formatBytes(999)).toBe('999 B');
    expect(formatBytes(1500)).toBe('1.5 KB');
    expect(formatBytes(734_003_200)).toBe('734 MB');
    expect(formatBytes(4_700_000_000)).toBe('4.7 GB');
    expect(formatBytes(5e15)).toBe('5000 TB');
  });
});

describe('isActive', () => {
  it('is true only while the server is still working on a request', () => {
    expect(isActive('downloading')).toBe(true);
    expect(isActive('imported')).toBe(false);
    expect(isActive('failed')).toBe(false);
  });
});
