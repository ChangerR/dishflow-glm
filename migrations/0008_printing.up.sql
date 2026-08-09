-- 0008_printing.up.sql
-- 云打印域：商鹏配置、打印机、打印任务。
-- PRD §13。密钥 AES-GCM 加密；配置状态 draft/ready/disabled；任务状态机。

-- 商鹏配置（每店一组 appid/appsecret，加密）。
CREATE TABLE cloud_print_config (
  store_id            BIGINT UNSIGNED NOT NULL,
  -- draft / ready / disabled（PRD §13.1）
  status              VARCHAR(16) NOT NULL DEFAULT 'draft',
  -- 读取接口只返回是否已配置，密文不回显（PRD §13.1/§18）
  appid               VARCHAR(64) NOT NULL DEFAULT '',
  app_secret_ciphertext BLOB NULL,
  app_secret_nonce     BLOB NULL,
  auto_print           TINYINT(1) NOT NULL DEFAULT 1,
  -- 显式模拟打印开关（PRD §13.1）
  mock_print           TINYINT(1) NOT NULL DEFAULT 0,
  created_at           DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at           DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (store_id),
  CONSTRAINT chk_cpc_status CHECK (status IN ('draft','ready','disabled')),
  CONSTRAINT fk_cpc_store FOREIGN KEY (store_id) REFERENCES stores(id) ON DELETE RESTRICT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- 云打印机（商鹏设备）。
CREATE TABLE cloud_printers (
  id              BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  store_id        BIGINT UNSIGNED NOT NULL,
  sn              VARCHAR(64) NOT NULL,
  -- 写入型 KEY（加密，PRD §13.1）
  write_key_ciphertext BLOB NULL,
  write_key_nonce      BLOB NULL,
  name            VARCHAR(100) NOT NULL DEFAULT '',
  is_default      TINYINT(1) NOT NULL DEFAULT 0,
  copies          INT NOT NULL DEFAULT 1,
  enabled         TINYINT(1) NOT NULL DEFAULT 1,
  online          TINYINT(1) NOT NULL DEFAULT 0,
  note            VARCHAR(255) NOT NULL DEFAULT '',
  last_seen_at    DATETIME(3) NULL,
  created_at      DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at      DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  UNIQUE KEY uq_printer_store_sn (store_id, sn),
  CONSTRAINT chk_printer_copies CHECK (copies BETWEEN 1 AND 5),
  CONSTRAINT fk_printer_store FOREIGN KEY (store_id) REFERENCES stores(id) ON DELETE RESTRICT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- 打印任务。类型 order/test/reprint/purchase；状态 QUEUED→SENDING→SUBMITTED→PRINTED，失败 FAILED。
CREATE TABLE cloud_print_jobs (
  id              BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  store_id        BIGINT UNSIGNED NOT NULL,
  job_type        VARCHAR(16) NOT NULL,
  status          VARCHAR(16) NOT NULL DEFAULT 'QUEUED',
  -- 关联单号/清单号（可空）
  order_id        BIGINT UNSIGNED NULL,
  purchase_list_id BIGINT UNSIGNED NULL,
  printer_id      BIGINT UNSIGNED NULL,
  -- 商鹏任务 ID
  provider_job_id VARCHAR(64) NOT NULL DEFAULT '',
  -- 58mm 小票内容（或采购清单内容）
  content         MEDIUMTEXT NOT NULL,
  attempts        INT NOT NULL DEFAULT 0,
  last_error      VARCHAR(255) NOT NULL DEFAULT '',
  created_at      DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at      DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  KEY idx_cpj_status (store_id, status),
  KEY idx_cpj_order (order_id),
  CONSTRAINT chk_cpj_type CHECK (job_type IN ('order','test','reprint','purchase')),
  CONSTRAINT chk_cpj_status CHECK (status IN ('QUEUED','SENDING','SUBMITTED','PRINTED','FAILED')),
  CONSTRAINT fk_cpj_store FOREIGN KEY (store_id) REFERENCES stores(id) ON DELETE RESTRICT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
