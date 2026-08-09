# DishFlow

餐饮门店点餐与履约系统：Go 模块化单体（API + Worker）+ MySQL 8 + Redis + React/TS/Vite 管理后台 + 微信原生 TS 小程序 + OpenAPI 契约。

> 当前进度：**后端 API + Worker + 数据库 + OpenAPI 契约已完整实现（P0–P7 后端）**，React 管理后台与微信小程序前端待补全。
> 需求基线：[`prd-current/complete-requirements-no-ios.md`](./prd-current/complete-requirements-no-ios.md)。不含 iOS。

## 已交付（后端，全部真实持久化、可编译、可测试）

- **工程骨架**：4 个可执行入口（serve/worker/migrate/healthcheck）、Makefile、docker-compose、CI、OpenAPI + Redocly lint。
- **数据库**：10 个迁移、41 张表，覆盖 PRD §17 全部实体与不变量（CHECK 约束实测生效）。
- **平台与入驻**：门店/账号/成员、**唯一店主事务**、开店/加入申请审批（事务建店+设店主）、平台审计。
- **管理会话**：bcrypt、IP+账号限速、Secure/HttpOnly/SameSite Cookie、8h/7d/30min 续期、`X-Store-Id` 成员关系校验、角色层级。
- **菜单与库存**：分类/菜品/SKU/选项（事务）、**30 天回收站（同批次软删/恢复）**、每日库存调整（行锁+不变量+幂等+流水）。
- **顾客侧 + 算价**：微信会话、storefront/bootstrap、公开菜单、桌码解析、pickup-slots、**算价引擎（满减 vs 券择优不叠加）**、Redis 十分钟报价 quote_token（HMAC 签名）。
- **订单**：**单事务创建**（库存预占 + 预约原子占位 + 快照 + outbox + 取餐号）、状态机（乐观锁）、工作台、查询。
- **支付/退款/Worker**：微信支付 v3 Provider 接口 + mock、回调（路径门店一致性 + webhook 去重）、退款意图（一单一意图）、Worker（心跳/outbox/过期释放/对账）。
- **营销/会员**：满减、券模板（领取幂等/核销）、入会**手机号 AES-GCM 加密 + hash + 后四位**、积分流水（幂等）、人工调整。
- **桌台**：高熵 token、换码原子替换（旧码失效）。
- **物料/采购/打印**：物料目录、采购清单状态机（DRAFT→SUBMITTED→PRINTED→COMPLETED/VOID，乐观锁）、商鹏打印配置 + 58mm 小票渲染 + mock。
- **分析/导入导出**：概览/趋势/分布、门店备份导出 + replace 导入（事务、AppID 冲突校验）。

## 测试

- 单元测试：config、httpx、security、pickup、pricing（算价 17 例）。
- 集成测试（真实 MySQL）：platform、menu、orders、integrationtest（admin HTTP 全流程 + 支付 mock→outbox→订单 PAID + 退款 + 券幂等 + 入会加密 + 积分）。
- `go vet ./...`、`go test ./... -race`、`make openapi-lint` 全绿。


## 目录结构

```
prd-current/      PRD 文档（只读基线）
openapi/          OpenAPI 契约（Redocly 校验）
migrations/       纯 SQL 迁移（NNNN_name.up.sql / .down.sql）
internal/
  config/         SHOP_* 环境配置（fail closed）
  httpx/          错误包络、request-id、分页、中间件
  reliability/    幂等键存储（idempotency_keys）
  server/         路由、中间件装配、健康检查
  storage/        MySQL/Redis 连接助手
cmd/
  serve/          API 进程
  worker/         Worker 进程（P0 仅心跳）
  migrate/        迁移：up / down N / redo / status
  healthcheck/    CLI 探活
.github/workflows/ci.yml
```

## 快速开始

### 1. 起依赖（MySQL + Redis）

```bash
make up          # docker compose up -d（mysql:8.0 + redis:7）
```

> 若本地已有 MySQL/Redis，可直接使用；DSN/地址见 `.env.example`。

### 2. 配置

```bash
cp .env.example .env   # 按需修改；开发可设 SHOP_DEV_MODE=true 跳过密钥强度校验
```

关键变量：
- `SHOP_DATABASE_DSN` — MySQL 8 DSN（务必带 `parseTime=true&loc=UTC&charset=utf8mb4`）
- `SHOP_REDIS_ADDR` — `redis://host:port/db`
- `SHOP_DEV_MODE=true` — 开发模式：服务端可使用 mock 支付/打印，并放行短密钥；**生产必须 false**
- 生产模式（`SHOP_DEV_MODE=false`）要求 `SHOP_SESSION_SIGNING_KEY`、`SHOP_QUOTE_SIGNING_KEY` ≥32 字符、`SHOP_CREDENTIAL_KEY` 非空（fail closed）

### 3. 迁移 + 启动

```bash
make migrate     # 应用全部迁移（41 张业务表 + schema_migrations）
make serve       # 启动 API（默认 127.0.0.1:8080）
make health      # 另一终端：就绪探活
```

健康检查（PRD §19）：
- `GET /health/live` — 仅证明进程存活
- `GET /health/ready` — 检查 MySQL + Redis，任一不可用返回 503

### 4. 验证

```bash
curl -i http://127.0.0.1:8080/health/ready
```

## 常用命令

| 命令 | 说明 |
| --- | --- |
| `make up` / `make down` | 起停本地 MySQL + Redis |
| `make migrate` | 迁移到最新 |
| `make migrate-down` | 回滚最近一次迁移 |
| `make migrate-status` | 显示迁移状态 |
| `make serve` | 启动 API |
| `make worker` | 启动 Worker |
| `make test` | `go test ./... -race` |
| `make vet` | `go vet ./...` |
| `make openapi-lint` | Redocly 校验 openapi.yaml |
| `make ci` | vet + test + openapi lint（CI 等价） |
| `make build` | 编译全部 cmd 到 ./bin |

## 架构要点（贯穿后续阶段）

- **可信状态以服务端为准**：顾客看到的价格/库存/优惠/预约/订单状态都来自服务端，客户端只展示。
- **事务一致性**：下单、库存预占、订单快照、预约容量占用、outbox 事件同一事务提交（PRD §1.5）。
- **支付只认回调/查单**：支付成功仅由微信验签回调或服务端主动查单确认（PRD §4.8）。
- **乐观锁状态机**：所有状态推进带 `expected_version`；禁止跳级/回退（PRD §6.2）。
- **多租户隔离**：管理端 `X-Store-Id` + 成员关系校验；小程序 `X-Wechat-Appid` 解析门店，不接受客户端直传 `store_id`（PRD §2.2）。
- **幂等**：关键写接口 `Idempotency-Key`；回调/补偿重复执行结果不变（PRD §16/§14.2）。
- **金额 `int64` 分；时间 UTC 存储、业务日按门店时区计算**（PRD §4.5/§19）。

## 迁移与不变量

`migrations/0001..0010` 覆盖 PRD §17.1 全部实体（41 张表）与 §17.2 关键不变量，部分由 MySQL `CHECK` 约束在数据库层强制，例如：

- `available_qty >= reserved_qty + sold_qty`（`daily_inventory.chk_di_inv`）
- 堂食不允许带预约时间（`orders.chk_order_dine_no_schedule`）
- 积分余额非负（`customer_memberships.chk_cm_points`）
- 门店成员角色合法、取餐号门店业务日唯一等

> 注：部分不变量（如“一单最多一个有效退款意图”）需应用层 + 部分状态枚举保证，数据库唯一索引仅作近似。

## 测试与质量门槛（PRD §20）

- 后端：`go vet ./...`、`go test ./... -race`。
- 契约：`make openapi-lint`（Redocly）。
- CI：[`.github/workflows/ci.yml`](./.github/workflows/ci.yml) 在 MySQL+Redis service 上跑 vet/test/build/迁移 up-down-up 循环 + OpenAPI lint。

前端（`pnpm`）测试、构建门槛在 P7 引入。

## 排除范围（PRD §0.2）

不实现、不修改、不引用 iOS；不做第三方外卖配送、桌位预订、拼团、秒杀、储值、会员等级、积分抵现、部分退款、订单拆单/并单；不把 mock 支付/mock 打印当作生产替代。
