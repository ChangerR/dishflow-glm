// pages/coupons/coupons.ts — 优惠券中心（PRD §4.11）。
import { request, ensureSession } from '../../utils/api';

interface Coupon {
  id: number;
  name: string;
  status: string;
  min_spend_cents: number;
  discount_cents: number;
}

Page({
  data: { mine: [] as Coupon[], offers: [] as Coupon[] },
  async onShow() {
    try {
      await ensureSession();
      const mc = await request<{ items: Coupon[] }>('GET', '/api/v1/coupons');
      this.setData({ mine: mc.items || [] });
      // 公开领取（无需登录也可，但领取需 Bearer）。
      const of = await request<{ items: Coupon[] }>('GET', '/api/v1/coupon-offers');
      this.setData({ offers: of.items || [] });
    } catch (e) {
      wx.showToast({ title: '加载失败', icon: 'none' });
    }
  },
  async claim(e: WechatMiniprogram.TouchEvent) {
    const id = e.currentTarget.dataset.id as number;
    try {
      await request('POST', '/api/v1/coupon-offers/' + id + '/claim');
      wx.showToast({ title: '已领取', icon: 'success' });
      this.onShow();
    } catch (err) {
      const er = err as { message?: string };
      wx.showToast({ title: er.message || '领取失败', icon: 'none' });
    }
  },
});
