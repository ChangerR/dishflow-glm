// Package admin 实现管理后台 HTTP 层：会话（登录/退出/当前）、平台与门店运营接口装配。
//
// 会话规则见 internal/authn。本包只负责 HTTP 编解码与调用 store。
package admin

import (
	"net/http"
	"time"

	"github.com/dishflow/zshop/internal/authn"
	"github.com/dishflow/zshop/internal/httpx"
	"github.com/dishflow/zshop/internal/security"
)

// SessionHandlers 管理会话相关 HTTP handler。
type SessionHandlers struct {
	store     *authn.Store
	limiter   *authn.RateLimiter
	idleTTL   time.Duration
	absTTL    time.Duration
	devSecure bool // dev 模式可设非 secure cookie
}

// NewSessionHandlers 构造会话 handler。
func NewSessionHandlers(store *authn.Store, limiter *authn.RateLimiter, idleTTL, absTTL time.Duration, devSecure bool) *SessionHandlers {
	return &SessionHandlers{store: store, limiter: limiter, idleTTL: idleTTL, absTTL: absTTL, devSecure: devSecure}
}

type loginRequest struct {
	Login    string `json:"login"`
	Password string `json:"password"`
}

type sessionUserResponse struct {
	ID              int64  `json:"id"`
	Login           string `json:"login"`
	DisplayName     string `json:"display_name"`
	IsPlatformAdmin bool   `json:"is_platform_admin"`
}

// Login POST /api/v1/admin/session
func (h *SessionHandlers) Login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	login := authn.NormalizeLogin(req.Login)
	ip := clientIP(r)

	// 限速（PRD §5.1）。即便账号不存在也消耗 IP 配额，避免枚举。
	if !h.limiter.AllowLogin(ip, login, time.Now()) {
		httpx.WriteError(w, r, httpx.New(httpx.CodeRateLimited, http.StatusTooManyRequests, "too many attempts"))
		return
	}

	user, err := h.store.GetAdminUserByLogin(r.Context(), login)
	if err != nil || !security.VerifyPassword(user.PasswordHash, req.Password) {
		// 统一错误，不区分账号/密码错误（避免枚举）。
		httpx.WriteError(w, r, httpx.New(httpx.CodeUnauthorized, http.StatusUnauthorized, "invalid login or password"))
		return
	}
	if !user.Enabled {
		httpx.WriteError(w, r, httpx.New(httpx.CodeForbidden, http.StatusForbidden, "account disabled"))
		return
	}

	token, _, err := h.store.IssueAdminSession(r.Context(), user, ip, r.UserAgent(), h.idleTTL, h.absTTL)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	_ = h.store.TouchLastLogin(r.Context(), user.ID)

	secure := r.TLS != nil || !h.devSecure
	authn.SetSessionCookie(w, authn.SessionCookieName, token, time.Now().UTC().Add(h.idleTTL), secure)
	httpx.WriteJSON(w, http.StatusOK, sessionUserResponse{
		ID: user.ID, Login: user.Login, DisplayName: user.DisplayName, IsPlatformAdmin: user.IsPlatformAdmin,
	})
}

// Logout DELETE /api/v1/admin/session（幂等，PRD §5.1）。
func (h *SessionHandlers) Logout(w http.ResponseWriter, r *http.Request) {
	token := authn.ReadSessionCookie(r)
	if token != "" {
		sess, err := h.store.GetSessionByToken(r.Context(), security.HashToken(token))
		if err == nil {
			_ = h.store.RevokeSession(r.Context(), sess.ID)
		}
	}
	authn.ClearSessionCookie(w, authn.SessionCookieName)
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// SessionInfo GET /api/v1/admin/session —— 当前账号、门店与角色上下文。
func (h *SessionHandlers) SessionInfo(w http.ResponseWriter, r *http.Request) {
	user, ok := authn.AdminUserFrom(r.Context())
	if !ok {
		authn.Unauthorized(w, r)
		return
	}
	resp := map[string]any{
		"id":               user.ID,
		"login":            user.Login,
		"display_name":     user.DisplayName,
		"is_platform_admin": user.IsPlatformAdmin,
	}
	if m, ok := authn.MembershipFrom(r.Context()); ok {
		resp["active_store_id"] = m.StoreID
		resp["role"] = string(m.Role)
	} else if !user.IsPlatformAdmin {
		// 未带 X-Store-Id：若普通账号仅归属一家门店，自动返回（PRD §5.2）。
		members, err := h.store.ListMemberships(r.Context(), user.ID)
		if err == nil && len(members) == 1 {
			resp["active_store_id"] = members[0].StoreID
			resp["role"] = string(members[0].Role)
		}
	}
	httpx.WriteJSON(w, http.StatusOK, resp)
}

// clientIP 取请求 IP（简化：RemoteAddr 去端口）。可信代理处理在外层。
func clientIP(r *http.Request) string {
	host := r.RemoteAddr
	for i := len(host) - 1; i >= 0; i-- {
		if host[i] == ':' {
			return host[:i]
		}
	}
	return host
}
