-- 0009_platform.up.sql
-- 平台入驻域：开店申请、加入申请。PRD §10.4。
-- 状态 PENDING / APPROVED / REJECTED。

CREATE TABLE shop_applications (
  id              BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  applicant_admin_user_id BIGINT UNSIGNED NOT NULL,
  store_name      VARCHAR(100) NOT NULL,
  contact         VARCHAR(100) NOT NULL DEFAULT '',
  status          VARCHAR(16) NOT NULL DEFAULT 'PENDING',
  submitted_at    DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  reviewed_at     DATETIME(3) NULL,
  reviewer_admin_user_id BIGINT UNSIGNED NULL,
  -- 审批通过后建店得到
  created_store_id BIGINT UNSIGNED NULL,
  note            VARCHAR(255) NOT NULL DEFAULT '',
  created_at      DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at      DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  KEY idx_sa_applicant (applicant_admin_user_id),
  KEY idx_sa_status (status),
  CONSTRAINT chk_sa_status CHECK (status IN ('PENDING','APPROVED','REJECTED')),
  CONSTRAINT fk_sa_applicant FOREIGN KEY (applicant_admin_user_id) REFERENCES admin_users(id) ON DELETE RESTRICT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE shop_join_requests (
  id              BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  store_id        BIGINT UNSIGNED NOT NULL,
  applicant_admin_user_id BIGINT UNSIGNED NOT NULL,
  requested_role  VARCHAR(16) NOT NULL,
  status          VARCHAR(16) NOT NULL DEFAULT 'PENDING',
  submitted_at    DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  reviewed_at     DATETIME(3) NULL,
  reviewer_admin_user_id BIGINT UNSIGNED NULL,
  note            VARCHAR(255) NOT NULL DEFAULT '',
  created_at      DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at      DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  KEY idx_sjr_store_status (store_id, status),
  KEY idx_sjr_applicant (applicant_admin_user_id),
  CONSTRAINT chk_sjr_role CHECK (requested_role IN ('STAFF','MANAGER','OWNER')),
  CONSTRAINT chk_sjr_status CHECK (status IN ('PENDING','APPROVED','REJECTED')),
  CONSTRAINT fk_sjr_store FOREIGN KEY (store_id) REFERENCES stores(id) ON DELETE RESTRICT,
  CONSTRAINT fk_sjr_applicant FOREIGN KEY (applicant_admin_user_id) REFERENCES admin_users(id) ON DELETE RESTRICT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- 微信支付/登录配置（PRD §14.1）。密钥只写不读，AES-GCM 加密。
CREATE TABLE payment_config (
  store_id                  BIGINT UNSIGNED NOT NULL,
  -- draft / ready / disabled
  status                    VARCHAR(16) NOT NULL DEFAULT 'draft',
  mch_id                    VARCHAR(32) NOT NULL DEFAULT '',
  mch_cert_serial_no        VARCHAR(64) NOT NULL DEFAULT '',
  -- APIv3 密钥（加密）
  apiv3_key_ciphertext      BLOB NULL,
  apiv3_key_nonce           BLOB NULL,
  -- 商户私钥 PEM（加密）
  mch_private_key_ciphertext BLOB NULL,
  mch_private_key_nonce     BLOB NULL,
  -- 微信支付验签材料模式：PUB_KEY 平台公钥模式 / PLATFORM_CERT 平台证书模式
  verify_mode               VARCHAR(16) NOT NULL DEFAULT 'PUB_KEY',
  wechat_pub_key_id         VARCHAR(64) NOT NULL DEFAULT '',
  wechat_pub_key_ciphertext BLOB NULL,
  wechat_pub_key_nonce      BLOB NULL,
  platform_cert_serial_no   VARCHAR(64) NOT NULL DEFAULT '',
  platform_cert_ciphertext  BLOB NULL,
  platform_cert_nonce       BLOB NULL,
  -- mock 支付开关（仅店主可切，PRD §14.4）
  mock_payment              TINYINT(1) NOT NULL DEFAULT 0,
  created_at                DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at                DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (store_id),
  CONSTRAINT chk_pc_status CHECK (status IN ('draft','ready','disabled')),
  CONSTRAINT chk_pc_verify CHECK (verify_mode IN ('PUB_KEY','PLATFORM_CERT')),
  CONSTRAINT fk_pc_store FOREIGN KEY (store_id) REFERENCES stores(id) ON DELETE RESTRICT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
