// Package authn 实现管理后台会话（Cookie）、顾客会话（Bearer）和角色鉴权（PRD §5.1/§3）。
//
// 管理会话规则（PRD §5.1）：
//   - 账号密码登录，bcrypt 强哈希；新密码 12..72。
//   - 登录按客户端 IP + 标准化账号限速。
//   - Cookie：Secure、HttpOnly、SameSite=Lax；空闲 8h 过期、绝对最长 7d、
//     活跃续期最频繁每 30min 一次。
//   - 退出先撤销服务端会话再清 Cookie；过期 Cookie 退出幂等成功。
//   - 401 时统一回到登录页。
package authn

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/dishflow/zshop/internal/httpx"
)

// SessionCookieName 是管理会话 Cookie 名。
const SessionCookieName = "shop_session"

// 默认限速：同 IP 每分钟 20 次、同账号每分钟 10 次（PRD §5.1）。
const (
	loginIPLimitPerMin   = 20
	loginAccountLimitMin = 10
)

// Session 表示一条管理会话。
type Session struct {
	ID            string
	AdminUserID   int64
	TokenHash     []byte
	ActiveStoreID sql.NullInt64
	IPAddress     string
	UserAgent     string
	IssuedAt      time.Time
	LastSeenAt    time.Time
	ExpiresAt     time.Time
	RevokedAt     sql.NullTime
}

// AdminUser 表示后台账号。
type AdminUser struct {
	ID              int64
	Login           string
	DisplayName     string
	PasswordHash    string
	Enabled         bool
	IsPlatformAdmin bool
	LastLoginAt     sql.NullTime
}

// Membership 表示账号在某门店的成员关系与角色。
type Membership struct {
	ID         int64
	StoreID    int64
	AdminUserID int64
	Role       Role
}

// Role 是门店成员角色（PRD §3）。
type Role string

const (
	RoleStaff   Role = "STAFF"
	RoleManager Role = "MANAGER"
	RoleOwner   Role = "OWNER"
)

// AtLeast 判断角色是否不低于 r（权限层级 STAFF < MANAGER < OWNER）。
func (r Role) AtLeast(min Role) bool {
	return roleRank(r) >= roleRank(min)
}

func roleRank(r Role) int {
	switch r {
	case RoleOwner:
		return 3
	case RoleManager:
		return 2
	case RoleStaff:
		return 1
	default:
		return 0
	}
}

// SetSessionCookie 写入管理会话 Cookie。
// dev 模式下（非 https）Secure 由调用方根据配置关闭，便于本地。
func SetSessionCookie(w http.ResponseWriter, name, value string, expires time.Time, secure bool) {
	cookie := &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		Expires:  expires,
		MaxAge:   int(time.Until(expires).Seconds()),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	}
	http.SetCookie(w, cookie)
}

// ClearSessionCookie 清除会话 Cookie（幂等，PRD §5.1）。
func ClearSessionCookie(w http.ResponseWriter, name string) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

// ReadSessionCookie 读取请求中的会话 Cookie，缺失返回 ""。
func ReadSessionCookie(r *http.Request) string {
	c, err := r.Cookie(SessionCookieName)
	if err != nil {
		return ""
	}
	return c.Value
}

// ── context 注入 ──────────────────────────────────────────────────────

type ctxKey int

const (
	adminUserCtx ctxKey = iota
	membershipCtx
)

// WithAdminUser 把已认证账号放入 ctx。
func WithAdminUser(ctx context.Context, u AdminUser) context.Context {
	return context.WithValue(ctx, adminUserCtx, u)
}

// AdminUserFrom 取出 ctx 中的账号。
func AdminUserFrom(ctx context.Context) (AdminUser, bool) {
	u, ok := ctx.Value(adminUserCtx).(AdminUser)
	return u, ok
}

// WithMembership 把当前门店成员关系放入 ctx。
func WithMembership(ctx context.Context, m Membership) context.Context {
	return context.WithValue(ctx, membershipCtx, m)
}

// MembershipFrom 取出 ctx 中的成员关系。
func MembershipFrom(ctx context.Context) (Membership, bool) {
	m, ok := ctx.Value(membershipCtx).(Membership)
	return m, ok
}

// ── 错误 ──────────────────────────────────────────────────────────────

var (
	// ErrSessionMissing 表示请求未携带有效会话。
	ErrSessionMissing = errors.New("session missing or invalid")
)

// Unauthorized 返回 401 错误响应。
func Unauthorized(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, r, httpx.New(httpx.CodeUnauthorized, http.StatusUnauthorized, "authentication required"))
}

// Forbidden 返回 403 错误响应。
func Forbidden(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, r, httpx.New(httpx.CodeForbidden, http.StatusForbidden, "forbidden"))
}

// NormalizeLogin 标准化登录账号（去首尾空白 + 转小写）用于限速与唯一性。
func NormalizeLogin(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}
