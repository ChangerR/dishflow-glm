// pages/me/me.ts — 我的（会员号/积分/订单/券入口，PRD §4.13）。
import { request, ensureSession } from '../../utils/api';

Page({
  data: { memberNo: '', points: 0, isMember: false, customerId: 0 },
  onShow() {
    this.load();
  },
  async load() {
    try {
      await ensureSession();
      const me = await request<{ customer_id: number; member_no?: string; points_balance?: number; membership_status?: string }>('GET', '/api/v1/me');
      this.setData({
        customerId: me.customer_id,
        memberNo: me.member_no || '',
        points: me.points_balance || 0,
        isMember: !!me.member_no,
      });
    } catch (e) {
      // 未登录时静默。
    }
  },
});
