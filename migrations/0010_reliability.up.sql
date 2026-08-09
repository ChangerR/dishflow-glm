-- 0010_reliability.up.sql
-- 可靠性域：幂等键、Webhook 去重、Outbox、审计日志。
-- PRD §16（Idempotency-Key）、§15（outbox 分发）、§18（审计）。

-- 幂等键：同主体同 key 同请求体返回已保存结果；不同请求体冲突（PRD §16）。
-- subject 区分调用方/资源，如 "customer:123:orders"。
CREATE TABLE idempotency_keys (
  id              BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  idem_key        VARCHAR(100) NOT NULL,
  subject         VARCHAR(100) NOT NULL,
  -- 请求体哈希，用于冲突检测
  request_hash    CHAR(64) NOT NULL DEFAULT '',
  status_code     INT NOT NULL DEFAULT 0,
  response_body   MEDIUMTEXT NULL,
  created_at      DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  UNIQUE KEY uq_idem_subject_key (subject, idem_key),
  KEY idx_idem_created (created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Webhook 事件去重：provider event ID + 业务状态双重幂等（PRD §14.2/§14.3）。
CREATE TABLE webhook_events (
  id              BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  store_id        BIGINT UNSIGNED NOT NULL,
  -- 微信支付/退款回调的 event id
  provider        VARCHAR(24) NOT NULL,
  provider_event_id VARCHAR(64) NOT NULL,
  -- 粗粒度资源类型与 ID：payment / refund / order
  resource_type   VARCHAR(24) NOT NULL,
  resource_id     VARCHAR(64) NOT NULL DEFAULT '',
  -- 处理结果：PROCESSED / IGNORED / ERROR
  result          VARCHAR(16) NOT NULL DEFAULT 'PROCESSED',
  processed_at    DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  -- 同 provider + event id 全局唯一（幂等）
  UNIQUE KEY uq_we_provider_event (provider, provider_event_id),
  KEY idx_we_resource (store_id, resource_type, resource_id),
  CONSTRAINT fk_we_store FOREIGN KEY (store_id) REFERENCES stores(id) ON DELETE RESTRICT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Outbox：支付成功等事件投递（PRD §15 分发，§14.2 同事务写）。
CREATE TABLE outbox (
  id              BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  store_id        BIGINT UNSIGNED NOT NULL,
  event_type      VARCHAR(40) NOT NULL,    -- e.g. order.paid
  aggregate_type  VARCHAR(32) NOT NULL,    -- order / refund / print
  aggregate_id    BIGINT NOT NULL,
  payload         JSON NOT NULL,
  -- PENDING / SENT / FAILED
  status          VARCHAR(16) NOT NULL DEFAULT 'PENDING',
  attempts        INT NOT NULL DEFAULT 0,
  last_error      VARCHAR(255) NOT NULL DEFAULT '',
  next_attempt_at DATETIME(3) NULL,
  created_at      DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  sent_at         DATETIME(3) NULL,
  PRIMARY KEY (id),
  KEY idx_outbox_status_due (status, next_attempt_at),
  KEY idx_outbox_aggregate (aggregate_type, aggregate_id),
  CONSTRAINT chk_outbox_status CHECK (status IN ('PENDING','SENT','FAILED'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- 审计日志（关键写入记录操作者/动作/资源/摘要/request_id/时间，PRD §18）。
-- 摘要不得原样倾倒含密钥的 JSON。
CREATE TABLE audit_logs (
  id              BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  -- 门店级审计（store_id 非空）/ 平台级审计（store_id 空，platform_scope=1）
  store_id        BIGINT UNSIGNED NULL,
  platform_scope  TINYINT(1) NOT NULL DEFAULT 0,
  actor_type      VARCHAR(16) NOT NULL,    -- ADMIN / CUSTOMER / SYSTEM / WORKER
  actor_admin_user_id BIGINT UNSIGNED NULL,
  action          VARCHAR(64) NOT NULL,
  resource_type   VARCHAR(32) NOT NULL,
  resource_id     VARCHAR(64) NOT NULL DEFAULT '',
  summary         VARCHAR(500) NOT NULL DEFAULT '',
  request_id      VARCHAR(64) NOT NULL DEFAULT '',
  created_at      DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  KEY idx_audit_store_time (store_id, created_at),
  KEY idx_audit_resource (resource_type, resource_id),
  KEY idx_audit_actor (actor_admin_user_id, created_at),
  CONSTRAINT chk_audit_actor CHECK (actor_type IN ('ADMIN','CUSTOMER','SYSTEM','WORKER'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
