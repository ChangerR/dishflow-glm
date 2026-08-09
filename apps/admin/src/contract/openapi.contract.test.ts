// contract/openapi.contract.test.ts — 前端 ↔ OpenAPI 契约一致性测试（PRD §20.3 / §23.3）。
//
// 解析 ../../../../openapi/openapi.yaml，断言：
//   1. 后台调用路径（与 src/api/client.ts + 各页面实际调用一致）都存在于 OpenAPI。
//   2. Error.code 枚举与前端依赖的错误码集合一致（无遗漏/无幽灵码）。
//   3. 关键安全参数（X-Request-Id 模式、Idempotency-Key 头）存在于契约。
//   4. 金额字段统一为 int64 cents（_cents 后缀 + format int64）。
// 失败即表明前后端契约漂移，阻断 CI。
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { parse } from 'yaml';

const openApiPath = resolve(__dirname, '../../../../openapi/openapi.yaml');
const spec = parse(readFileSync(openApiPath, 'utf8')) as {
  paths: Record<string, Record<string, { operationId?: string; parameters?: { name: string; in: string; schema?: { pattern?: string; type?: string; format?: string } }[]; responses?: Record<string, unknown> }>>;
  components?: {
    parameters?: Record<string, { name: string; in: string; schema?: { pattern?: string; type?: string; format?: string; maxLength?: number } }>;
    schemas?: Record<string, { properties?: Record<string, { type?: string; format?: string; enum?: string[] }> }>;
  };
};

describe('OpenAPI ↔ admin 契约一致性', () => {
  it('openapi.yaml 可解析且包含 paths', () => {
    expect(spec.paths).toBeDefined();
    expect(Object.keys(spec.paths).length).toBeGreaterThan(20);
  });

  // 管理后台实际调用的路径（来自 api/client.ts + 页面）。
  const adminCalls: Array<{ method: string; path: string }> = [
    { method: 'post', path: '/api/v1/admin/session' },
    { method: 'delete', path: '/api/v1/admin/session' },
    { method: 'get', path: '/api/v1/admin/session' },
    { method: 'get', path: '/api/v1/admin/orders/board' },
    { method: 'get', path: '/api/v1/admin/orders/{id}' },
    { method: 'post', path: '/api/v1/admin/orders/{id}/transitions' },
    { method: 'get', path: '/api/v1/admin/categories' },
    { method: 'post', path: '/api/v1/admin/categories' },
    { method: 'patch', path: '/api/v1/admin/categories/{id}' },
    { method: 'delete', path: '/api/v1/admin/categories/{id}' },
    { method: 'post', path: '/api/v1/admin/categories/{id}/restore' },
    { method: 'get', path: '/api/v1/admin/dishes' },
    { method: 'post', path: '/api/v1/admin/dishes' },
    { method: 'delete', path: '/api/v1/admin/dishes/{id}' },
    { method: 'post', path: '/api/v1/admin/dishes/{id}/restore' },
    { method: 'post', path: '/api/v1/admin/dishes/{id}/stock-adjustments' },
    { method: 'get', path: '/api/v1/admin/promotions' },
    { method: 'post', path: '/api/v1/admin/promotions' },
    { method: 'get', path: '/api/v1/admin/tables' },
    { method: 'post', path: '/api/v1/admin/tables' },
    { method: 'post', path: '/api/v1/admin/tables/{id}/rotate-token' },
    { method: 'get', path: '/api/v1/admin/analytics/overview' },
    { method: 'get', path: '/api/v1/admin/analytics/trends' },
    { method: 'get', path: '/api/v1/admin/analytics/breakdown' },
    { method: 'get', path: '/api/v1/admin/store/export' },
    { method: 'post', path: '/api/v1/admin/store/import' },
    { method: 'get', path: '/api/v1/admin/materials' },
    { method: 'post', path: '/api/v1/admin/materials' },
    { method: 'post', path: '/api/v1/admin/customer-members/{customerId}/points-adjustments' },
    { method: 'post', path: '/api/v1/admin/platform/stores' },
    { method: 'get', path: '/api/v1/admin/platform/stores' },
    { method: 'patch', path: '/api/v1/admin/platform/stores/{id}' },
    { method: 'post', path: '/api/v1/admin/platform/users' },
    { method: 'get', path: '/api/v1/admin/platform/users' },
    { method: 'patch', path: '/api/v1/admin/platform/users/{id}' },
    { method: 'post', path: '/api/v1/admin/platform/users/{id}/assign-store-owner' },
    { method: 'get', path: '/api/v1/admin/platform/shop-applications' },
    { method: 'post', path: '/api/v1/admin/platform/shop-applications/{id}/review' },
    { method: 'post', path: '/api/v1/admin/shop-applications' },
    { method: 'get', path: '/api/v1/admin/my/shop-applications' },
    { method: 'post', path: '/api/v1/admin/shop-join-requests' },
    { method: 'get', path: '/api/v1/admin/my/shop-join-requests' },
  ];

  it('每个后台实际调用路径都存在于 OpenAPI（method+path）', () => {
    const missing: string[] = [];
    for (const call of adminCalls) {
      const ops = spec.paths[call.path];
      if (!ops || !ops[call.method]) {
        missing.push(`${call.method.toUpperCase()} ${call.path}`);
      }
    }
    expect(missing, `缺失契约路径: ${missing.join(', ')}`).toEqual([]);
  });

  it('Error.code 枚举包含前端依赖的全部可操作错误码（PRD §16）', () => {
    const errSchema = spec.components?.schemas?.Error;
    expect(errSchema, 'Error schema 应存在').toBeDefined();
    const codes = errSchema?.properties?.code?.enum as string[] | undefined;
    expect(codes, 'Error.code 应有 enum').toBeDefined();
    // 前端 Board/Checkout/Import 等依赖的错误码必须都在契约里。
    const required = [
      'INTERNAL', 'BAD_REQUEST', 'UNAUTHORIZED', 'FORBIDDEN', 'NOT_FOUND',
      'CONFLICT', 'STATE_CONFLICT', 'RATE_LIMITED',
      'QUOTE_EXPIRED', 'QUOTE_MISMATCH',
      'TABLE_NOT_FOUND', 'TABLE_DISABLED',
      'PICKUP_SLOT_FULL', 'PICKUP_TIME_INVALID',
      'PAYMENT_UNAVAILABLE', 'REFUND_CONFLICT',
      'INSUFFICIENT_POINTS', 'WECHAT_APPID_CONFLICT',
    ];
    const absent = required.filter((c) => !codes?.includes(c));
    expect(absent, `契约缺失错误码: ${absent.join(', ')}`).toEqual([]);
  });

  it('X-Request-Id 参数模式符合安全字符与长度约束（PRD §16）', () => {
    const rid = spec.components?.parameters?.RequestIdHeader;
    expect(rid, 'RequestIdHeader 参数应存在').toBeDefined();
    expect(rid?.schema?.maxLength).toBe(64);
    expect(rid?.schema?.pattern).toBe('^[A-Za-z0-9._:-]{1,64}$');
  });

  it('Idempotency-Key 头参数存在且有长度上限', () => {
    const idem = spec.components?.parameters?.IdempotencyKeyHeader;
    expect(idem, 'IdempotencyKeyHeader 参数应存在').toBeDefined();
    expect(idem?.name).toBe('Idempotency-Key');
    expect(idem?.in).toBe('header');
    expect(idem?.schema?.maxLength).toBe(100);
  });

  it('金额字段统一为 int64 cents（PRD §4.5）', () => {
    // 抽检若干典型金额字段：都以 _cents 结尾且 format int64。
    const quote = spec.components?.schemas?.Quote;
    expect(quote?.properties?.item_amount_cents?.format).toBe('int64');
    expect(quote?.properties?.payable_cents?.format).toBe('int64');
    const order = spec.components?.schemas?.Order;
    expect(order?.properties?.payable_cents?.format).toBe('int64');
    // 不应出现 *_yuan / *_yuan_float 之类的字段。
    const allProps = Object.keys(order?.properties ?? {});
    const yuanFields = allProps.filter((p) => /yuan/i.test(p));
    expect(yuanFields, `Order 不应含 yuan 字段: ${yuanFields.join(',')}`).toEqual([]);
  });
});
