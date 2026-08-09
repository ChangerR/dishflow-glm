// tests/setup.ts — 为小程序 utils 提供内存版 wx 全局，使纯逻辑可在 Vitest 中跑。
// 仅模拟 utils/cart.ts、utils/api.ts 用到的 storage/request 部分。

const storage = new Map<string, unknown>();

const wxMock = {
  getStorageSync(key: string): unknown {
    return storage.has(key) ? storage.get(key) : '';
  },
  setStorageSync(key: string, value: unknown): void {
    storage.set(key, value);
  },
  removeStorageSync(key: string): void {
    storage.delete(key);
  },
  clearStorageSync(): void {
    storage.clear();
  },
};

// api.ts 在模块加载时调用 getApp()；提供返回 globalData 的桩。
const appMock = {
  globalData: { appid: 'wxtestappid', storeId: 0, scenario: 'PICKUP', tableToken: '', tableLabel: '', token: '', customerId: 0 },
};

// 暴露到全局，供被测模块使用。
(globalThis as unknown as { wx: typeof wxMock; getApp: () => typeof appMock }).wx = wxMock;
(globalThis as unknown as { getApp: () => typeof appMock }).getApp = () => appMock;

// 工具：测试间重置存储。
export function resetStorage(): void {
  storage.clear();
}

