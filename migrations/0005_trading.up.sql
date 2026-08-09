-- 0005_trading.up.sql
-- 交易域：订单、订单项（快照）、订单事件、支付、退款。
-- PRD §4.7/§4.8/§6.2 状态机；§17.2 不变量。

-- 订单。
CREATE TABLE orders (
  id                BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  store_id          BIGINT UNSIGNED NOT NULL,
  customer_id       BIGINT UNSIGNED NOT NULL,

  -- 业务标识
  order_no          VARCHAR(40) NOT NULL,
  -- 门店业务日内唯一取餐号（001..，PRD §4.7）
  pickup_no         INT UNSIGNED NULL,
  pickup_business_date DATE NULL,

  -- 履约场景 DINE_IN 堂食 / PICKUP 自取
  scenario          VARCHAR(16) NOT NULL,
  -- 桌台（堂食）
  dining_table_id   BIGINT UNSIGNED NULL,
  table_token       VARCHAR(64) NOT NULL DEFAULT '',
  table_label       VARCHAR(32) NOT NULL DEFAULT '',

  -- 取餐类型 IMMEDIATE 即时 / SCHEDULED 预约（reservation-pickup §7.3）
  pickup_type       VARCHAR(16) NOT NULL DEFAULT 'IMMEDIATE',
  scheduled_for     DATETIME NULL,                       -- 门店本地墙钟时间
  pickup_capacity_released_at DATETIME(3) NULL,           -- 容量幂等释放标记

  -- 金额（分）
  item_amount_cents BIGINT NOT NULL,
  packaging_fee_cents BIGINT NOT NULL DEFAULT 0,
  discount_cents    BIGINT NOT NULL DEFAULT 0,
  payable_cents     BIGINT NOT NULL,
  paid_cents        BIGINT NOT NULL DEFAULT 0,
  refunded_cents    BIGINT NOT NULL DEFAULT 0,

  -- 优惠快照
  promotion_id      BIGINT UNSIGNED NULL,
  customer_coupon_id BIGINT UNSIGNED NULL,

  remark            VARCHAR(100) NOT NULL DEFAULT '',

  -- 履约状态机（PRD §6.2）
  -- PENDING_PAYMENT/PAID/ACCEPTED/PREPARING/READY/COMPLETED/
  -- CANCELLED/REFUNDING/REFUNDED/CANCEL_REQUESTED
  fulfillment_state VARCHAR(24) NOT NULL DEFAULT 'PENDING_PAYMENT',
  version           BIGINT NOT NULL DEFAULT 1,

  -- 报价绑定（PRD §4.4/§4.7）：同 quote 唯一
  quote_token       CHAR(64) NOT NULL,
  quote_expires_at  DATETIME(3) NOT NULL,

  mock_order        TINYINT(1) NOT NULL DEFAULT 0,
  created_at        DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at        DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  paid_at           DATETIME(3) NULL,
  accepted_at       DATETIME(3) NULL,
  completed_at      DATETIME(3) NULL,
  cancelled_at      DATETIME(3) NULL,
  expires_at        DATETIME(3) NOT NULL,                 -- 待支付过期（十分钟）

  PRIMARY KEY (id),
  UNIQUE KEY uq_order_no (order_no),
  UNIQUE KEY uq_order_quote (store_id, quote_token),
  -- 取餐号门店业务日唯一（仅非空时）
  UNIQUE KEY uq_order_pickup_no (store_id, pickup_business_date, pickup_no),
  KEY idx_order_store_state (store_id, fulfillment_state),
  KEY idx_order_customer (customer_id, created_at),
  KEY idx_order_scheduled (store_id, scheduled_for),

  CONSTRAINT chk_order_scenario CHECK (scenario IN ('DINE_IN','PICKUP')),
  CONSTRAINT chk_order_pickup_type CHECK (pickup_type IN ('IMMEDIATE','SCHEDULED')),
  CONSTRAINT chk_order_amts CHECK (
    item_amount_cents >= 0 AND packaging_fee_cents >= 0 AND discount_cents >= 0
    AND payable_cents >= 0 AND paid_cents >= 0 AND refunded_cents >= 0
  ),
  CONSTRAINT chk_order_paid_le_payable CHECK (paid_cents <= payable_cents),
  -- 堂食不带预约时间（PRD §4.6）
  CONSTRAINT chk_order_dine_no_schedule CHECK (NOT (scenario = 'DINE_IN' AND scheduled_for IS NOT NULL)),

  CONSTRAINT fk_order_store FOREIGN KEY (store_id) REFERENCES stores(id) ON DELETE RESTRICT,
  CONSTRAINT fk_order_customer FOREIGN KEY (customer_id) REFERENCES customers(id) ON DELETE RESTRICT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- 订单项快照（PRD §4.9：历史展示不受改名/改价/删除影响）。
CREATE TABLE order_items (
  id                BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  order_id          BIGINT UNSIGNED NOT NULL,
  store_id          BIGINT UNSIGNED NOT NULL,
  -- 快照 ID（便于“再来一单”重新匹配，但展示以快照文本为准）
  sku_id            BIGINT UNSIGNED NOT NULL,
  product_id        BIGINT UNSIGNED NOT NULL,
  sku_name          VARCHAR(100) NOT NULL,
  product_name      VARCHAR(100) NOT NULL,
  unit_price_cents  BIGINT NOT NULL,           -- SKU 基础价 + 选项加价
  quantity          INT NOT NULL,
  -- 选项快照 JSON: [{group_name, option_name, price_modifier_cents}]
  options_snapshot  JSON NOT NULL,
  packaging_fee_cents BIGINT NOT NULL DEFAULT 0,
  line_amount_cents BIGINT NOT NULL,
  created_at        DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  KEY idx_oi_order (order_id),
  CONSTRAINT chk_oitem_qty CHECK (quantity BETWEEN 1 AND 99),
  CONSTRAINT chk_oitem_price CHECK (unit_price_cents >= 0 AND line_amount_cents >= 0),
  CONSTRAINT fk_oi_order FOREIGN KEY (order_id) REFERENCES orders(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- 订单事件时间线（不可变，PRD §4.9/§6.1）。
CREATE TABLE order_events (
  id              BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  order_id        BIGINT UNSIGNED NOT NULL,
  store_id        BIGINT UNSIGNED NOT NULL,
  event_type      VARCHAR(40) NOT NULL,
  from_state      VARCHAR(24) NULL,
  to_state        VARCHAR(24) NULL,
  actor_type      VARCHAR(16) NOT NULL DEFAULT 'SYSTEM',  -- SYSTEM/CUSTOMER/STAFF
  actor_id        BIGINT UNSIGNED NULL,
  summary         VARCHAR(255) NOT NULL DEFAULT '',
  metadata        JSON NULL,
  created_at      DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  KEY idx_oe_order (order_id, created_at),
  CONSTRAINT chk_oe_actor CHECK (actor_type IN ('SYSTEM','CUSTOMER','STAFF')),
  CONSTRAINT fk_oe_order FOREIGN KEY (order_id) REFERENCES orders(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- 支付（一单唯一，PRD §4.8/§17.2）。
CREATE TABLE payments (
  id                  BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  store_id            BIGINT UNSIGNED NOT NULL,
  order_id            BIGINT UNSIGNED NOT NULL,
  amount_cents        BIGINT NOT NULL,
  -- PREPAY_CREATED / SUCCESS / CLOSED / FAILED
  status              VARCHAR(24) NOT NULL DEFAULT 'PREPAY_CREATED',
  -- 微信支付标识
  prepay_id           VARCHAR(64) NOT NULL DEFAULT '',
  transaction_id      VARCHAR(64) NOT NULL DEFAULT '',
  provider_event_id   VARCHAR(64) NOT NULL DEFAULT '',
  mock_payment        TINYINT(1) NOT NULL DEFAULT 0,
  -- 微信支付 v3 预支付参数快照（小程序 wx.requestPayment 用）
  prepay_payload      JSON NULL,
  last_error          VARCHAR(255) NOT NULL DEFAULT '',
  created_at          DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at          DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  succeeded_at        DATETIME(3) NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uq_pay_order (order_id),
  KEY idx_pay_status (status),
  KEY idx_pay_tx (transaction_id),
  CONSTRAINT chk_pay_amt CHECK (amount_cents >= 0),
  CONSTRAINT chk_pay_status CHECK (status IN ('PREPAY_CREATED','SUCCESS','CLOSED','FAILED')),
  CONSTRAINT fk_pay_order FOREIGN KEY (order_id) REFERENCES orders(id) ON DELETE RESTRICT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- 退款（一单最多一个有效退款意图 + 唯一商户退款号，PRD §14.3/§17.2）。
CREATE TABLE refunds (
  id                  BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  store_id            BIGINT UNSIGNED NOT NULL,
  order_id            BIGINT UNSIGNED NOT NULL,
  payment_id          BIGINT UNSIGNED NOT NULL,
  amount_cents        BIGINT NOT NULL,
  -- CREATED / PROCESSING / SUCCESS / ABNORMAL / CANCELLED
  status              VARCHAR(24) NOT NULL DEFAULT 'CREATED',
  refund_no           VARCHAR(40) NOT NULL,           -- 商户退款号（唯一）
  refund_id_wx        VARCHAR(64) NOT NULL DEFAULT '', -- 微信退款 ID
  provider_event_id   VARCHAR(64) NOT NULL DEFAULT '',
  reason              VARCHAR(255) NOT NULL DEFAULT '',
  -- 触发来源：CUSTOMER_AUTO 顾客自动 / CUSTOMER_REVIEW 待审核 / STAFF_MANUAL 门店主动
  trigger_kind        VARCHAR(24) NOT NULL,
  mock_refund         TINYINT(1) NOT NULL DEFAULT 0,
  last_error          VARCHAR(255) NOT NULL DEFAULT '',
  created_at          DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at          DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  succeeded_at        DATETIME(3) NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uq_refund_no (refund_no),
  UNIQUE KEY uq_refund_order_active (order_id, status),
  KEY idx_refund_status (status),
  CONSTRAINT chk_refund_amt CHECK (amount_cents > 0),
  CONSTRAINT chk_refund_status CHECK (status IN ('CREATED','PROCESSING','SUCCESS','ABNORMAL','CANCELLED')),
  CONSTRAINT chk_refund_trigger CHECK (trigger_kind IN ('CUSTOMER_AUTO','CUSTOMER_REVIEW','STAFF_MANUAL')),
  CONSTRAINT fk_refund_order FOREIGN KEY (order_id) REFERENCES orders(id) ON DELETE RESTRICT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- 注：uq_refund_order_active 用 (order_id, status) 近似保证“一单一个有效退款”，
-- 真正的“一单一有效意图”需要应用层 + 部分状态枚举保证（MySQL 唯一索引不支持条件）。
