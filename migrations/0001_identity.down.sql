-- 0001_identity.down.sql
-- 按依赖逆序回滚。
DROP TABLE IF EXISTS shop_members;
DROP TABLE IF EXISTS admin_sessions;
DROP TABLE IF EXISTS admin_users;
DROP TABLE IF EXISTS customer_sessions;
DROP TABLE IF EXISTS customers;
DROP TABLE IF EXISTS miniprogram_config;
DROP TABLE IF EXISTS stores;
