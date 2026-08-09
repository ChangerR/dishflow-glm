-- 0004_inventory.up.sql
-- 库存与预约容量域。
-- PRD §4.7/§4.6；reservation-pickup §5.3/§7.3。
-- 不变量：available_qty >= reserved_qty + sold_qty（PRD §17.2）。

-- 每日库存：按门店 + SKU + 业务日。
CREATE TABLE daily_inventory (
  id            BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  store_id      BIGINT UNSIGNED NOT NULL,
  sku_id        BIGINT UNSIGNED NOT NULL,
  -- 业务日（门店本地，PRD §4.7；不能用 DB CURRENT_DATE 推断）
  business_date DATE NOT NULL,
  -- 可用 = base（含人工调整）；available_qty = target - reserved - sold
  target_qty    INT NOT NULL DEFAULT 0,
  reserved_qty  INT NOT NULL DEFAULT 0,
  sold_qty      INT NOT NULL DEFAULT 0,
  created_at    DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at    DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  UNIQUE KEY uq_di_store_sku_date (store_id, sku_id, business_date),
  CONSTRAINT chk_di_target CHECK (target_qty >= 0),
  CONSTRAINT chk_di_reserved CHECK (reserved_qty >= 0),
  CONSTRAINT chk_di_sold CHECK (sold_qty >= 0),
  -- 不可用数量不超过目标（不变量，PRD §17.2）
  CONSTRAINT chk_di_inv CHECK (reserved_qty + sold_qty <= target_qty),
  CONSTRAINT fk_di_sku FOREIGN KEY (sku_id) REFERENCES skus(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- 库存预占（订单级，便于释放/转已售，PRD §4.7）。
CREATE TABLE inventory_reservations (
  id            BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  store_id      BIGINT UNSIGNED NOT NULL,
  order_id      BIGINT UNSIGNED NOT NULL,
  sku_id        BIGINT UNSIGNED NOT NULL,
  business_date DATE NOT NULL,
  quantity      INT NOT NULL,
  -- RESERVED / FULFILLED（转已售）/ RELEASED
  state         VARCHAR(16) NOT NULL DEFAULT 'RESERVED',
  created_at    DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at    DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  UNIQUE KEY uq_ir_order_sku_date (order_id, sku_id, business_date),
  KEY idx_ir_state (state),
  CONSTRAINT chk_ir_qty CHECK (quantity > 0),
  CONSTRAINT chk_ir_state CHECK (state IN ('RESERVED','FULFILLED','RELEASED')),
  CONSTRAINT fk_ir_sku FOREIGN KEY (sku_id) REFERENCES skus(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- 库存流水（不可变审计）。
-- 类型：RESERVE 预占 / FULFILL 转已售 / RELEASE 释放 / ADJUST 人工调整
CREATE TABLE inventory_movements (
  id            BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  store_id      BIGINT UNSIGNED NOT NULL,
  sku_id        BIGINT UNSIGNED NOT NULL,
  business_date DATE NOT NULL,
  movement_type VARCHAR(16) NOT NULL,
  -- 数量变化（保留 +、释放 -）
  delta_reserved INT NOT NULL DEFAULT 0,
  delta_sold     INT NOT NULL DEFAULT 0,
  order_id      BIGINT UNSIGNED NULL,
  reason        VARCHAR(100) NOT NULL DEFAULT '',
  operator_admin_user_id BIGINT UNSIGNED NULL,
  created_at    DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  KEY idx_im_sku_date (sku_id, business_date),
  CONSTRAINT chk_im_type CHECK (movement_type IN ('RESERVE','FULFILL','RELEASE','ADJUST')),
  CONSTRAINT fk_im_sku FOREIGN KEY (sku_id) REFERENCES skus(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- 预约时段容量（门店 + 预约时间唯一；保存容量快照，PRD §4.6/§17.2）。
-- scheduled_for 为门店本地墙钟时间。
CREATE TABLE pickup_slot_capacity (
  id                BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  store_id          BIGINT UNSIGNED NOT NULL,
  scheduled_for     DATETIME NOT NULL,
  capacity_snapshot INT NOT NULL,
  reserved_orders   INT NOT NULL DEFAULT 0,
  created_at        DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at        DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  UNIQUE KEY uq_psc_store_slot (store_id, scheduled_for),
  CONSTRAINT chk_psc_cap CHECK (capacity_snapshot >= 1),
  CONSTRAINT chk_psc_reserved CHECK (reserved_orders >= 0 AND reserved_orders <= capacity_snapshot)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
