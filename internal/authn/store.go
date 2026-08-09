package authn

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/dishflow/zshop/internal/security"
)

// Store 提供账号、会话与成员关系的持久化。
type Store struct {
	db *sql.DB
}

// NewStore 创建认证存储。
func NewStore(db *sql.DB) *Store { return &Store{db: db} }

// GetAdminUserByLogin 按标准化 login 查账号。
func (s *Store) GetAdminUserByLogin(ctx context.Context, login string) (AdminUser, error) {
	var u AdminUser
	err := s.db.QueryRowContext(ctx,
		`SELECT id, login, display_name, password_hash, enabled, is_platform_admin, last_login_at
		 FROM admin_users WHERE login = ?`, NormalizeLogin(login)).
		Scan(&u.ID, &u.Login, &u.DisplayName, &u.PasswordHash, &u.Enabled, &u.IsPlatformAdmin, &u.LastLoginAt)
	if errors.Is(err, sql.ErrNoRows) {
		return AdminUser{}, sql.ErrNoRows
	}
	return u, err
}

// GetAdminUser 按 ID 查账号。
func (s *Store) GetAdminUser(ctx context.Context, id int64) (AdminUser, error) {
	var u AdminUser
	err := s.db.QueryRowContext(ctx,
		`SELECT id, login, display_name, password_hash, enabled, is_platform_admin, last_login_at
		 FROM admin_users WHERE id = ?`, id).
		Scan(&u.ID, &u.Login, &u.DisplayName, &u.PasswordHash, &u.Enabled, &u.IsPlatformAdmin, &u.LastLoginAt)
	if errors.Is(err, sql.ErrNoRows) {
		return AdminUser{}, sql.ErrNoRows
	}
	return u, err
}

// TouchLastLogin 更新最后登录时间。
func (s *Store) TouchLastLogin(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE admin_users SET last_login_at = UTC_TIMESTAMP(3) WHERE id = ?`, id)
	return err
}

// CreateSession 插入一条会话。
func (s *Store) CreateSession(ctx context.Context, sess Session) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO admin_sessions (id, admin_user_id, token_hash, active_store_id, ip_address, user_agent, issued_at, last_seen_at, expires_at, revoked_at)
		 VALUES (?,?,?,?,?,?,?,?,?,NULL)`,
		sess.ID, sess.AdminUserID, sess.TokenHash, sess.ActiveStoreID, sess.IPAddress, sess.UserAgent,
		sess.IssuedAt, sess.LastSeenAt, sess.ExpiresAt)
	return err
}

// GetSessionByToken 按 token 哈希查未撤销会话。
func (s *Store) GetSessionByToken(ctx context.Context, tokenHash []byte) (Session, error) {
	var sess Session
	err := s.db.QueryRowContext(ctx,
		`SELECT id, admin_user_id, token_hash, active_store_id, ip_address, user_agent, issued_at, last_seen_at, expires_at, revoked_at
		 FROM admin_sessions WHERE token_hash = ? AND revoked_at IS NULL LIMIT 1`, tokenHash).
		Scan(&sess.ID, &sess.AdminUserID, &sess.TokenHash, &sess.ActiveStoreID, &sess.IPAddress,
			&sess.UserAgent, &sess.IssuedAt, &sess.LastSeenAt, &sess.ExpiresAt, &sess.RevokedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, sql.ErrNoRows
	}
	return sess, err
}

// RenewSession 更新最后活动时间与过期时间（活跃续期）。
func (s *Store) RenewSession(ctx context.Context, id string, idleExpiry time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE admin_sessions SET last_seen_at = UTC_TIMESTAMP(3), expires_at = ? WHERE id = ? AND revoked_at IS NULL`,
		idleExpiry, id)
	return err
}

// RevokeSession 撤销会话（幂等）。
func (s *Store) RevokeSession(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE admin_sessions SET revoked_at = UTC_TIMESTAMP(3) WHERE id = ? AND revoked_at IS NULL`, id)
	return err
}

// GetMembership 查账号在某门店的成员关系（用于 X-Store-Id 校验）。
func (s *Store) GetMembership(ctx context.Context, adminUserID, storeID int64) (Membership, error) {
	var m Membership
	var role string
	err := s.db.QueryRowContext(ctx,
		`SELECT id, store_id, admin_user_id, role FROM shop_members WHERE admin_user_id = ? AND store_id = ?`,
		adminUserID, storeID).
		Scan(&m.ID, &m.StoreID, &m.AdminUserID, &role)
	if errors.Is(err, sql.ErrNoRows) {
		return Membership{}, sql.ErrNoRows
	}
	m.Role = Role(role)
	return m, err
}

// ListMemberships 查账号的全部成员关系（普通账号应最多一条，PRD §2.2）。
func (s *Store) ListMemberships(ctx context.Context, adminUserID int64) ([]Membership, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, store_id, admin_user_id, role FROM shop_members WHERE admin_user_id = ?`, adminUserID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Membership
	for rows.Next() {
		var m Membership
		var role string
		if err := rows.Scan(&m.ID, &m.StoreID, &m.AdminUserID, &role); err != nil {
			return nil, err
		}
		m.Role = Role(role)
		out = append(out, m)
	}
	return out, rows.Err()
}

// 计数器记录，用于登录限速（PRD §5.1）。简单起见用内存窗口计数。
// 注意：多副本部署时需替换为 Redis；单门店目标足够。
type loginCounter struct {
	window time.Duration
	limit  int
	hits   map[string][]time.Time
}

func newLoginCounter(limit int, window time.Duration) *loginCounter {
	return &loginCounter{window: window, limit: limit, hits: map[string][]time.Time{}}
}

// Allow 返回 key 是否仍在限速窗口内允许；并记录本次命中。
func (c *loginCounter) Allow(key string, now time.Time) bool {
	cutoff := now.Add(-c.window)
	hits := c.hits[key]
	// 丢弃过期。
	keep := hits[:0]
	for _, t := range hits {
		if t.After(cutoff) {
			keep = append(keep, t)
		}
	}
	if len(keep) >= c.limit {
		c.hits[key] = keep
		return false
	}
	keep = append(keep, now)
	c.hits[key] = keep
	return true
}

// RateLimiter 组合 IP 与账号限速。
type RateLimiter struct {
	byIP      *loginCounter
	byAccount *loginCounter
}

// NewRateLimiter 构造默认限速器。
func NewRateLimiter() *RateLimiter {
	return &RateLimiter{
		byIP:      newLoginCounter(loginIPLimitPerMin, time.Minute),
		byAccount: newLoginCounter(loginAccountLimitMin, time.Minute),
	}
}

// AllowLogin 检查 IP 与账号是否均未超限。
func (rl *RateLimiter) AllowLogin(ip, account string, now time.Time) bool {
	if !rl.byIP.Allow(ip, now) {
		return false
	}
	if account != "" && !rl.byAccount.Allow(account, now) {
		return false
	}
	return true
}

// LoginResult 描述一次登录尝试结果。
type LoginResult struct {
	Session   Session
	RateLimited bool
	BadCredentials bool
	Disabled      bool
}

// IssueAdminSession 创建会话并返回原始 token（调用方写 Cookie）。
// 普通账号（非平台超管）如果恰好归属一家门店，自动设置 active_store_id（PRD §5.2）。
func (s *Store) IssueAdminSession(ctx context.Context, user AdminUser, ip, ua string, idleTTL, absTTL time.Duration) (string, Session, error) {
	token, err := security.NewToken(32)
	if err != nil {
		return "", Session{}, err
	}
	now := time.Now().UTC()
	sess := Session{
		ID:          mustToken(16),
		AdminUserID: user.ID,
		TokenHash:   security.HashToken(token),
		IPAddress:   ip,
		UserAgent:   ua,
		IssuedAt:    now,
		LastSeenAt:  now,
		// 绝对过期 = min(now+idle, issued+abs)
		ExpiresAt: earliest(now.Add(idleTTL), now.Add(absTTL)),
	}
	// 普通账号自动绑定唯一门店（PRD §5.2/§2.2）。
	if !user.IsPlatformAdmin {
		members, err := s.ListMemberships(ctx, user.ID)
		if err == nil && len(members) == 1 {
			sess.ActiveStoreID = sql.NullInt64{Int64: members[0].StoreID, Valid: true}
		}
	}
	if err := s.CreateSession(ctx, sess); err != nil {
		return "", Session{}, err
	}
	return token, sess, nil
}

func earliest(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

func mustToken(n int) string {
	id, err := security.NewHexID(n)
	if err != nil {
		panic(fmt.Sprintf("generate id: %v", err))
	}
	return id
}
