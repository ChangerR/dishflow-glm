-- 0010_reliability.down.sql
DROP TABLE IF EXISTS audit_logs;
DROP TABLE IF EXISTS outbox;
DROP TABLE IF EXISTS webhook_events;
DROP TABLE IF EXISTS idempotency_keys;
