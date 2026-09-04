# DishFlow PRD 完成度评估（对照代码，非 README）

评估日期：2026-09-04  
基线：`prd-current/complete-requirements-no-ios.md` v1.0  
仓库：`ChangerR/dishflow-glm`（评估时 `main` @ `4e218ef`）

> **结论先说**：不要按 README 宣称的「P0–P7 后端已完整」验收。  
> 更稳妥的总体判断：**产品约 40%**；后端约 **55–60%**；管理后台约 **30–35%**；小程序约 **25–30%**。  
> README 对前端「待补全」已经过时（`apps/admin` / `apps/miniprogram` 都在且可跑），但对后端完整度**明显高估**。

## 1. 总体裁定

| 层 | 裁定 | 约 % | 说明 |
| --- | --- | --- | --- |
| 数据库 / 迁移 | 接近完成 | 85–90 | 10 个迁移、42 张表（含 `schema_migrations`），PRD §17 实体大体落地 |
| Go API / Worker | 部分完成 | 55–60 | 核心域有仓储与部分 HTTP；PRD §16 大量接口未挂路由或仅骨架 |
| OpenAPI | 部分完成 | 50 | 54 条 path，覆盖已实现主路径；缺历史订单、券模板、支付配置、打印设备、退款回调等 |
| 管理后台 React | 骨架可用 | 30–35 | 登录 + 9 个门店页可点；平台/支付/打印/历史订单/券模板等缺失 |
| 微信小程序 | 骨架 | 25–30 | 8 个页面有路由；菜单页不能加购，结算缺选券，我的页导航损坏 |
| 端到端场景 A–I | 未完成 | <20 | 可在本地 mock 演示工作台；堂食扫码、真实支付、打印、权限矩阵未闭环 |

**阶段对照 PRD 路线图**

| 阶段 | 后端 | 前端 |
| --- | --- | --- |
| P0 骨架+DB | 完成 | n/a |
| P1 平台入驻 | 接口大体有 | **无平台 UI** |
| P2 会话+菜单库存 | 会话/分类/创建菜品/库存调整有；**无 PATCH 菜品** | 分类/菜品列表可用，编辑/SKU/选项 UI 无 |
| P3 顾客侧+算价 | 算价引擎与报价较完整 | 小程序浏览骨架，**无加购** |
| P4 订单 | 创建/状态机/工作台有；**无历史检索/CSV** | 工作台四列可用，无详情/搜索/声音 |
| P5 支付退款 Worker | mock 支付+部分 outbox/释放；**硬编码 MockProvider** | 无支付/退款/异常页 |
| P6 营销会员桌台物料打印 | 仓储部分有，HTTP 缺口大 | 满减/桌台/物料/积分调整极简 |
| P7 分析导入导出+前端 | overview/trends/breakdown + 导出导入有 | 分析仅总览卡片；前端远未达 §20.2 |

## 2. README 声明 vs 代码

README 原文：「后端 API + Worker + 数据库 + OpenAPI 契约已完整实现（P0–P7 后端），React 管理后台与微信小程序前端待补全。」

| README 说法 | 代码事实 |
| --- | --- |
| 后端 P0–P7 完整 | **不成立**。`internal/server/server.go` 未挂 PRD §16 中大量路径（历史订单、券模板、门店设置、支付配置、成员、打印机、退款回调、媒体代理等） |
| 微信支付 v3 Provider + mock | 仅 `MockProvider` 被装配；`wechatpay-go` 在 go.mod 为 indirect，无生产 Provider 实现 |
| Worker 心跳/outbox/过期释放/对账 | 心跳与 outbox/释放/回收站窗口有代码；对账 `reconcile` 为 mock 占位 |
| 前端待补全 | **过时**。存在完整 Vite 管理端与原生 TS 小程序，且最近提交改过小程序 API 地址 |
| `go test -race` / vet / openapi-lint 全绿 | 本次复测：**vet 绿、unit+race 绿、integration 绿、openapi 绿（2 条 warning）、前端 check/test 绿** |
| `.env.example` 含引导账号 | **没有**。账号只在 `cmd/seed` 注释里 |

## 3. 分域完成矩阵（PRD）

图例：✅ 已落地且可跑 · ◐ 部分（有表/仓储或薄 UI，缺关键接口或交互） · ❌ 缺失或不可用

### 3.1 顾客小程序（§4）

| 能力 | 后端 | 小程序 | 备注 |
| --- | --- | --- | --- |
| AppID 定位 / bootstrap | ✅ | ◐ | 演示 AppID 回退有；无品牌主题/Logo 应用 |
| 桌码扫码锁定堂食 | ✅ `tables/resolve` | ❌ | `onLaunch` 未解析 `scene` |
| 菜单浏览 | ✅ | ◐ | 分类+列表有；**无搜索、无 SKU/选项弹层、无加购** |
| 本地购物车合并 | n/a | ◐ | `utils/cart.ts` 规则对；菜单不调用 `add()` |
| 服务端算价 + quote | ✅ | ◐ | checkout 会 preview；**不能选券** |
| 预约时段 | ✅ | ◐ | 有尽快/预约切换；无跨日 |
| 下单 + mock 支付 + 轮询 | ✅ | ◐ | 流程写了；无支付结果页、无继续支付 |
| 订单列表/详情/时间线 | ✅ | ◐ | 有筛选；无再来一单、无取消 |
| 领券 | ✅ | ◐ | 中心页有；结算不选券 |
| 入会/积分/兑券 | ◐ | ◐ | 入会+积分流水有；`/me/rewards` **固定空列表**；无 redeem 路由 |
| 门店信息/政策/拨号地图 | ❌ `/store/policies` 未挂 | ❌ | `me.wxml` 用 `bindtap="wx.navigateTo"`，**导航无效** |

### 3.2 管理后台（§5–§14）

| 能力 | 后端 HTTP | Admin UI | 备注 |
| --- | --- | --- | --- |
| 登录/Cookie/限速/401 回登录 | ✅ | ✅ | 已截图 |
| 角色菜单 / 无门店走入驻 | ◐ | ❌ | 侧栏不按 STAFF 隐藏；无「开店与加入」页；无 mock 支付全局警告 |
| 订单工作台四列 + 乐观锁 | ✅ | ◐ | 3s 轮询+推进有；无搜索/详情/声音/超时标红/打印 |
| 历史订单 + CSV | ❌ | ❌ | |
| 退款列表/审核/异常重试 | ◐ | ❌ | handler 有，侧栏无入口 |
| 分类 CRUD + 30 天回收站 | ✅ | ◐ | 列表/新建/删/恢复；无改名/排序/启停 |
| 菜品 SKU/选项/图片/改价 | ◐ | ◐ | 后端无 PATCH；UI 把菜品 id 当 SKU id，且不发 Idempotency-Key |
| 满减 / 券模板 / 人群发放 | ◐ | ◐ | 仅满减 list/create；无券模板 |
| 会员列表/洞察/设置 | ❌ | ◐ | 仅手填 customer id 调积分 |
| 经营分析 趋势/分布/客户 | ◐ | ◐ | 仅 overview 卡片 |
| 桌台 + 官方小程序码 | ◐ | ◐ | 无码图下载；token 明文展示 |
| 门店设置 / 小程序配置 / 安全中心支付 | ❌ | ❌ | |
| 平台门店/账号/开店审批 | ✅ | ❌ | 超管登录后也无菜单 |
| 备份导入导出 | ✅ | ◐ | 有导出/店主导入 |
| 物料 + 采购清单状态机 | ◐ | ◐ | UI 仅物料目录 |
| 云打印配置/打印机/任务 | ◐ | ❌ | 仅 GET print/config |
| 微信支付真实回调/退款回调 | ◐ | n/a | 交易回调有；退款回调未挂 |

### 3.3 可靠性 / 安全（§15–§20）

| 项 | 状态 |
| --- | --- |
| 金额 int64 分、UTC、X-Store-Id / X-Wechat-Appid | ✅ 主路径遵守 |
| Idempotency-Key 中间件 | ◐ 有；库存调整强制要求，前端未带 |
| 生产 fail-closed 密钥 | ✅ config 测试覆盖 |
| 真实微信支付 / 商鹏打印 | ❌ 运行时恒为 mock |
| 媒体代理 `/media/menu` | ❌ |
| 日志脱敏 | 未做专项审计（本次不声称通过） |
| OpenAPI ↔ 实现 ↔ 前端类型 | ◐ 契约测试只断言 admin 已用路径存在于 yaml |

## 4. 前端路由对照

### `apps/admin`（`src/App.tsx`）

已实现路由：`/login` `/board` `/categories` `/dishes` `/promotions` `/tables` `/members` `/materials` `/analytics` `/export`

PRD 有、代码无的页面：平台门店/用户/开店审批、开店与加入、历史订单、退款与异常、门店设置、小程序配置、支付/mock、云打印、券模板、采购清单、客户洞察、审计日志。

### `apps/miniprogram`（`app.json`）

已注册：`menu` `cart` `checkout` `orders` `order-detail` `me` `coupons` `membership`

关键缺口：菜单不能加购；购物车只显示 `SKU {id}`；无 tabBar；API 写死 `http://172.17.186.176:8090`；无取消/再来一单/选券结算。

## 5. 质量检查（本次实测）

环境：本机 apt 安装 MySQL 8.0.46 + Redis 7（**无 Docker**；`docker compose` 不可用）。DSN `shop:shop@tcp(127.0.0.1:3306)/dishflow`。

| 检查 | 结果 |
| --- | --- |
| `go vet ./...` | 通过 |
| `go test ./...` | 通过（仅非 integration 包：config/httpx/payments/pickup/pricing/security） |
| `go test ./... -race` | 通过 |
| `go test -tags=integration ./...` | 通过（integrationtest/menu/orders/platform） |
| `npx @redocly/cli lint openapi/openapi.yaml` | **valid**，2 warnings：`info-license`、`spec-ref-siblings`；`redocly.yaml` 另有过时字段 warning |
| `pnpm --recursive run check` | admin + miniprogram `tsc --noEmit` 通过 |
| `pnpm --recursive run test` | admin 9、小程序 13，全部通过 |
| `pnpm --recursive run build` | 未跑（非阻断本次评估） |

说明：多数域包 **没有** 非 integration 单测。`make test` 默认不加 `-tags=integration`，CI 才跑 integration。

## 6. 本地栈与管理端截图

### 启动方式（本次）

1. MySQL + Redis（apt，非 docker compose）
2. `go run ./cmd/migrate up` — 10 个迁移成功
3. `go run ./cmd/seed` — 打印成功，但见下方种子缺陷
4. `SHOP_DEV_MODE=true SHOP_SERVE_ADDR=127.0.0.1:8090 go run ./cmd/serve` — `/health/ready` 200
5. `apps/admin` `pnpm dev` :5173，代理 `/api` → `:8090`

### 种子缺陷（阻断 `.env.example` 引导登录）

`.env.example` **没有**账号。`cmd/seed` 声称：

- 超管 `demo / demo123456`（10 字符）
- 店长 `manager / manager123456`（13）
- 店员 `staff / staff123456`（11）

`security.HashPassword` 要求 **12–72**。seed **忽略哈希错误**，导致 `demo`、`staff` 的 `password_hash` 为空。实测：

- `POST /api/v1/admin/session` `demo/demo123456` → **401**
- `manager/manager123456` → **200**，`role=OWNER`，`active_store_id=1`

另外：seed 的演示订单 `INSERT IGNORE` 在本次未写入（顾客/约束问题）；评估时用 SQL 补了 4 张订单以便工作台有数据。桌码 `HashTokenStr` 为 64 hex，列是 `CHAR(32)`，桌台 seed 可能失败。

**截图登录请用 `manager` / `manager123456`，并设 `SHOP_DEV_MODE=true`**（否则 Cookie `Secure` 在 HTTP 下不可用）。Vite 代理端口是 **8090**，不是 Makefile 默认的 8080。

### 截图产物

已在本机打开并登录管理端，截取：

| 页面 | 产物路径 |
| --- | --- |
| 登录 | `/opt/cursor/artifacts/admin_login.webp` |
| 订单工作台 | `/opt/cursor/artifacts/admin_board.webp` |
| 分类 | `/opt/cursor/artifacts/admin_categories.webp` |
| 菜品/库存 | `/opt/cursor/artifacts/admin_dishes.webp` |
| 经营分析 | `/opt/cursor/artifacts/admin_analytics.webp` |

工作台可见四列各 1 单；分类有「招牌主食」「饮品」；菜品 4 道；分析支付金额 111.50 / 4 单。侧栏同时暴露了满减、桌台、会员、物料、备份等入口（未全部截图，页面存在但能力偏薄）。

## 7. 接下来最该补的 5 个缺口

1. **把 PRD §16 未挂路由的管理接口补齐并写进 OpenAPI**  
   优先：历史订单+导出、菜品 PATCH、券模板、门店/小程序/支付配置、成员与加入审批、采购清单读写、打印机、退款回调、媒体代理。仓储很多已经在 `internal/*`，缺口主要在 `server.go` + DTO。

2. **管理端按角色补齐信息架构**  
   平台超管页、开店与加入、订单详情/历史/退款异常、门店与安全中心、打印。工作台补搜索、详情、乐观锁冲突提示、STAFF 菜单裁剪。

3. **小程序把堂食/自取主路径做完**  
   菜单 SKU+选项加购、失效行清理、结算选券、取消/继续支付、扫码 `scene`、去掉写死局域网 IP。先修 `me.wxml` 无效 `bindtap`。

4. **种子与本地开发体验**  
   密码满足 12 位；检查 `INSERT` 错误；桌码长度与 `CHAR(32)` 对齐；README/`.env.example` 写明 `manager` 账号、`SHOP_DEV_MODE`、Vite 代理 8090。

5. **支付/打印生产路径 fail-closed**  
   实现并装配真实微信 Provider（不要只留接口注释）；退款回调；打印派发/轮询。在此之前不得把 mock 写成「P5/P6 完成」。

## 8. 明确不声称完成的事项

- PRD §21 场景 A–I 端到端验收  
- 生产微信支付 / 商鹏云打印  
- 管理端移动抽屉与完整权限壳层  
- iOS（排除范围，仓库亦无 `apps/ios`）
