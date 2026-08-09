// pages/menu/menu.ts — 菜单浏览（PRD §4.2）。
// 启动读取 storefront bootstrap、门店状态、营业状态、当前用餐方式与桌号（PRD §4.1.4）。

import { request } from '../../utils/api';
import { count } from '../../utils/cart';

interface Bootstrap {
  store_id: number;
  store_name: string;
  business_open: boolean;
  announcement: string;
}

interface MenuResp {
  store_id: number;
  categories: { id: number; name: string }[];
  dishes: Dish[];
}

interface Dish {
  id: number;
  category_id: number;
  name: string;
  description: string;
  image_url: string;
  start_price_cents: number;
  manually_sold_out: boolean;
}

Page({
  data: {
    storeName: '',
    businessOpen: true,
    announcement: '',
    scenario: 'PICKUP' as 'DINE_IN' | 'PICKUP',
    tableLabel: '',
    categories: [] as { id: number; name: string }[],
    dishes: [] as Dish[],
    activeCat: 0,
    cartCount: 0,
  },

  onLoad() {
    const app = getApp();
    this.setData({
      scenario: app.globalData.scenario as 'DINE_IN' | 'PICKUP',
      tableLabel: app.globalData.tableLabel,
    });
    this.loadBootstrap();
    this.loadMenu();
  },

  onShow() {
    this.setData({ cartCount: count() });
  },

  loadBootstrap() {
    request<Bootstrap>('GET', '/api/v1/storefront/bootstrap')
      .then((b) => {
        const app = getApp();
        app.globalData.storeId = b.store_id;
        this.setData({
          storeName: b.store_name,
          businessOpen: b.business_open,
          announcement: b.announcement,
        });
      })
      .catch(() => {
        wx.showToast({ title: '门店加载失败', icon: 'none' });
      });
  },

  loadMenu() {
    request<MenuResp>('GET', '/api/v1/menu')
      .then((m) => {
        this.setData({ categories: m.categories, dishes: m.dishes });
      })
      .catch(() => {
        wx.showToast({ title: '菜单加载失败', icon: 'none' });
      });
  },

  selectCat(e: WechatMiniprogram.TouchEvent) {
    this.setData({ activeCat: Number(e.currentTarget.dataset.id) });
  },

  goCart() {
    if (!this.data.businessOpen) {
      wx.showToast({ title: '门店休息中，不可下单', icon: 'none' });
      return;
    }
    wx.navigateTo({ url: '/pages/cart/cart' });
  },
});
