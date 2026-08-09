// pages/membership/membership.ts — 入会（手机号验证，PRD §4.12）。
// 需勾选协议并通过微信 getPhoneNumber 获得一次性 code。
import { request, ensureSession } from '../../utils/api';

Page({
  data: { phone: '', countryCode: '86', agreed: false, loading: false },

  toggleAgree() {
    this.setData({ agreed: !this.data.agreed });
  },

  // 微信 getPhoneNumber（PRD §4.12）。
  getPhone(e: WechatMiniprogram.CustomEvent) {
    if (!this.data.agreed) {
      wx.showToast({ title: '请先勾选会员协议', icon: 'none' });
      return;
    }
    const code = (e.detail as { code?: string }).code;
    if (!code) {
      wx.showToast({ title: '未授权手机号', icon: 'none' });
      return;
    }
    this.join(code);
  },

  async join(phoneCode: string) {
    this.setData({ loading: true });
    try {
      await ensureSession();
      // 真实手机号 code 由服务端调微信验证（P6 完整）；此处传 code 占位。
      const r = await request<{ member_no: string; is_new: boolean }>('POST', '/api/v1/me/membership', {
        phone: phoneCode,
        country_code: this.data.countryCode,
      });
      wx.showToast({ title: r.is_new ? '入会成功' : '已是会员', icon: 'success' });
      setTimeout(() => wx.navigateBack(), 1200);
    } catch (err) {
      const e = err as { code?: string; message?: string };
      if (e.code === 'CONFLICT') {
        wx.showToast({ title: '手机号已绑定其它会员', icon: 'none' });
      } else {
        wx.showToast({ title: e.message || '入会失败', icon: 'none' });
      }
    } finally {
      this.setData({ loading: false });
    }
  },
});
