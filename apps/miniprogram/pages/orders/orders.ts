// pages/orders/orders.ts — 我的订单列表（PRD §4.9）。
import { request, ensureSession } from '../../utils/api';

interface Order {
  id: number;
  order_no: string;
  pickup_no?: number;
  pickup_type: string;
  scheduled_for?: string;
  payable_cents: number;
  fulfillment_state: string;
}

const STATE_LABEL: Record<string, string> = {
  PENDING_PAYMENT: '待支付',
  PAID: '已支付',
  ACCEPTED: '制作中',
  PREPARING: '制作中',
  READY: '待取餐',
  COMPLETED: '已完成',
  CANCELLED: '已取消',
  REFUNDING: '退款中',
  REFUNDED: '已退款',
};

Page({
  data: { filter: '', orders: [] as (Order & { label: string })[] },

  onLoad() {
    this.load();
  },

  async load() {
    try {
      await ensureSession();
      const r = await request<{ items: Order[] }>('GET', '/api/v1/orders' + (this.data.filter ? '?status=' + this.data.filter : ''));
      this.setData({
        orders: (r.items || []).map((o) => ({ ...o, label: STATE_LABEL[o.fulfillment_state] || o.fulfillment_state })),
      });
    } catch (e) {
      wx.showToast({ title: '加载失败', icon: 'none' });
    }
  },

  setFilter(e: WechatMiniprogram.TouchEvent) {
    this.setData({ filter: e.currentTarget.dataset.status as string }, () => this.load());
  },

  detail(e: WechatMiniprogram.TouchEvent) {
    wx.navigateTo({ url: '/pages/order-detail/order-detail?id=' + e.currentTarget.dataset.id });
  },
});
