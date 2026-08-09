// Package storage 提供数据库和 Redis 连接助手。
package storage

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
	"github.com/redis/go-redis/v9"
)

// OpenMySQL 打开并 ping 一个 sqlx DB (parseTime 必须在 DSN 中设置)。
func OpenMySQL(ctx context.Context, dsn string) (*sqlx.DB, error) {
	if dsn == "" {
		return nil, fmt.Errorf("empty database DSN")
	}
	db, err := sqlx.ConnectContext(ctx, "mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("connect mysql: %w", err)
	}
	// 合理的默认连接池参数；单门店目标 500 并发（PRD §19）。
	db.SetMaxOpenConns(50)
	db.SetMaxIdleConns(10)
	db.SetConnMaxIdleTime(30 * time.Minute)
	return db, nil
}

// OpenRedis 解析一个 redis:// URL 并返回一个已 ping 过的客户端。
func OpenRedis(ctx context.Context, addrOrURL string) (*redis.Client, error) {
	opts, err := redis.ParseURL(addrOrURL)
	if err != nil {
		// 回退：视为原始地址。
		opts = &redis.Options{Addr: addrOrURL}
	}
	rdb := redis.NewClient(opts)
	if err := rdb.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("ping redis: %w", err)
	}
	return rdb, nil
}

// SQLDB 暴露用于 ping 的底层 *sql.DB（在尚未引入 sqlx 仓库的地方很有用）。
func SQLDB(db *sqlx.DB) *sql.DB { return db.DB }
