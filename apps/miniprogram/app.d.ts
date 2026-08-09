// 全局 globalData 类型声明（app.ts 的 globalData 形状）。
interface AppGlobalData {
  appid: string;
  storeId: number;
  scenario: 'DINE_IN' | 'PICKUP';
  tableToken: string;
  tableLabel: string;
  token: string;
  customerId: number;
}

// 取代默认 getApp，返回带 globalData 的实例。
declare function getApp(): { globalData: AppGlobalData };
