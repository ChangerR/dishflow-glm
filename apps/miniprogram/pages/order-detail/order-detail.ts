// pages/order-detail/order-detail.ts — 订单详情与履约时间线（PRD §4.9）。
import { request, ensureSession } from '../../utils/api';

interface OrderDetail {
  id: number;
  order_no: string;
  pickup_no?: number;
  pickup_type: string;
  scheduled_for?: string;
  payable_cents: number;
  fulfillment_state: string;
  items: { sku_name: string; quantity: number; line_amount_cents: number }[];
  events: { event_type: string; summary: string; created_at: string }[];
}

Page({
  data: { order: null as OrderDetail | null, paying: false },

  onLoad(query: Record<string, string>) {
    const id = query.id;
    this.load(Number(id));
    // 支付后轮询直到服务端确认（PRD §4.8.3）。
    this.poller = setInterval(() => this.load(Number(id)), 3000);
  },
  poller: 0 as unknown as number,

  onUnload() {
    if (this.poller) clearInterval(this.poller);
  },

  async load(id: number) {
    try {
      await ensureSession();
      const o = await request<OrderDetail>('GET', '/api/v1/orders/' + id);
      this.setData({ order: o });
      // 终态停止轮询。
      if (['COMPLETED', 'CANCELLED', 'REFUNDED'].includes(o.fulfillment_state) && this.poller) {
        clearInterval(this.poller);
        this.poller = 0;
      }
    } catch (e) {
      wx.showToast({ title: '加载失败', icon: 'none' });
    }
  },
});
