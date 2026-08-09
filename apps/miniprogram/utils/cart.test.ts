// utils/cart.test.ts — 本地购物车逻辑单元测试（PRD §4.3）。
import { describe, it, expect, beforeEach } from 'vitest';
import { resetStorage } from '../tests/setup';
import { add, setQuantity, loadCart, count, clear } from './cart';

describe('cart merge / dedup (PRD §4.3)', () => {
  beforeEach(() => resetStorage());

  it('merges same SKU + same option set', () => {
    add({ sku_id: 1, quantity: 2, option_ids: [10, 11] });
    add({ sku_id: 1, quantity: 3, option_ids: [10, 11] });
    const items = loadCart();
    expect(items).toHaveLength(1);
    expect(items[0].quantity).toBe(5);
  });

  it('does not merge when option ids differ', () => {
    add({ sku_id: 1, quantity: 1, option_ids: [10] });
    add({ sku_id: 1, quantity: 1, option_ids: [11] });
    expect(loadCart()).toHaveLength(2);
  });

  it('treats option order as same line identity (顺序不影响身份)', () => {
    add({ sku_id: 2, quantity: 1, option_ids: [11, 10] });
    add({ sku_id: 2, quantity: 1, option_ids: [10, 11] });
    // 同一 SKU + 同一组选项（顺序不同）应合并为 2。
    expect(loadCart()).toHaveLength(1);
    expect(loadCart()[0].quantity).toBe(2);
  });

  it('caps quantity at 99', () => {
    add({ sku_id: 3, quantity: 98, option_ids: [] });
    add({ sku_id: 3, quantity: 10, option_ids: [] });
    expect(loadCart()[0].quantity).toBe(99);
  });

  it('removes line when quantity drops to 0', () => {
    add({ sku_id: 4, quantity: 2, option_ids: [] });
    setQuantity(4, [], 0);
    expect(loadCart()).toHaveLength(0);
  });

  it('removes line when quantity goes negative', () => {
    add({ sku_id: 4, quantity: 2, option_ids: [] });
    setQuantity(4, [], -1);
    expect(loadCart()).toHaveLength(0);
  });

  it('does not allow quantity below 1 via setQuantity', () => {
    add({ sku_id: 5, quantity: 1, option_ids: [] });
    setQuantity(5, [], 0); // 删除
    expect(loadCart()).toHaveLength(0);
  });

  it('count sums all quantities', () => {
    add({ sku_id: 1, quantity: 2, option_ids: [] });
    add({ sku_id: 2, quantity: 3, option_ids: [] });
    expect(count()).toBe(5);
  });

  it('clear empties the cart', () => {
    add({ sku_id: 1, quantity: 1, option_ids: [] });
    clear();
    expect(loadCart()).toHaveLength(0);
    expect(count()).toBe(0);
  });

  it('start with empty cart', () => {
    expect(loadCart()).toHaveLength(0);
  });
});
