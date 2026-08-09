// Package reliability 实现幂等键存储、收件箱/发件箱原语和 webhook 去重。
//
// P0 提供基于数据库（由 0001 migration 创建的 idempotency_keys 表）的
// Idempotency-Key 中间件。业务 handler 在后续阶段接入复用逻辑。
package reliability

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/dishflow/zshop/internal/httpx"
	"github.com/jmoiron/sqlx"
)

const idempotencyHeader = "Idempotency-Key"

// ErrConflict 表示相同的 key 被用于不同的请求体（PRD §16）。
var ErrConflict = errors.New("idempotency key used with different request")

// Record 表示一行 idempotency_keys 数据。
type Record struct {
	Key           string    `db:"idem_key"`
	Subject       string    `db:"subject"`
	RequestHash   string    `db:"request_hash"`
	StatusCode    int       `db:"status_code"`
	ResponseBody  []byte    `db:"response_body"`
	CreatedAt     time.Time `db:"created_at"`
}

// Store 在 idempotency_keys 之上实现幂等性记录读写。
type Store struct {
	db *sqlx.DB
}

// NewStore 创建一个 Store。
func NewStore(db *sqlx.DB) *Store { return &Store{db: db} }

// Get 返回给定 (subject, key) 的已存储记录，或 sql.ErrNoRows。
func (s *Store) Get(ctx context.Context, subject, key string) (Record, error) {
	var rec Record
	err := s.db.GetContext(ctx, &rec,
		`SELECT idem_key, subject, request_hash, status_code, response_body, created_at
		 FROM idempotency_keys WHERE subject = ? AND idem_key = ?`, subject, key)
	return rec, err
}

// Save 持久化一个已完成请求的响应。调用者必须已通过 INSERT 的唯一约束，
// 或在同事务内完成业务写入后再落本记录（取决于业务，后续阶段细化）。
func (s *Store) Save(ctx context.Context, rec Record) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO idempotency_keys (idem_key, subject, request_hash, status_code, response_body, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		rec.Key, rec.Subject, rec.RequestHash, rec.StatusCode, rec.ResponseBody, rec.CreatedAt)
	return err
}

// Middleware 尚不执行完整复用（这需要捕获响应字节，在 P1+ 由具体 handler 接入）。
// P0 它仅校验 Idempotency-Key 头格式并把 key 放入 context，供后续阶段使用。
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get(idempotencyHeader)
		if key != "" {
			if !validKey(key) {
				httpx.WriteError(w, r, httpx.New(httpx.CodeBadRequest, http.StatusBadRequest,
					"invalid Idempotency-Key"))
				return
			}
			ctx := WithKey(r.Context(), key)
			r = r.WithContext(ctx)
		}
		next.ServeHTTP(w, r)
	})
}

// validKey 限制为安全字符且长度 <= 100。
func validKey(s string) bool {
	if len(s) == 0 || len(s) > 100 {
		return false
	}
	for _, c := range s {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_', c == '.':
		default:
			return false
		}
	}
	return true
}

type ctxKey int

const keyCtx ctxKey = 1

// WithKey 将幂等键存储在 ctx 中。
func WithKey(ctx context.Context, key string) context.Context {
	return context.WithValue(ctx, keyCtx, key)
}

// FromContext 返回幂等键（如果有）。
func FromContext(ctx context.Context) string {
	if v, ok := ctx.Value(keyCtx).(string); ok {
		return v
	}
	return ""
}

// 保证 sql 包在 P0 仍被引用（Store 的查询返回 sql.ErrNoRows）。
var _ = sql.ErrNoRows
