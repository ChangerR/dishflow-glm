import { defineConfig } from 'vitest/config';

export default defineConfig({
  test: {
    include: ['utils/**/*.test.ts', 'tests/**/*.test.ts'],
    // 小程序 utils 依赖全局 wx；测试通过 setupFiles 注入内存 mock。
    setupFiles: ['./tests/setup.ts'],
  },
});
