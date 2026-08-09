-- 0002_menu.up.sql
-- 菜单域：分类、菜品、SKU、选项组、选项项。
-- PRD §4.3/§7.1/§7.2；30 天软删除回收站（§7.1）。

-- 分类。删除为 30 天软删除：deleted_at + delete_batch_id（§7.1）。
CREATE TABLE categories (
  id               BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  store_id         BIGINT UNSIGNED NOT NULL,
  name             VARCHAR(64) NOT NULL,
  enabled          TINYINT(1) NOT NULL DEFAULT 1,
  sort_order       INT NOT NULL DEFAULT 0,
  -- 回收站
  deleted_at       DATETIME(3) NULL,
  delete_batch_id  CHAR(32) NOT NULL DEFAULT '',
  created_at       DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at       DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  -- 门店内启用/未删除分类名唯一（软删除后允许同名重建：仅约束未删除）
  KEY idx_cat_store (store_id, sort_order),
  CONSTRAINT fk_cat_store FOREIGN KEY (store_id) REFERENCES stores(id) ON DELETE RESTRICT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- 菜品。
CREATE TABLE products (
  id               BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  store_id         BIGINT UNSIGNED NOT NULL,
  category_id      BIGINT UNSIGNED NOT NULL,
  code             VARCHAR(64) NOT NULL DEFAULT '',
  name             VARCHAR(100) NOT NULL,
  description      VARCHAR(500) NOT NULL DEFAULT '',
  image_url        VARCHAR(512) NOT NULL DEFAULT '',
  -- 启停/上架
  enabled          TINYINT(1) NOT NULL DEFAULT 1,
  -- 人工售罄（优先于剩余库存，PRD §7.3）
  manually_sold_out TINYINT(1) NOT NULL DEFAULT 0,
  sort_order       INT NOT NULL DEFAULT 0,
  -- 每份自取包装费（分，PRD §4.5/§7.2）；堂食包装费为 0
  packaging_fee_cents BIGINT NOT NULL DEFAULT 0,
  -- 回收站（与分类同批次联动，§7.1）
  deleted_at       DATETIME(3) NULL,
  delete_batch_id  CHAR(32) NOT NULL DEFAULT '',
  created_at       DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at       DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  KEY idx_prod_store_cat (store_id, category_id, sort_order),
  CONSTRAINT chk_prod_pkg CHECK (packaging_fee_cents >= 0),
  CONSTRAINT fk_prod_store FOREIGN KEY (store_id) REFERENCES stores(id) ON DELETE RESTRICT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- SKU。库存模式：UNLIMITED | DAILY（PRD §4.3）。
CREATE TABLE skus (
  id               BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  store_id         BIGINT UNSIGNED NOT NULL,
  product_id       BIGINT UNSIGNED NOT NULL,
  name             VARCHAR(100) NOT NULL DEFAULT '',
  -- 基础价（分）
  price_cents      BIGINT NOT NULL,
  inventory_mode   VARCHAR(16) NOT NULL DEFAULT 'UNLIMITED',
  -- 每日库存（DAILY 模式默认数量；实际按业务日存 daily_inventory）
  daily_stock      INT NOT NULL DEFAULT 0,
  enabled          TINYINT(1) NOT NULL DEFAULT 1,
  is_default       TINYINT(1) NOT NULL DEFAULT 0,
  sort_order       INT NOT NULL DEFAULT 0,
  -- 与菜品同批次进回收站
  deleted_at       DATETIME(3) NULL,
  delete_batch_id  CHAR(32) NOT NULL DEFAULT '',
  created_at       DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at       DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  KEY idx_sku_product (product_id, sort_order),
  CONSTRAINT chk_sku_price CHECK (price_cents >= 0),
  CONSTRAINT chk_sku_mode CHECK (inventory_mode IN ('UNLIMITED','DAILY')),
  CONSTRAINT chk_sku_daily CHECK (daily_stock >= 0),
  CONSTRAINT fk_sku_product FOREIGN KEY (product_id) REFERENCES products(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- 选项组。
CREATE TABLE option_groups (
  id               BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  store_id         BIGINT UNSIGNED NOT NULL,
  product_id       BIGINT UNSIGNED NOT NULL,
  name             VARCHAR(100) NOT NULL,
  -- SINGLE 单选 / MULTI 多选
  selection_type   VARCHAR(8) NOT NULL DEFAULT 'SINGLE',
  is_required      TINYINT(1) NOT NULL DEFAULT 0,
  min_select       INT NOT NULL DEFAULT 1,
  max_select       INT NOT NULL DEFAULT 1,
  sort_order       INT NOT NULL DEFAULT 0,
  deleted_at       DATETIME(3) NULL,
  delete_batch_id  CHAR(32) NOT NULL DEFAULT '',
  created_at       DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at       DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  KEY idx_og_product (product_id, sort_order),
  CONSTRAINT chk_og_type CHECK (selection_type IN ('SINGLE','MULTI')),
  CONSTRAINT chk_og_select CHECK (min_select >= 0 AND max_select >= 1 AND min_select <= max_select),
  CONSTRAINT fk_og_product FOREIGN KEY (product_id) REFERENCES products(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- 选项项。
CREATE TABLE option_items (
  id               BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  store_id         BIGINT UNSIGNED NOT NULL,
  option_group_id  BIGINT UNSIGNED NOT NULL,
  name             VARCHAR(100) NOT NULL,
  -- 加价（分）
  price_modifier_cents BIGINT NOT NULL DEFAULT 0,
  enabled          TINYINT(1) NOT NULL DEFAULT 1,
  is_default       TINYINT(1) NOT NULL DEFAULT 0,
  sort_order       INT NOT NULL DEFAULT 0,
  deleted_at       DATETIME(3) NULL,
  delete_batch_id  CHAR(32) NOT NULL DEFAULT '',
  created_at       DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at       DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  KEY idx_oi_group (option_group_id, sort_order),
  CONSTRAINT chk_oi_price CHECK (price_modifier_cents >= 0),
  CONSTRAINT fk_oi_group FOREIGN KEY (option_group_id) REFERENCES option_groups(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
