// pages/cart/cart.ts — 本地购物车展示（PRD §4.3）。
import { loadCart, setQuantity, count } from '../../utils/cart';

Page({
  data: { items: [] as { sku_id: number; quantity: number; option_ids: number[] }[], count: 0 },
  onShow() {
    this.setData({ items: loadCart(), count: count() });
  },
  dec(e: WechatMiniprogram.TouchEvent) {
    const i = e.currentTarget.dataset.item as { sku_id: number; option_ids: number[]; quantity: number };
    setQuantity(i.sku_id, i.option_ids, i.quantity - 1);
    this.setData({ items: loadCart() });
  },
  inc(e: WechatMiniprogram.TouchEvent) {
    const i = e.currentTarget.dataset.item as { sku_id: number; option_ids: number[]; quantity: number };
    setQuantity(i.sku_id, i.option_ids, i.quantity + 1);
    this.setData({ items: loadCart() });
  },
  checkout() {
    wx.navigateTo({ url: '/pages/checkout/checkout' });
  },
});
