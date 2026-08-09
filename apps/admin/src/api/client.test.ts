import { describe, it, expect } from 'vitest';
import { centsToYuan } from './client';

describe('centsToYuan', () => {
  it('formats cents to yuan with 2 decimals', () => {
    expect(centsToYuan(0)).toBe('0.00');
    expect(centsToYuan(1)).toBe('0.01');
    expect(centsToYuan(99)).toBe('0.99');
    expect(centsToYuan(100)).toBe('1.00');
    expect(centsToYuan(12345)).toBe('123.45');
  });
  it('handles negative amounts', () => {
    expect(centsToYuan(-100)).toBe('-1.00');
    expect(centsToYuan(-5)).toBe('-0.05');
  });
  it('pads single-digit fen', () => {
    expect(centsToYuan(101)).toBe('1.01');
    expect(centsToYuan(110)).toBe('1.10');
  });
});
