-- 0001_identity.up.sql
-- 身份域：stores（多租户根）、顾客、顾客会话、后台账号、后台会话、门店成员。
-- PRD §17.1 身份实体；§2.2 多租户规则。

-- 门店（多租户根）。PRD §10.2 门店设置、§5.1 预约配置字段。
CREATE TABLE stores (
  id                    BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  -- 平台展示用名称
  name                  VARCHAR(100) NOT NULL,
  -- 启停（PRD §10.2 营业开关与平台启停；营业开关用 business_open）
  enabled               TINYINT(1) NOT NULL DEFAULT 1,
  -- 营业开关：关闭后仍可浏览菜单/历史订单，但禁止结算（PRD §10.2）
  business_open         TINYINT(1) NOT NULL DEFAULT 1,
  phone                 VARCHAR(40) NOT NULL DEFAULT '',
  address               VARCHAR(255) NOT NULL DEFAULT '',
  -- 单个营业区间 HH:mm-HH:mm，不支持跨午夜（PRD §4.6/§5.1）
  business_hours        VARCHAR(32) NOT NULL DEFAULT '',
  announcement          VARCHAR(500) NOT NULL DEFAULT '',
  -- 预计取餐/制作分钟数 1..180（PRD §5.1）
  pickup_minutes        INT UNSIGNED NOT NULL DEFAULT 15,
  -- 预约配置（PRD §5.1 / reservation-pickup §7.3）
  scheduled_pickup_enabled TINYINT(1) NOT NULL DEFAULT 1,
  pickup_advance_days   INT UNSIGNED NOT NULL DEFAULT 7,   -- 1..30
  pickup_slot_minutes   INT UNSIGNED NOT NULL DEFAULT 15,  -- 5..120
  pickup_slot_capacity  INT UNSIGNED NOT NULL DEFAULT 5,   -- 1..999
  pickup_min_lead_minutes INT UNSIGNED NOT NULL DEFAULT 30, -- 0..1440
  -- IANA 时区（PRD §19）
  timezone              VARCHAR(64) NOT NULL DEFAULT 'Asia/Shanghai',
  -- 每消费 1 元积分数（PRD §4.12）
  points_per_yuan       INT UNSIGNED NOT NULL DEFAULT 1,
  -- 新人券模板 ID（null=未配置）
  new_member_coupon_template_id BIGINT UNSIGNED NULL,
  -- 0 元订单走真实状态机但不调微信收款（PRD §4.5）
  created_at            DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at            DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  CONSTRAINT chk_store_pickup_minutes CHECK (pickup_minutes BETWEEN 1 AND 180),
  CONSTRAINT chk_store_advance_days CHECK (pickup_advance_days BETWEEN 1 AND 30),
  CONSTRAINT chk_store_slot_minutes CHECK (pickup_slot_minutes BETWEEN 5 AND 120),
  CONSTRAINT chk_store_slot_cap CHECK (pickup_slot_capacity BETWEEN 1 AND 999),
  CONSTRAINT chk_store_lead CHECK (pickup_min_lead_minutes BETWEEN 0 AND 1440)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- 小程序配置（每店唯一 AppID/品牌/主题/Logo）。
-- AppID 全局唯一（PRD §10.3 / §17.2）；AppSecret 只写不读，存加密密文（P6）。
CREATE TABLE miniprogram_config (
  store_id      BIGINT UNSIGNED NOT NULL,
  wechat_appid  VARCHAR(64) NOT NULL,
  brand_name    VARCHAR(100) NOT NULL DEFAULT '',
  theme_color   VARCHAR(16)  NOT NULL DEFAULT '',
  logo_url      VARCHAR(512) NOT NULL DEFAULT '',
  -- AES-GCM 加密的 AppSecret 密文 + nonce（P6 实现）
  app_secret_ciphertext BLOB NULL,
  app_secret_nonce      BLOB NULL,
  created_at    DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at    DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (store_id),
  UNIQUE KEY uq_mp_appid (wechat_appid)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- 顾客（门店范围）。
CREATE TABLE customers (
  id              BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  store_id        BIGINT UNSIGNED NOT NULL,
  wechat_openid   VARCHAR(64) NOT NULL,
  wechat_unionid  VARCHAR(64) NOT NULL DEFAULT '',
  -- 入会手机号：加密密文 + 不可逆 hash + 后四位 + 区号（PRD §4.12/§8.1）
  phone_encrypted BLOB NULL,
  phone_nonce     BLOB NULL,
  phone_hash      VARBINARY(64) NOT NULL DEFAULT '',  -- 门店范围唯一
  phone_last4     CHAR(4) NOT NULL DEFAULT '',
  phone_country_code VARCHAR(8) NOT NULL DEFAULT '',
  nickname        VARCHAR(64) NOT NULL DEFAULT '',
  avatar_url      VARCHAR(512) NOT NULL DEFAULT '',
  created_at      DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at      DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  UNIQUE KEY uq_customer_store_openid (store_id, wechat_openid),
  KEY idx_customer_phone_hash (store_id, phone_hash),
  CONSTRAINT fk_customer_store FOREIGN KEY (store_id) REFERENCES stores(id) ON DELETE RESTRICT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- 顾客会话（Bearer）。
CREATE TABLE customer_sessions (
  id              CHAR(32) NOT NULL,
  customer_id     BIGINT UNSIGNED NOT NULL,
  store_id        BIGINT UNSIGNED NOT NULL,
  -- 签名/校验用：最后一次 wx.login code 派生
  token_hash      VARBINARY(64) NOT NULL,
  expires_at      DATETIME(3) NOT NULL,
  created_at      DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  revoked_at      DATETIME(3) NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uq_cs_token_hash (token_hash),
  KEY idx_cs_customer (customer_id),
  CONSTRAINT fk_cs_customer FOREIGN KEY (customer_id) REFERENCES customers(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- 后台账号。
-- 登录账号全局唯一；密码 bcrypt；新密码 12..72（PRD §5.1）。
CREATE TABLE admin_users (
  id                BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  login             VARCHAR(64) NOT NULL,
  display_name      VARCHAR(100) NOT NULL DEFAULT '',
  password_hash     VARCHAR(100) NOT NULL,
  -- 启停
  enabled           TINYINT(1) NOT NULL DEFAULT 1,
  is_platform_admin TINYINT(1) NOT NULL DEFAULT 0,
  last_login_at     DATETIME(3) NULL,
  created_at        DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at        DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  UNIQUE KEY uq_admin_login (login)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- 后台会话（Cookie，Secure/HttpOnly/SameSite 由代码设置）。
-- 空闲 8h、绝对 7d、续期每 30min（PRD §5.1）。
CREATE TABLE admin_sessions (
  id              CHAR(32) NOT NULL,
  admin_user_id   BIGINT UNSIGNED NOT NULL,
  token_hash      VARBINARY(64) NOT NULL,
  -- 当前生效门店上下文（普通账号仅一个，但保留兼容）
  active_store_id BIGINT UNSIGNED NULL,
  ip_address      VARCHAR(64) NOT NULL DEFAULT '',
  user_agent      VARCHAR(255) NOT NULL DEFAULT '',
  issued_at       DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  last_seen_at    DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  expires_at      DATETIME(3) NOT NULL,
  revoked_at      DATETIME(3) NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uq_as_token_hash (token_hash),
  KEY idx_as_user (admin_user_id),
  CONSTRAINT fk_as_user FOREIGN KEY (admin_user_id) REFERENCES admin_users(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- 门店成员（账号 × 门店 × 角色）。普通账号最多归属一家门店（PRD §2.2）。
-- 角色：STAFF / MANAGER / OWNER；门店唯一店主（PRD §3/§10.4）。
CREATE TABLE shop_members (
  id            BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  store_id      BIGINT UNSIGNED NOT NULL,
  admin_user_id BIGINT UNSIGNED NOT NULL,
  role          VARCHAR(16) NOT NULL,
  created_at    DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at    DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  UNIQUE KEY uq_member_store_user (store_id, admin_user_id),
  KEY idx_member_user (admin_user_id),
  CONSTRAINT fk_member_store FOREIGN KEY (store_id) REFERENCES stores(id) ON DELETE RESTRICT,
  CONSTRAINT fk_member_user FOREIGN KEY (admin_user_id) REFERENCES admin_users(id) ON DELETE CASCADE,
  CONSTRAINT chk_member_role CHECK (role IN ('STAFF','MANAGER','OWNER'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
