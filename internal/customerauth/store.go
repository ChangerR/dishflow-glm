// Package customerauth 实现顾客会话（微信 wx.login code 换 Bearer，PRD §4.1）。
//
// 真实微信 code2session 需要 AppID/AppSecret（P6 安全中心）；本包在 dev/mock 模式
// 提供可注入的 CodeExchanger，便于测试与无密钥联调。生产必须 fail closed。
package customerauth

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/dishflow/zshop/internal/security"
)

// CodeExchanger 把 wx.login code 换成 openid（PRD §4.1.6）。
// 生产实现调用微信 code2session；mock 实现可固定返回。
type CodeExchanger interface {
	Exchange(ctx context.Context, storeID int64, appid, code string) (openid, unionid string, err error)
}

// MockExchanger 固定返回 openid（dev/mock 联调用）。
type MockExchanger struct{}

// Exchange 返回基于 code 的确定性 openid。
func (MockExchanger) Exchange(_ context.Context, _ int64, _, code string) (string, string, error) {
	if code == "" {
		return "", "", errors.New("empty code")
	}
	return "mock_" + code, "", nil
}

// Session 顾客会话。
type Session struct {
	ID         string
	CustomerID int64
	StoreID    int64
	TokenHash  []byte
	ExpiresAt  time.Time
	IssuedAt   time.Time
}

// Store 顾客身份持久化。
type Store struct {
	db       *sql.DB
	exchange CodeExchanger
	ttl      time.Duration
}

// NewStore 创建顾客身份存储。ttl 为会话有效期。
func NewStore(db *sql.DB, ex CodeExchanger, ttl time.Duration) *Store {
	if ex == nil {
		ex = MockExchanger{}
	}
	if ttl <= 0 {
		ttl = 7 * 24 * time.Hour
	}
	return &Store{db: db, exchange: ex, ttl: ttl}
}

// Customer 顾客。
type Customer struct {
	ID      int64
	StoreID int64
	OpenID  string
	UnionID string
}

// EnsureByOpenID 按 (store_id, openid) 取或建顾客，返回顾客 ID（PRD §4.1.5）。
func (s *Store) EnsureByOpenID(ctx context.Context, storeID int64, openid, unionid string) (int64, error) {
	if openid == "" {
		return 0, errors.New("empty openid")
	}
	// 先查。
	var id int64
	err := s.db.QueryRowContext(ctx,
		`SELECT id FROM customers WHERE store_id = ? AND wechat_openid = ?`, storeID, openid).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	// 插入。
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO customers (store_id, wechat_openid, wechat_unionid) VALUES (?,?,?)
		 ON DUPLICATE KEY UPDATE id = id`, storeID, openid, unionid)
	if err != nil {
		// 并发插入：再次查询。
		err2 := s.db.QueryRowContext(ctx,
			`SELECT id FROM customers WHERE store_id = ? AND wechat_openid = ?`, storeID, openid).Scan(&id)
		if err2 == nil {
			return id, nil
		}
		return 0, err
	}
	// 若 ON DUPLICATE 命中既有行，LastInsertId 可能不对；重查以确定。
	if lid, _ := res.LastInsertId(); lid > 0 {
		return lid, nil
	}
	return s.idByOpenID(ctx, storeID, openid)
}

func (s *Store) idByOpenID(ctx context.Context, storeID int64, openid string) (int64, error) {
	var id int64
	err := s.db.QueryRowContext(ctx,
		`SELECT id FROM customers WHERE store_id = ? AND wechat_openid = ?`, storeID, openid).Scan(&id)
	return id, err
}

// IssueSession 换 code → openid → 顾客 → 会话，返回原始 Bearer token。
func (s *Store) IssueSession(ctx context.Context, storeID int64, appid, code string) (string, Session, error) {
	openid, unionid, err := s.exchange.Exchange(ctx, storeID, appid, code)
	if err != nil {
		return "", Session{}, err
	}
	customerID, err := s.EnsureByOpenID(ctx, storeID, openid, unionid)
	if err != nil {
		return "", Session{}, err
	}
	token, err := security.NewToken(32)
	if err != nil {
		return "", Session{}, err
	}
	now := time.Now().UTC()
	sessID, err := security.NewHexID(16)
	if err != nil {
		return "", Session{}, err
	}
	sess := Session{
		ID: sessID, CustomerID: customerID, StoreID: storeID,
		TokenHash: security.HashToken(token), IssuedAt: now, ExpiresAt: now.Add(s.ttl),
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO customer_sessions (id, customer_id, store_id, token_hash, expires_at) VALUES (?,?,?,?,?)`,
		sess.ID, sess.CustomerID, sess.StoreID, sess.TokenHash, sess.ExpiresAt); err != nil {
		return "", Session{}, err
	}
	return token, sess, nil
}

// VerifyToken 校验 Bearer token，返回未过期会话。
func (s *Store) VerifyToken(ctx context.Context, token string) (Session, error) {
	if token == "" {
		return Session{}, errors.New("missing token")
	}
	var sess Session
	err := s.db.QueryRowContext(ctx,
		`SELECT id, customer_id, store_id, token_hash, expires_at, issued_at
		 FROM customer_sessions WHERE token_hash = ? AND revoked_at IS NULL LIMIT 1`,
		security.HashToken(token)).
		Scan(&sess.ID, &sess.CustomerID, &sess.StoreID, &sess.TokenHash, &sess.ExpiresAt, &sess.IssuedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Session{}, errors.New("invalid session")
		}
		return Session{}, err
	}
	if time.Now().UTC().After(sess.ExpiresAt) {
		return Session{}, errors.New("session expired")
	}
	return sess, nil
}

// GetCustomer 按 ID 取顾客。
func (s *Store) GetCustomer(ctx context.Context, id int64) (Customer, error) {
	var c Customer
	err := s.db.QueryRowContext(ctx,
		`SELECT id, store_id, wechat_openid, wechat_unionid FROM customers WHERE id = ?`, id).
		Scan(&c.ID, &c.StoreID, &c.OpenID, &c.UnionID)
	if errors.Is(err, sql.ErrNoRows) {
		return Customer{}, sql.ErrNoRows
	}
	return c, err
}
