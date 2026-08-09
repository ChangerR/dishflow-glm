// pages/checkout/checkout.ts — 结算（PRD §4.6/§4.7/§4.8）。
// 流程：选择预约时段（自取）→ 服务端报价（quote_token）→ 创建订单 → 支付 → 轮询确认。

import { request, ensureSession, centsToYuan } from '../../utils/api';
import { loadCart, clear } from '../../utils/cart';

interface Quote {
  payable_cents: number;
  item_amount_cents: number;
  packaging_fee_cents: number;
  discount_cents: number;
  quote_token: string;
  expires_at: number;
}

interface Slot {
  starts_at: string;
  label: string;
  capacity: number;
  remaining: number;
  available: boolean;
}

Page({
  data: {
    scenario: 'PICKUP' as 'DINE_IN' | 'PICKUP',
    pickupType: 'IMMEDIATE' as 'IMMEDIATE' | 'SCHEDULED',
    tableLabel: '',
    slots: [] as Slot[],
    selectedSlot: '',
    quote: null as Quote | null,
    remark: '',
    loading: false,
    payable: '',
  },

  onLoad() {
    const app = getApp();
    this.setData({ scenario: app.globalData.scenario as 'DINE_IN' | 'PICKUP', tableLabel: app.globalData.tableLabel });
    this.preview();
  },

  switchPickupType(e: WechatMiniprogram.TouchEvent) {
    const t = e.currentTarget.dataset.type as 'IMMEDIATE' | 'SCHEDULED';
    this.setData({ pickupType: t, selectedSlot: '', quote: null });
    if (t === 'SCHEDULED') {
      this.loadSlots(new Date().toISOString().slice(0, 10));
    } else {
      this.preview();
    }
  },

  loadSlots(date: string) {
    request<{ slots: Slot[] }>('GET', '/api/v1/pickup-slots?date=' + date)
      .then((r) => this.setData({ slots: r.slots }))
      .catch(() => wx.showToast({ title: '时段加载失败', icon: 'none' }));
  },

  selectSlot(e: WechatMiniprogram.TouchEvent) {
    const startsAt = e.currentTarget.dataset.starts as string;
    if (!e.currentTarget.dataset.available) return;
    this.setData({ selectedSlot: startsAt });
    this.preview();
  },

  // 服务端算价并签发 quote（PRD §4.4）。客户端不提交可信价格。
  preview() {
    const cart = loadCart();
    if (cart.length === 0) {
      wx.showToast({ title: '购物车为空', icon: 'none' });
      return;
    }
    const app = getApp();
    const scheduledFor = this.data.pickupType === 'SCHEDULED' ? this.data.selectedSlot : '';
    const body = {
      scenario: this.data.scenario,
      table_token: this.data.scenario === 'DINE_IN' ? app.globalData.tableToken : '',
      scheduled_for: scheduledFor,
      cart,
    };
    this.setData({ loading: true });
    request<Quote>('POST', '/api/v1/pricing/preview', body)
      .then((q) => {
        this.setData({ quote: q, payable: centsToYuan(q.payable_cents), loading: false });
      })
      .catch((err) => {
        this.setData({ loading: false });
        if (err.code === 'PICKUP_SLOT_FULL') {
          wx.showToast({ title: '该时段刚刚约满，请重新选择', icon: 'none' });
          this.setData({ selectedSlot: '', quote: null });
        } else {
          wx.showToast({ title: err.message || '报价失败', icon: 'none' });
        }
      });
  },

  inputRemark(e: WechatMiniprogram.Input) {
    this.setData({ remark: (e.detail.value || '').slice(0, 100) });
  },

  // 创建订单（信任 quote_token，PRD §4.7）→ 支付 → 轮询服务端状态（PRD §4.8.3）。
  async submit() {
    if (!this.data.quote) return;
    if (this.data.quote.expires_at < Date.now() / 1000) {
      wx.showToast({ title: '报价已过期，重新报价', icon: 'none' });
      this.preview();
      return;
    }
    try {
      await ensureSession();
      // 创建订单。
      const order = await request<{ id: number; order_no: string }>('POST', '/api/v1/orders', {
        quote_token: this.data.quote!.quote_token,
        remark: this.data.remark,
      });
      clear();
      // 预支付。
      const prepay = await request<{ prepay_id: string; mock_payment: boolean; jsapi_payload: Record<string, string> }>(
        'POST',
        '/api/v1/orders/' + order.id + '/prepay',
      );
      if (prepay.mock_payment) {
        // mock 模式：显式确认接口（PRD §4.8）。
        await request('POST', '/api/v1/orders/' + order.id + '/mock-payment/confirm');
      } else {
        // 真实微信支付：wx.requestPayment（PRD §4.8）。
        await this.wxPay(prepay.jsapi_payload);
      }
      // 不能仅凭 requestPayment 成功显示已支付，必须轮询服务端（PRD §4.8.3）。
      wx.redirectTo({ url: '/pages/order-detail/order-detail?id=' + order.id });
    } catch (err) {
      const e = err as { code?: string; message?: string };
      if (e.code === 'PICKUP_SLOT_FULL') {
        wx.showToast({ title: '时段已满，请重新选择', icon: 'none' });
        this.preview();
      } else if (e.code === 'QUOTE_EXPIRED' || e.code === '410') {
        wx.showToast({ title: '报价过期，重新报价', icon: 'none' });
        this.preview();
      } else {
        wx.showToast({ title: e.message || '下单失败', icon: 'none' });
      }
    }
  },

  wxPay(payload: Record<string, string>): Promise<void> {
    return new Promise((resolve, reject) => {
      wx.requestPayment({
        timeStamp: payload.timeStamp || '',
        nonceStr: payload.nonceStr || '',
        package: payload.package || '',
        signType: (payload.signType as 'RSA') || 'RSA',
        paySign: payload.paySign || '',
        success: () => resolve(),
        fail: () => reject({ code: 'PAY_CANCELLED', message: '支付取消' }),
      });
    });
  },
});
