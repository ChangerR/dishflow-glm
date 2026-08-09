package authn

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/dishflow/zshop/internal/httpx"
	"github.com/dishflow/zshop/internal/security"
)

// ipFromRequest 提取客户端 IP，仅信任 X-Forwarded-For 当来自可信代理（由调用方保证）。
// 这里简单取 RemoteAddr；可信代理处理在外层。
func ipFromRequest(r *http.Request) string {
	if r == nil {
		return ""
	}
	host := r.RemoteAddr
	for i := len(host) - 1; i >= 0; i-- {
		if host[i] == ':' {
			return host[:i]
		}
	}
	return host
}

// Middleware 校验管理会话，并把账号与（若提供 X-Store-Id）成员关系注入 ctx。
// 续期：距 last_seen 超过 30min 才刷新（避免每次请求写库，PRD §5.1）。
func Middleware(store *Store, idleTTL, absTTL, renewSlack time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := ReadSessionCookie(r)
			if token == "" {
				Unauthorized(w, r)
				return
			}
			sess, err := store.GetSessionByToken(r.Context(), security.HashToken(token))
			if err != nil {
				Unauthorized(w, r)
				return
			}
			now := time.Now().UTC()
			if sess.ExpiresAt.Before(now) || (sess.RevokedAt.Valid) {
				Unauthorized(w, r)
				return
			}
			user, err := store.GetAdminUser(r.Context(), sess.AdminUserID)
			if err != nil || !user.Enabled {
				Unauthorized(w, r)
				return
			}

			// 活跃续期：超过 renewSlack 未续期则刷新（PRD §5.1）。
			if now.Sub(sess.LastSeenAt) >= renewSlack {
				newExpiry := earliest(now.Add(idleTTL), sess.IssuedAt.Add(absTTL))
				if newExpiry.After(sess.ExpiresAt) {
					_ = store.RenewSession(r.Context(), sess.ID, newExpiry)
					SetSessionCookie(w, SessionCookieName, token, newExpiry, r.TLS != nil)
				}
			}

			ctx := WithAdminUser(r.Context(), user)

			// 若携带 X-Store-Id，校验成员关系（PRD §2.2）。
			if hid := r.Header.Get("X-Store-Id"); hid != "" {
				storeID, err := strconv.ParseInt(hid, 10, 64)
				if err != nil || storeID <= 0 {
					httpx.WriteError(w, r, httpx.New(httpx.CodeBadRequest, http.StatusBadRequest, "invalid X-Store-Id"))
					return
				}
				m, err := store.GetMembership(r.Context(), user.ID, storeID)
				if err != nil {
					if errors.Is(err, sql.ErrNoRows) {
						Forbidden(w, r)
						return
					}
					httpx.WriteError(w, r, err)
					return
				}
				ctx = WithMembership(ctx, m)
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequirePlatformAdmin 要求账号为平台超管（PRD §3.5）。
func RequirePlatformAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, ok := AdminUserFrom(r.Context())
		if !ok || !u.IsPlatformAdmin {
			Forbidden(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequireMember 要求账号在某门店有成员关系（任意角色）。
func RequireMember(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := MembershipFrom(r.Context()); !ok {
			Forbidden(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequireRole 要求当前成员角色不低于 min（PRD §3）。
func RequireRole(min Role) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			m, ok := MembershipFrom(r.Context())
			if !ok || !m.Role.AtLeast(min) {
				Forbidden(w, r)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// CurrentStoreID 从 ctx 取当前门店 ID（必须有成员关系）。
func CurrentStoreID(ctx context.Context) (int64, bool) {
	m, ok := MembershipFrom(ctx)
	if !ok {
		return 0, false
	}
	return m.StoreID, true
}
