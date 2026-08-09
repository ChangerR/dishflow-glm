# AGENTS.md — DishFlow 工作约定

本文件给协作者（含 AI agent）约定路径、命令与解释优先级。

## 1. PRD 解释优先级（不可违反）

按 `prd-current/complete-requirements-no-ios.md` §0.1：

1. 业务规则与验收标准（本文档/PRD）。
2. `openapi/openapi.yaml` 的 HTTP 契约。
3. 数据库迁移表达的数据约束。
4. 当前界面仅作信息架构参考，**不得**以"页面上没显示"为由省略服务端校验。

## 2. 排除范围（PRD §0.2）

- 不实现、不修改、不引用 `apps/ios` 任何内容。
- 不做第三方外卖配送、桌位预订、拼团、秒杀、储值、会员等级、积分抵现、部分退款、拆单/并单。
- 不把 mock 支付/mock 打印当作生产替代。

## 3. 目录约定

```
prd-current/      PRD 文档，只读
openapi/          OpenAPI 契约（Redocly 校验）
migrations/       纯 SQL 迁移，按编号递增（NNNN_name.up.sql / .down.sql）
internal/         业务代码，按域分包（config/server/authn/platform/storefront/...）
cmd/              可执行入口：serve / worker / migrate / healthcheck
```

## 4. 常用命令

```bash
make deps        # 下载 Go 依赖
make migrate     # 执行迁移到最新
make serve       # 启动 API 进程
make worker      # 启动 Worker 进程
make health      # CLI 健康检查
make test        # go test ./... -race
make vet         # go vet ./...
make openapi-lint# Redocly 校验 openapi.yaml
make build       # 编译全部 cmd
```

## 5. 编码与一致性

- 金额一律 `int64` 分；UI 只负责把分格式化为元。
- 时间统一存 UTC 或明确语义，业务日计算用门店 IANA 时区。
- 关键写接口接受 `Idempotency-Key`；状态推进带 `expected_version` 乐观锁。
- 多租户：管理端 `X-Store-Id` + 成员关系校验；小程序 `X-Wechat-Appid` 解析门店，不接受客户端直传 `store_id`。
- 日志禁止记录 openid、完整手机号、Cookie/Bearer、支付密文、私钥、APIv3、AppSecret、打印 KEY。
- 新增/变更接口必须同步 OpenAPI、Go DTO、前端类型与契约测试。

## 6. 阶段路线图

P0 骨架+DB → P1 平台入驻 → P2 管理会话+菜单库存 → P3 顾客侧+算价 → P4 订单 → P5 支付退款Worker → P6 营销会员桌台物料打印 → P7 分析导入导出+前端。
