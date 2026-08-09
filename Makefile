# DishFlow Makefile
# 所有命令也适合无 GNU make 之外的手工执行。

SHELL := /bin/bash

# 工具
GO        := go
GOTEST    := $(GO) test
NPM       := npm
NPX       := npx
DBIN      := ./bin

# 配置（可被环境变量覆盖）
DSN       ?= shop:shop@tcp(127.0.0.1:3306)/dishflow?parseTime=true&loc=UTC&charset=utf8mb4
REDS      ?= redis://127.0.0.1:6379/0
SERVE_ADDR ?= 127.0.0.1:8080

.PHONY: help
help: ## 显示帮助
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'

.PHONY: deps
deps: ## 下载 Go 依赖
	$(GO) mod download

.PHONY: tidy
tidy: ## 整理 go.mod
	$(GO) mod tidy

.PHONY: build
build: ## 编译全部 cmd 到 ./bin
	@mkdir -p $(DBIN)
	$(GO) build -o $(DBIN)/serve ./cmd/serve
	$(GO) build -o $(DBIN)/worker ./cmd/worker
	$(GO) build -o $(DBIN)/migrate ./cmd/migrate
	$(GO) build -o $(DBIN)/healthcheck ./cmd/healthcheck

.PHONY: serve
serve: ## 启动 API 进程
	SHOP_DATABASE_DSN="$(DSN)" SHOP_REDIS_ADDR="$(REDS)" SHOP_SERVE_ADDR="$(SERVE_ADDR)" \
		$(GO) run ./cmd/serve

.PHONY: worker
worker: ## 启动 Worker 进程
	SHOP_DATABASE_DSN="$(DSN)" SHOP_REDIS_ADDR="$(REDS)" \
		$(GO) run ./cmd/worker

.PHONY: health
health: ## CLI 健康检查
	SHOP_SERVE_ADDR="$(SERVE_ADDR)" $(GO) run ./cmd/healthcheck

.PHONY: migrate
migrate: ## 执行迁移到最新
	SHOP_DATABASE_DSN="$(DSN)" $(GO) run ./cmd/migrate up

.PHONY: migrate-down
migrate-down: ## 回滚最近一次迁移
	SHOP_DATABASE_DSN="$(DSN)" $(GO) run ./cmd/migrate down 1

.PHONY: migrate-status
migrate-status: ## 显示迁移状态
	SHOP_DATABASE_DSN="$(DSN)" $(GO) run ./cmd/migrate status

.PHONY: test
test: ## 运行全部测试（含竞态检测）
	$(GOTEST) ./... -race

.PHONY: vet
vet: ## go vet
	$(GO) vet ./...

.PHONY: fmt
fmt: ## 格式化
	$(GO) fmt ./...

.PHONY: openapi-lint
openapi-lint: ## Redocly 校验 openapi.yaml
	$(NPX) --yes @redocly/cli@latest lint openapi/openapi.yaml

.PHONY: lint
lint: vet openapi-lint ## 静态检查 + 契约校验

.PHONY: up
up: ## 本地启动 MySQL + Redis（docker compose）
	docker compose up -d

.PHONY: down
down: ## 停止并清理本地 MySQL + Redis
	docker compose down -v

.PHONY: ci
ci: vet test openapi-lint ## CI 等价：vet + test + openapi lint

.PHONY: fe-check
fe-check: ## 前端 TypeScript 检查（管理后台 + 小程序）
	$(PNPM) --recursive run check

.PHONY: fe-test
fe-test: ## 前端测试
	$(PNPM) --recursive run test

.PHONY: fe-build
fe-build: ## 前端生产构建
	$(PNPM) --recursive run build
