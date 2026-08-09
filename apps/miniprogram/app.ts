// app.ts — DishFlow 小程序入口。
// 门店定位：通过小程序 AppID 解析门店（X-Wechat-Appid），不接受客户端直传 store_id（PRD §2.2）。
// 场景：普通入口=自取；扫码桌台=堂食并锁定桌号（PRD §4.1）。

interface GlobalData {
  appid: string;
  storeId: number;
  scenario: 'DINE_IN' | 'PICKUP';
  tableToken: string;
  tableLabel: string;
  token: string; // 顾客 Bearer
  customerId: number;
}

App({
  globalData: {
    appid: '',
    storeId: 0,
    scenario: 'PICKUP',
    tableToken: '',
    tableLabel: '',
    token: '',
    customerId: 0,
  } as GlobalData,
  onLaunch() {
    // 小程序自身的 AppID（用于 X-Wechat-Appid）。
    try {
      const accountInfo = wx.getAccountInfoSync();
      this.globalData.appid = accountInfo.miniProgram.appId;
    } catch (e) {
      this.globalData.appid = '';
    }
    // 演示模式：如果小程序 AppID 未在服务端注册（touristappid 等），
    // 使用演示门店的 AppID，使菜单/下单可联调。
    if (!this.globalData.appid || this.globalData.appid === 'touristappid') {
      this.globalData.appid = 'wxdemoappid0001';
    }
    // 清除过期桌台上下文（PRD §4.1.1）。
    const t = wx.getStorageSync('table_token');
    const texp = wx.getStorageSync('table_expires');
    if (t && texp && Date.now() > texp) {
      wx.removeStorageSync('table_token');
      wx.removeStorageSync('table_label');
    }
    this.globalData.token = wx.getStorageSync('token') || '';
    this.globalData.customerId = wx.getStorageSync('customer_id') || 0;
  },
});
