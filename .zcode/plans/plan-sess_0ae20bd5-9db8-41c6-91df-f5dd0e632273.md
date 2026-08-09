# DishFlow 全系统分阶段实现计划（起点：后端 API + 数据库）

## 现状结论

- `/home/changer/project/zshop` 当前**只有 PRD 文档，无任何源代码**（无 `apps/`、无 `go.mod`、无 `package.json`、无 `openapi/`、无迁移、无测试）。
- 目标系统 = 多门店餐饮点餐/履约平台：Go 模块化单体（API + Worker）、MySQL 8 + Redis、OpenAPI 3 契约、React+TS+Vite 管理后台、微信原生 TS 小程序。
- 你选择**整套分阶段、后端先行**。本计划聚焦把系统拆成可独立交付的阶段，并给出**第一阶段（后端 + 数据库骨架）的落地步骤**；后续阶段附在该计划末尾作为路线图，逐阶段再细化。

---

## 总体技术决策（贯穿全阶段）

- **目录结构（Go monorepo，前端稍后并入）**：
  ```
  zshop/
    prd-current/                 # 已存在，不动
    openapi/openapi.yaml         # 契约，Redocly lint
    migrations/                  # 纯 SQL 迁移，按编号递增
    internal/
      config/                    # 环境配置（SHOP_*）
      server/                    # HTTP server、路由、中间件
      authn/                     # 顾客会话(Bearer)、管理会话(Cookie)、限速、密码哈希
      platform/                  # 平台/门店/账号/开店申请
      storefront/                # bootstrap、menu、store、policies、media proxy
      pricing/                   # 算价引擎（领域核心，单元测试重点）
      orders/                    # 订单创建、状态机、工作台、历史、导出、取消
      inventory/                 # 每日库存、预占、流水
      pickup/                    # 预约时段生成 + 容量原子占位
      payments/                  # 微信支付 v3、mock、回调验签、对账
      refunds/                   # 全额退款、回调、扣积分幂等
      coupons/                   # 模板、领取、核销、按人群发放
      promotions/                # 满减
      members/                   # 顾客会员、积分流水、入会、手机号加密
      tables/                    # 桌台、不透明 token、小程序码
      materials/                 # 物料、采购清单、状态机
      printing/                  # 商鹏配置、打印机、打印任务、58mm 渲染
      export/                    # 门店数据导出/导入
      audit/                     # 审计日志
      reliability/               # idempotency、outbox、webhook 去重
      worker/                    # 心跳、释放过期、对账、outbox、打印、回收站窗口
      storage/                   # MySQL 仓储、Redis 报价、COS、AES-GCM 凭证
    cmd/
      serve/                     # API 进程
      worker/                    # Worker 进程
      migrate/                   # 迁移
      healthcheck/               # 存活/就绪
  ```
- **架构约束（来自 PRD §1.5/§6.2/§17）**：所有可信状态以服务端为准；下单/库存预占/快照/预约容量/outbox **同一事务**；支付成功只由回调或查单确认；状态机带 `expected_version` 乐观锁；金额一律 `int64` 分；多租户按 `store_id` + 后台 `X-Store-Id` / 小程序 `X-Wechat-Appid` 严格隔离；幂等键贯穿关键写。

---

## 路线图（共 8 阶段，每阶段独立可交付 + 可测试）

| 阶段 | 内容 | 主要交付 |
| --- | --- | --- |
| **P0** | 工程骨架 + 数据库 schema | monorepo、工具链、CI、初始迁移、OpenAPI 骨架、serve/migrate/healthcheck 能跑 |
| **P1** | 平台与门店入驻 | 平台门店/账号/店主、开店申请、加入申请、审计 |
| **P2** | 管理会话 + 菜单/分类/SKU/选项 | 登录、Cookie 会话、X-Store-Id 鉴权、分类/菜品（含 30 天回收站）、库存调整 |
| **P3** | 顾客侧：门店定位 + 菜单 + 算价 | storefront/bootstrap、menu、桌码解析、算价引擎 + Redis 报价、pickup-slots |
| **P4** | 订单创建 + 库存 + 预约容量 | POST /orders 事务、库存预占/释放、预约原子占位、取餐号、订单查询 |
| **P5** | 支付 + 退款 + Worker | 微信支付 v3、mock、回调验签、退款、对账 Worker、过期释放、outbox |
| **P6** | 优惠/会员/积分 + 桌台 + 物料 + 云打印 | 满减、券、入会手机号加密、积分流水、桌台小程序码、采购清单、商鹏打印 |
| **P7** | 分析、导入导出、管理后台、小程序 | analytics、CSV 导出、门店备份/恢复、React 后台、微信小程序 |

> 下面给出**本计划正式执行的 P0（骨架+数据库）**的详细步骤。P1–P7 作为路线图保留，每完成一阶段后再滚动细化下一阶段的实施计划（仍走计划→批准→实现循环），避免一次性给出无法落地的过细清单。

---

## 本次执行阶段：P0 — 工程骨架 + 数据库 Schema

### P0 目标
搭好可运行、可测试的 Go 单体骨架与 MySQL schema，让 `serve`/`migrate`/`healthcheck` 跑通，OpenAPI 骨架通过 Redocly lint，为后续所有业务模块提供地基。

### P0 步骤

**1. 校验工具链与环境**
- 检查本机 `go`(≥1.22)、`node`/`pnpm`、`mysql`/`redis`、`redocly`、`git` 可用性；缺则记录并优先用 Docker（`docker compose` 起 MySQL8+Redis）。

**2. 仓库初始化与工具链**
- `go mod init github.com/dishflow/zshop`（或你确认的 module 名）。
- 顶层加 `Makefile`（`make migrate / serve / worker / test / lint / openapi-lint`）、`.editorconfig`、`.gitignore`、`AGENTS.md`（约定路径与命令）。
- 选定 Web 框架与 DB 层（建议：路由 `chi`/`gin`（二选一）、`database/sql` + `sqlx`、Redis `redis/go-redis`、配置 `caarlos0/env` 或 `viper`）。

**3. 配置与进程骨架**
- `internal/config`：`SHOP_DATABASE_DSN`、`SHOP_REDIS_ADDR`、`SHOP_SESSION_*`、`SHOP_QUOTE_*`、`SHOP_DEV_MODE`、`SHOP_TRUSTED_PROXIES`、`SHOP_CREDENTIAL_KEY` 等；空值/过短密钥 fail closed。
- `cmd/serve`：HTTP server + `/health/live`、`/health/ready`(检查 MySQL+Redis)。
- `cmd/worker`：占位（P5 填业务）。
- `cmd/migrate`：读取 `migrations/*.sql` 顺序执行。
- `cmd/healthcheck`：CLI 探活。

**4. HTTP 基础设施**
- 统一 `/api/v1` 前缀、JSON snake_case、错误体 `{code,message,request_id,details}`、成功体或 `{items,next_cursor,total}`。
- 中间件：request_id（安全字符+限长，缺则服务端生成）、panic recover、安全响应头、日志（禁止记 openid/手机号/token/密钥）、可信代理 IP。
- `Idempotency-Key` 中间件骨架（`idempotency_keys` 表，P0 建表 + 中间件接入，业务在后续阶段用）。

**5. MySQL 初始迁移（覆盖 PRD §17 全部实体，但 P0 只建结构不写业务）**
按域分文件落 `migrations/0001_init_*.sql`，包含全部表与不变量约束：
- 身份：`customers`、`customer_sessions`、`admin_users`、`admin_sessions`、`shop_members`
- 门店：`stores`(含预约配置字段 §5.1)、`dining_tables`、开店/加入申请、`miniprogram_config`
- 菜单：`categories`、`products`、`skus`、`option_groups`、`option_items`（含软删除/回收站列与 batch_id）
- 营销/会员：`promotions`、`coupon_templates`、`customer_coupons`、`customer_memberships`、`member_points_ledger`
- 交易：`orders`(含 `scheduled_for`/`pickup_business_date`/`pickup_capacity_released_at`/`version`/`pickup_no`)、`order_items`(快照)、`order_events`、`payments`、`refunds`
- 库存预约：`daily_inventory`、`inventory_reservations`、`inventory_movements`、`pickup_slot_capacity`
- 采购打印：`materials`、`purchase_lists`、`purchase_list_items`(快照)、`purchase_list_events`、`cloud_printers`、`cloud_print_jobs`
- 可靠性：`idempotency_keys`、`webhook_events`、`outbox`、`audit_logs`
- 全局唯一：`stores` 的小程序 AppID 唯一索引；门店范围内唯一索引（桌号、物料名、会员号、手机号 hash、取餐号+业务日）；不变量 CHECK/外键（`available_qty >= reserved_qty + sold_qty` 等，MySQL 用应用层 + 触发器/生成列谨慎处理）。

**6. OpenAPI 骨架**
- `openapi/openapi.yaml`：servers、`/health/*`、错误 schema、分页 schema、`request_id`；预留各业务路径占位（P3+ 逐步补全 schema）。
- Redocly 配置 + Makefile 目标 + CI 校验。

**7. 测试与 CI 骨架**
- `internal` 单元测试占位（如 request_id、错误体、配置校验、迁移可执行性）。
- GitHub Actions（或按你环境）：`go vet ./...`、`go test ./... -race`、`make openapi-lint`。

**8. 文档**
- 顶层 `README.md`：架构、命令、本地起 MySQL/Redis、运行迁移、`serve`。
- `AGENTS.md`：阶段约定与 PRD 解释优先级。

### P0 验收
- `go build ./...`、`go vet ./...`、`go test ./... -race` 全绿。
- `docker compose up`（MySQL+Redis）后 `make migrate && make serve` 启动；`/health/ready` 200。
- `make openapi-lint` 通过。
- 迁移覆盖 §17 全部表与不变量约束；提供"幂等可重复 up"与 down 回滚。

---

## 后续阶段（路线图，每阶段执行前再出细计划）

- **P1 平台与入驻**：平台门店 CRUD、后台账号、`assign-store-owner`(唯一店主)、开店申请审批(事务建店+设店主)、加入申请审批、平台审计日志、`admin/session` 骨架前置。
- **P2 管理会话 + 菜单/库存**：账号密码登录、bcrypt、IP+账号限速、Secure/HttpOnly/SameSite Cookie、空闲8h/绝对7d/30min 续期、`X-Store-Id` 成员关系校验、分类/菜品(含 30 天回收站恢复批)、SKU/选项、每日库存调整(幂等+流水)。
- **P3 顾客侧 + 算价**：`storefront/bootstrap`、`menu`、桌码 token 解析、`pickup-slots`、`pricing/preview` 算价引擎(行单价/包装费/满减 vs 券择优/0 元订单)、Redis 十分钟报价 + `quote_token` 绑定。
- **P4 订单创建**：`POST /orders`(quote 校验 + 购物车一致性 + 事务预占库存 + 预约行锁占位 + 快照 + outbox + 取餐号)、订单列表/详情、过期释放(P5 Worker 调用)。
- **P5 支付/退款/Worker**：微信支付 v3 预支付/回调验签(AES-GCM)/公钥与证书双模、mock 模式(确认接口)、全额退款意图+回调、Worker(心跳/释放/对账/outbox/打印/回收站窗口)、扣积分幂等。
- **P6 营销/会员/桌台/物料/打印**：满减、券(领取幂等/按人群发放/永久删除安全条件)、入会(手机号加密+hash+后四位)、积分流水与兑券事务、桌台(高熵 token/换码/官方小程序码/COS)、物料与采购清单状态机、商鹏云打印(58mm 小票/任务状态/去重)。
- **P7 分析/导入导出/前端**：analytics(趋势/分布/客户洞察)、CSV 导出、门店备份导入(replace 回收站)、React 后台(全角色路由)、微信小程序(全流程)、契约测试对齐。

### 执行说明
- 每完成一阶段（从 P1 起）我会先给该阶段的细计划并经你确认再实现，控制单次改动范围与风险。
- 不实现、不引用 `apps/ios` 任何内容（PRD §0.2）。
- 遵循 PRD 解释优先级（§0.1）：业务规则 > OpenAPI > 迁移约束 > UI 参考；UI 缺失不省服务端校验。

如果认可，我将按 **P0** 开始实现（先校验工具链，再落 monorepo 骨架、迁移与 OpenAPI）。P0 完成后回到计划循环确定 P1 细节。