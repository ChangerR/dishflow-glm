// Package main 是 `migrate` 子命令：执行纯 SQL 迁移。
//
// 设计：migrations/ 下文件命名 NN_name.up.sql / NN_name.down.sql。
// schema_migrations 表记录已应用版本（顺序号）。
//
// 用法：
//   migrate up            # 应用到最新
//   migrate down N        # 回滚最近 N 个
//   migrate status        # 显示已应用版本
//   migrate redo          # 回滚最后一个再重新应用
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dishflow/zshop/internal/config"
	_ "github.com/go-sql-driver/mysql"
)

func main() {
	if err := run(); err != nil {
		slog.Error("migrate failed", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if len(os.Args) < 2 {
		return usage()
	}
	cmd := os.Args[1]

	db, err := sql.Open("mysql", cfg.DatabaseDSN)
	if err != nil {
		return fmt.Errorf("open mysql: %w", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := ensureMigrationsTable(ctx, db); err != nil {
		return fmt.Errorf("ensure migrations table: %w", err)
	}

	migs, err := loadMigrations("migrations")
	if err != nil {
		return err
	}

	switch cmd {
	case "up":
		return migrateUp(ctx, db, migs)
	case "down":
		n := 1
		if len(os.Args) >= 3 {
			if n, err = strconv.Atoi(os.Args[2]); err != nil {
				return fmt.Errorf("invalid count: %s", os.Args[2])
			}
		}
		return migrateDown(ctx, db, migs, n)
	case "redo":
		if err := migrateDown(ctx, db, migs, 1); err != nil {
			return err
		}
		return migrateUp(ctx, db, migs)
	case "status":
		return migrateStatus(ctx, db, migs)
	default:
		return usage()
	}
}

func usage() error {
	fmt.Fprintln(os.Stderr, "usage: migrate [up|down N|redo|status]")
	return errors.New("invalid command")
}

// migration 表示一个带版本号的迁移对。
type migration struct {
	version int
	name    string
	up      string
	down    string
}

func loadMigrations(dir string) ([]migration, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read migrations dir %s: %w", dir, err)
	}
	byVer := map[int]*migration{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".sql") {
			continue
		}
		ver, suffix, ok := splitVersion(name)
		if !ok {
			continue
		}
		body, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		m, exists := byVer[ver]
		if !exists {
			m = &migration{version: ver}
			byVer[ver] = m
		}
		m.name = suffix
		switch {
		case strings.HasSuffix(name, ".up.sql"):
			m.up = string(body)
		case strings.HasSuffix(name, ".down.sql"):
			m.down = string(body)
		}
	}
	out := make([]migration, 0, len(byVer))
	for _, m := range byVer {
		out = append(out, *m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	return out, nil
}

// splitVersion 解析 "0001_init.up.sql" -> (1, "init", true)。
func splitVersion(name string) (int, string, bool) {
	idx := strings.Index(name, "_")
	if idx < 0 {
		return 0, "", false
	}
	ver, err := strconv.Atoi(name[:idx])
	if err != nil {
		return 0, "", false
	}
	rest := name[idx+1:]
	rest = strings.TrimSuffix(rest, ".up.sql")
	rest = strings.TrimSuffix(rest, ".down.sql")
	return ver, rest, true
}

func ensureMigrationsTable(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS schema_migrations (
  version BIGINT PRIMARY KEY,
  applied_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`)
	return err
}

func appliedVersions(ctx context.Context, db *sql.DB) (map[int]bool, error) {
	rows, err := db.QueryContext(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	applied := map[int]bool{}
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		applied[v] = true
	}
	return applied, rows.Err()
}

func migrateUp(ctx context.Context, db *sql.DB, migs []migration) error {
	applied, err := appliedVersions(ctx, db)
	if err != nil {
		return err
	}
	for _, m := range migs {
		if applied[m.version] {
			continue
		}
		if err := execInTx(ctx, db, m.up); err != nil {
			return fmt.Errorf("apply %04d %s: %w", m.version, m.name, err)
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO schema_migrations (version) VALUES (?)`, m.version); err != nil {
			return fmt.Errorf("record %04d: %w", m.version, err)
		}
		fmt.Printf("applied %04d %s\n", m.version, m.name)
	}
	return nil
}

func migrateDown(ctx context.Context, db *sql.DB, migs []migration, n int) error {
	applied, err := appliedVersions(ctx, db)
	if err != nil {
		return err
	}
	// 逆序回滚已应用版本。
	for i := len(migs) - 1; i >= 0 && n > 0; i-- {
		m := migs[i]
		if !applied[m.version] {
			continue
		}
		if m.down == "" {
			return fmt.Errorf("no down migration for %04d %s", m.version, m.name)
		}
		if err := execInTx(ctx, db, m.down); err != nil {
			return fmt.Errorf("rollback %04d %s: %w", m.version, m.name, err)
		}
		if _, err := db.ExecContext(ctx, `DELETE FROM schema_migrations WHERE version = ?`, m.version); err != nil {
			return fmt.Errorf("unrecord %04d: %w", m.version, err)
		}
		fmt.Printf("rolled back %04d %s\n", m.version, m.name)
		n--
	}
	return nil
}

func migrateStatus(ctx context.Context, db *sql.DB, migs []migration) error {
	applied, err := appliedVersions(ctx, db)
	if err != nil {
		return err
	}
	for _, m := range migs {
		status := "pending"
		if applied[m.version] {
			status = "applied"
		}
		fmt.Printf("%04d %-30s %s\n", m.version, m.name, status)
	}
	return nil
}

func execInTx(ctx context.Context, db *sql.DB, script string) error {
	stmts := splitStatements(script)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	for _, s := range stmts {
		if _, err := tx.ExecContext(ctx, s); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// splitStatements 将 SQL 脚本按语句分隔符 ';' 切分，保留字符串字面量与注释。
// 适用于纯 DDL 迁移（不含存储过程）。
func splitStatements(script string) []string {
	var out []string
	var buf strings.Builder
	inSingle := false
	inDouble := false
	inLineComment := false
	inBlockComment := false
	for i := 0; i < len(script); i++ {
		c := script[i]
		if inLineComment {
			buf.WriteByte(c)
			if c == '\n' {
				inLineComment = false
			}
			continue
		}
		if inBlockComment {
			buf.WriteByte(c)
			if c == '*' && i+1 < len(script) && script[i+1] == '/' {
				buf.WriteByte('/')
				inBlockComment = false
				i++
			}
			continue
		}
		if inSingle {
			buf.WriteByte(c)
			if c == '\'' {
				inSingle = false
			}
			continue
		}
		if inDouble {
			buf.WriteByte(c)
			if c == '"' {
				inDouble = false
			}
			continue
		}
		switch c {
		case '\'':
			inSingle = true
			buf.WriteByte(c)
		case '"':
			inDouble = true
			buf.WriteByte(c)
		case '-':
			if i+1 < len(script) && script[i+1] == '-' {
				inLineComment = true
				buf.WriteByte(c)
				buf.WriteByte('-')
				i++
			} else {
				buf.WriteByte(c)
			}
		case '/':
			if i+1 < len(script) && script[i+1] == '*' {
				inBlockComment = true
				buf.WriteByte(c)
				buf.WriteByte('*')
				i++
			} else {
				buf.WriteByte(c)
			}
		case ';':
			stmt := strings.TrimSpace(buf.String())
			if stmt != "" {
				out = append(out, stmt)
			}
			buf.Reset()
		default:
			buf.WriteByte(c)
		}
	}
	if s := strings.TrimSpace(buf.String()); s != "" {
		out = append(out, s)
	}
	return out
}
