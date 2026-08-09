-- 0006_tables.up.sql
-- 桌台域。PRD §4.1/§10.1。
-- 同门店桌号唯一；高熵不透明 token；小程序码存 COS 对象 key（不存 scene 敏感数据）。

CREATE TABLE dining_tables (
  id              BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  store_id        BIGINT UNSIGNED NOT NULL,
  -- 桌号（门店范围唯一，PRD §17.2）
  table_no        VARCHAR(32) NOT NULL,
  area            VARCHAR(32) NOT NULL DEFAULT '',
  enabled         TINYINT(1) NOT NULL DEFAULT 1,
  -- 当前生效的高熵不透明 token（换码原子替换，旧码立即失效，PRD §10.1）
  table_token     CHAR(32) NOT NULL,
  -- 小程序码 COS 对象 key（不存图片本身；下载需门店权限，PRD §10.1）
  minicode_object_key VARCHAR(255) NOT NULL DEFAULT '',
  -- scene 不含敏感数据（PRD §10.1）
  scene           VARCHAR(64) NOT NULL DEFAULT '',
  created_at      DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at      DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  UNIQUE KEY uq_table_store_no (store_id, table_no),
  UNIQUE KEY uq_table_token (table_token),
  CONSTRAINT fk_table_store FOREIGN KEY (store_id) REFERENCES stores(id) ON DELETE RESTRICT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
