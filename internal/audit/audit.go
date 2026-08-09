// Package audit 提供门店级与平台级审计日志写入（PRD §18）。
//
// 摘要不得原样倾倒含密钥的 JSON；调用方需先脱敏。
package audit

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// Store 审计日志存储。
type Store struct {
	db *sql.DB
}

// NewStore 创建审计存储。
func NewStore(db *sql.DB) *Store { return &Store{db: db} }

// RecordStore 记录门店级审计。
// actorUserID 为操作的后台账号 ID；summary 已脱敏。
func (s *Store) RecordStore(ctx context.Context, storeID, actorUserID int64, action, resourceType, resourceID, summary, requestID string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO audit_logs (store_id, platform_scope, actor_type, actor_admin_user_id, action, resource_type, resource_id, summary, request_id)
		 VALUES (?, 0, 'ADMIN', ?, ?, ?, ?, ?, ?)`,
		storeID, actorUserID, action, resourceType, resourceID, sanitize(summary), requestID)
	return err
}

// RecordSystem 记录系统/Worker 触发的审计（无操作人）。
func (s *Store) RecordSystem(ctx context.Context, storeID int64, action, resourceType, resourceID, summary string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO audit_logs (store_id, platform_scope, actor_type, action, resource_type, resource_id, summary)
		 VALUES (?, 0, 'SYSTEM', ?, ?, ?, ?)`,
		storeID, action, resourceType, resourceID, sanitize(summary))
	return err
}

// AuditEntry 表示一条审计记录。
type AuditEntry struct {
	ID           int64
	StoreID      sql.NullInt64
	PlatformScope bool
	ActorType    string
	ActorUserID  sql.NullInt64
	Action       string
	ResourceType string
	ResourceID   string
	Summary      string
	RequestID    string
	CreatedAt    string
}

// ListStore 列出门店审计（倒序，分页）。
func (s *Store) ListStore(ctx context.Context, storeID int64, limit, offset int) ([]AuditEntry, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, store_id, platform_scope, actor_type, actor_admin_user_id, action, resource_type, resource_id, summary, request_id, created_at
		 FROM audit_logs WHERE store_id = ? ORDER BY id DESC LIMIT ? OFFSET ?`, storeID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditEntry
	for rows.Next() {
		var e AuditEntry
		if err := rows.Scan(&e.ID, &e.StoreID, &e.PlatformScope, &e.ActorType, &e.ActorUserID, &e.Action,
			&e.ResourceType, &e.ResourceID, &e.Summary, &e.RequestID, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// sanitize 防止摘要中出现密钥相关字段（简单启发式）。
func sanitize(s string) string {
	if len(s) > 500 {
		s = s[:500]
	}
	low := strings.ToLower(s)
	for _, kw := range []string{"secret", "private_key", "apiv3", "password", "token"} {
		if strings.Contains(low, kw) {
			// 不丢弃整条，但提示可能含敏感内容——调用方应主动脱敏。
			return fmt.Sprintf("[redacted-suspect] %s", s)
		}
	}
	return s
}
