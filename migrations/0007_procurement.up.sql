-- 0007_procurement.up.sql
-- 物料买菜域：物料目录、采购清单、清单项（快照）、清单事件。
-- PRD §12；状态机 DRAFT→SUBMITTED→PRINTED→COMPLETED，任意未完成可 VOID。

CREATE TABLE materials (
  id              BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  store_id        BIGINT UNSIGNED NOT NULL,
  -- 同门店名称唯一（PRD §12.1）
  name            VARCHAR(64) NOT NULL,
  image_url       VARCHAR(512) NOT NULL DEFAULT '',
  category        VARCHAR(64) NOT NULL DEFAULT '',
  unit            VARCHAR(16) NOT NULL DEFAULT '',
  default_qty     DECIMAL(10,2) NOT NULL DEFAULT 1.00,
  note            VARCHAR(255) NOT NULL DEFAULT '',
  enabled         TINYINT(1) NOT NULL DEFAULT 1,
  sort_order      INT NOT NULL DEFAULT 0,
  created_at      DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at      DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  UNIQUE KEY uq_mat_store_name (store_id, name),
  KEY idx_mat_store (store_id, sort_order),
  CONSTRAINT chk_mat_default CHECK (default_qty >= 0),
  CONSTRAINT fk_mat_store FOREIGN KEY (store_id) REFERENCES stores(id) ON DELETE RESTRICT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE purchase_lists (
  id              BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  store_id        BIGINT UNSIGNED NOT NULL,
  list_no         VARCHAR(40) NOT NULL,
  business_date   DATE NOT NULL,
  title           VARCHAR(100) NOT NULL DEFAULT '',
  -- DRAFT / SUBMITTED / PRINTED / COMPLETED / VOID
  status          VARCHAR(16) NOT NULL DEFAULT 'DRAFT',
  total_amount_cents BIGINT NOT NULL DEFAULT 0,
  version         BIGINT NOT NULL DEFAULT 1,
  print_count     INT NOT NULL DEFAULT 0,
  void_reason     VARCHAR(255) NOT NULL DEFAULT '',
  created_by      BIGINT UNSIGNED NULL,
  updated_by      BIGINT UNSIGNED NULL,
  created_at      DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at      DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  UNIQUE KEY uq_pl_store_no (store_id, list_no),
  KEY idx_pl_store_date (store_id, business_date),
  CONSTRAINT chk_pl_status CHECK (status IN ('DRAFT','SUBMITTED','PRINTED','COMPLETED','VOID')),
  CONSTRAINT chk_pl_total CHECK (total_amount_cents >= 0),
  CONSTRAINT fk_pl_store FOREIGN KEY (store_id) REFERENCES stores(id) ON DELETE RESTRICT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- 清单项（物料名称/单位快照，PRD §12.2；停用物料不影响快照）。
CREATE TABLE purchase_list_items (
  id              BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  purchase_list_id BIGINT UNSIGNED NOT NULL,
  store_id        BIGINT UNSIGNED NOT NULL,
  material_id     BIGINT UNSIGNED NULL,           -- 停用后仍保留快照，关联可为空
  material_name   VARCHAR(64) NOT NULL,
  unit            VARCHAR(16) NOT NULL DEFAULT '',
  quantity        DECIMAL(10,2) NOT NULL,
  note            VARCHAR(255) NOT NULL DEFAULT '',
  sort_order      INT NOT NULL DEFAULT 0,
  created_at      DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at      DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  -- 同清单不重复物料（PRD §12.2）
  UNIQUE KEY uq_pli_list_material (purchase_list_id, material_id),
  CONSTRAINT chk_pli_qty CHECK (quantity > 0),
  CONSTRAINT fk_pli_list FOREIGN KEY (purchase_list_id) REFERENCES purchase_lists(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- 清单事件（状态变更 + 操作者，PRD §12.2）。
CREATE TABLE purchase_list_events (
  id              BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  purchase_list_id BIGINT UNSIGNED NOT NULL,
  store_id        BIGINT UNSIGNED NOT NULL,
  event_type      VARCHAR(40) NOT NULL,
  from_status     VARCHAR(16) NULL,
  to_status       VARCHAR(16) NULL,
  actor_admin_user_id BIGINT UNSIGNED NULL,
  summary         VARCHAR(255) NOT NULL DEFAULT '',
  created_at      DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  KEY idx_ple_list (purchase_list_id, created_at),
  CONSTRAINT fk_ple_list FOREIGN KEY (purchase_list_id) REFERENCES purchase_lists(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
