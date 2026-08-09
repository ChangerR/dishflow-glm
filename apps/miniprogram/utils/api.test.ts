// utils/api.test.ts — 金额分/元转换测试（PRD §4.5：UI 只负责把分格式化为元）。
import { describe, it, expect } from 'vitest';
import { centsToYuan } from './api';

describe('centsToYuan (miniprogram)', () => {
  it('formats positive cents to yuan', () => {
    expect(centsToYuan(0)).toBe('0.00');
    expect(centsToYuan(1)).toBe('0.01');
    expect(centsToYuan(99)).toBe('0.99');
    expect(centsToYuan(100)).toBe('1.00');
    expect(centsToYuan(12345)).toBe('123.45');
  });

  it('formats negative cents', () => {
    expect(centsToYuan(-100)).toBe('-1.00');
    expect(centsToYuan(-5)).toBe('-0.05');
  });

  it('pads single-digit fen', () => {
    expect(centsToYuan(101)).toBe('1.01');
    expect(centsToYuan(110)).toBe('1.10');
  });
});
