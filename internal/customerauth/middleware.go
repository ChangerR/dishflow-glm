package customerauth

import (
	"context"
	"net/http"
	"strings"

	"github.com/dishflow/zshop/internal/httpx"
)

type bearerCtxKey int

const customerSessionKey bearerCtxKey = 1

// BearerMiddleware 校验顾客 Bearer token（PRD §4.1/§16）。
// 格式：Authorization: Bearer <token>。
func BearerMiddleware(store *Store) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			auth := r.Header.Get("Authorization")
			if !strings.HasPrefix(auth, "Bearer ") {
				httpx.WriteError(w, r, httpx.New(httpx.CodeUnauthorized, http.StatusUnauthorized, "missing bearer token"))
				return
			}
			token := strings.TrimPrefix(auth, "Bearer ")
			sess, err := store.VerifyToken(r.Context(), token)
			if err != nil {
				httpx.WriteError(w, r, httpx.New(httpx.CodeUnauthorized, http.StatusUnauthorized, "invalid or expired session"))
				return
			}
			ctx := context.WithValue(r.Context(), customerSessionKey, sess)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// SessionFrom 取 ctx 中的顾客会话。
func SessionFrom(ctx context.Context) (Session, bool) {
	s, ok := ctx.Value(customerSessionKey).(Session)
	return s, ok
}
