// utils/cart.ts — 本地购物车（PRD §4.3）。
//
// 规则：
//   - 购物车只在本地保存 SKU ID、选项 ID、数量，不保存可信价格。
//   - 同一 SKU + 同一组选项 ID（顺序无关）自动合并。
//   - 单行数量 1..99；数量降为 0 时删除。
//   - 选项顺序不影响购物车行身份。

export interface CartItem {
  sku_id: number;
  quantity: number;
  option_ids: number[];
}

function key(item: CartItem): string {
  const opts = [...item.option_ids].sort((a, b) => a - b);
  return item.sku_id + ':' + opts.join(',');
}

export function loadCart(): CartItem[] {
  return wx.getStorageSync('cart') || [];
}

function saveCart(items: CartItem[]): void {
  wx.setStorageSync('cart', items);
}

// 加/减某行（合并同 key），数量≤0 删除。
export function add(item: CartItem): CartItem[] {
  const items = loadCart();
  const k = key(item);
  const idx = items.findIndex((i) => key(i) === k);
  if (idx >= 0) {
    items[idx].quantity = Math.min(99, items[idx].quantity + item.quantity);
  } else {
    items.push({ ...item });
  }
  saveCart(items);
  return items;
}

export function setQuantity(sku_id: number, option_ids: number[], quantity: number): CartItem[] {
  const items = loadCart();
  const k = key({ sku_id, quantity: 0, option_ids });
  const idx = items.findIndex((i) => key(i) === k);
  if (quantity <= 0) {
    if (idx >= 0) items.splice(idx, 1);
  } else if (idx >= 0) {
    items[idx].quantity = Math.max(1, Math.min(99, quantity));
  }
  saveCart(items);
  return items;
}

export function clear(): void {
  wx.removeStorageSync('cart');
}

export function count(): number {
  return loadCart().reduce((s, i) => s + i.quantity, 0);
}
