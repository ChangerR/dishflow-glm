-- 0003_marketing.up.sql
-- 营销会员域：满减、优惠券模板、顾客券、会员、积分流水。
-- PRD §4.5/§4.11/§4.12/§7.4/§8。

-- 满减（PRD §4.5/§7.4）。
CREATE TABLE promotions (
  id              BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  store_id        BIGINT UNSIGNED NOT NULL,
  name            VARCHAR(100) NOT NULL,
  threshold_cents BIGINT NOT NULL,
  discount_cents  BIGINT NOT NULL,
  -- 适用范围（产品/分类 ID 列表 JSON；当前算价仅满减择优，PRD §22）
  scope           VARCHAR(16) NOT NULL DEFAULT 'STORE',
  scope_ref       JSON NULL,
  -- 叠加策略（字段保留；当前生产基线为“满减 vs 券择优不叠加”，PRD §22）
  stack_policy    VARCHAR(16) NOT NULL DEFAULT 'EXCLUSIVE',
  starts_at       DATETIME(3) NOT NULL,
  ends_at         DATETIME(3) NOT NULL,
  enabled         TINYINT(1) NOT NULL DEFAULT 1,
  deleted_at      DATETIME(3) NULL,
  created_at      DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at      DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  KEY idx_promo_store_time (store_id, starts_at, ends_at),
  CONSTRAINT chk_promo_thr CHECK (threshold_cents >= 0),
  CONSTRAINT chk_promo_disc CHECK (discount_cents >= 0),
  CONSTRAINT chk_promo_scope CHECK (scope IN ('STORE','CATEGORY','PRODUCT')),
  CONSTRAINT fk_promo_store FOREIGN KEY (store_id) REFERENCES stores(id) ON DELETE RESTRICT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- 优惠券模板（PRD §4.11/§7.4）。
CREATE TABLE coupon_templates (
  id                  BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  store_id            BIGINT UNSIGNED NOT NULL,
  name                VARCHAR(100) NOT NULL,
  -- 满减：最低消费 + 减免额（分）
  min_spend_cents     BIGINT NOT NULL DEFAULT 0,
  discount_cents      BIGINT NOT NULL,
  scope               VARCHAR(16) NOT NULL DEFAULT 'STORE',
  scope_ref           JSON NULL,
  starts_at           DATETIME(3) NOT NULL,
  ends_at             DATETIME(3) NOT NULL,
  enabled             TINYINT(1) NOT NULL DEFAULT 1,
  -- 公开领取
  publicly_claimable  TINYINT(1) NOT NULL DEFAULT 0,
  -- 人群：ALL / NEW / OLD / ACTIVE_180D / DORMANT_180D（PRD §7.4）
  audience            VARCHAR(16) NOT NULL DEFAULT 'ALL',
  -- 积分兑换（PRD §4.12）
  redeemable          TINYINT(1) NOT NULL DEFAULT 0,
  points_cost         INT NOT NULL DEFAULT 0,
  deleted_at          DATETIME(3) NULL,
  created_at          DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at          DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  KEY idx_ct_store (store_id, enabled, starts_at, ends_at),
  CONSTRAINT chk_ct_min CHECK (min_spend_cents >= 0),
  CONSTRAINT chk_ct_disc CHECK (discount_cents > 0),
  CONSTRAINT chk_ct_audience CHECK (audience IN ('ALL','NEW','OLD','ACTIVE_180D','DORMANT_180D')),
  CONSTRAINT chk_ct_scope CHECK (scope IN ('STORE','CATEGORY','PRODUCT')),
  CONSTRAINT chk_ct_points CHECK (points_cost >= 0),
  CONSTRAINT fk_ct_store FOREIGN KEY (store_id) REFERENCES stores(id) ON DELETE RESTRICT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- 顾客券实例（PRD §4.11/§4.12）。
CREATE TABLE customer_coupons (
  id              BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  store_id        BIGINT UNSIGNED NOT NULL,
  customer_id     BIGINT UNSIGNED NOT NULL,
  template_id     BIGINT UNSIGNED NOT NULL,
  -- AVAILABLE / USED / EXPIRED / REDEEMED（兑换）
  status          VARCHAR(16) NOT NULL DEFAULT 'AVAILABLE',
  -- 来源：CLAIM 公开领取 / ISSUE 按人群发放 / REDEEM 积分兑换 / NEW_MEMBER 新人礼
  source          VARCHAR(16) NOT NULL DEFAULT 'CLAIM',
  claimed_at      DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  expires_at      DATETIME(3) NOT NULL,
  used_at         DATETIME(3) NULL,
  -- 核销幂等键
  redeem_key      CHAR(32) NOT NULL DEFAULT '',
  created_at      DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at      DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  -- 领取幂等：同一顾客 + 模板 + 来源一次性（CLAIM）
  UNIQUE KEY uq_cc_claim (customer_id, template_id, source),
  KEY idx_cc_customer_status (customer_id, status, expires_at),
  CONSTRAINT chk_cc_status CHECK (status IN ('AVAILABLE','USED','EXPIRED','REDEEMED')),
  CONSTRAINT chk_cc_source CHECK (source IN ('CLAIM','ISSUE','REDEEM','NEW_MEMBER')),
  CONSTRAINT fk_cc_customer FOREIGN KEY (customer_id) REFERENCES customers(id) ON DELETE CASCADE,
  CONSTRAINT fk_cc_template FOREIGN KEY (template_id) REFERENCES coupon_templates(id) ON DELETE RESTRICT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- 顾客会员（PRD §4.12/§8）。会员号门店唯一稳定；状态 ACTIVE/FROZEN。
CREATE TABLE customer_memberships (
  id              BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  store_id        BIGINT UNSIGNED NOT NULL,
  customer_id     BIGINT UNSIGNED NOT NULL,
  -- 会员号（门店范围唯一）
  member_no       VARCHAR(32) NOT NULL,
  status          VARCHAR(16) NOT NULL DEFAULT 'ACTIVE',
  points_balance  BIGINT NOT NULL DEFAULT 0,
  joined_at       DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  created_at      DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at      DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  UNIQUE KEY uq_cm_customer (customer_id),
  UNIQUE KEY uq_cm_store_member_no (store_id, member_no),
  CONSTRAINT chk_cm_status CHECK (status IN ('ACTIVE','FROZEN')),
  CONSTRAINT chk_cm_points CHECK (points_balance >= 0),
  CONSTRAINT fk_cm_customer FOREIGN KEY (customer_id) REFERENCES customers(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- 积分流水（不可变，带 balance_after，PRD §4.12）。
-- 类型：EARN 入账 / REVERSE 扣回 / REDEEM 兑券 / ADJUST 人工调整
CREATE TABLE member_points_ledger (
  id              BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  store_id        BIGINT UNSIGNED NOT NULL,
  membership_id   BIGINT UNSIGNED NOT NULL,
  customer_id     BIGINT UNSIGNED NOT NULL,
  delta           BIGINT NOT NULL,
  balance_after   BIGINT NOT NULL,
  entry_type      VARCHAR(16) NOT NULL,
  -- 关联订单/退款/兑换券（可空）
  order_id        BIGINT UNSIGNED NULL,
  refund_id       BIGINT UNSIGNED NULL,
  coupon_id       BIGINT UNSIGNED NULL,
  reason          VARCHAR(100) NOT NULL DEFAULT '',
  operator_admin_user_id BIGINT UNSIGNED NULL,
  idempotency_key VARCHAR(100) NOT NULL DEFAULT '',
  created_at      DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  UNIQUE KEY uq_mpl_idem (membership_id, idempotency_key),
  KEY idx_mpl_customer (customer_id, created_at),
  CONSTRAINT chk_mpl_delta_nonzero CHECK (delta <> 0),
  CONSTRAINT chk_mpl_bal CHECK (balance_after >= 0),
  CONSTRAINT chk_mpl_type CHECK (entry_type IN ('EARN','REVERSE','REDEEM','ADJUST')),
  CONSTRAINT fk_mpl_membership FOREIGN KEY (membership_id) REFERENCES customer_memberships(id) ON DELETE RESTRICT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
