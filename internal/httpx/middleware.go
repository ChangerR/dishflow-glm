package httpx

import (
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"
)

// statusRecorder 捕获写入的 HTTP 状态以进行记录。
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// RecoverMiddleware 捕获 panic，记录日志（带有堆栈信息），并返回 INTERNAL 错误。
func RecoverMiddleware(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					log.Error("panic recovered",
						"request_id", RequestIDFrom(r.Context()),
						"method", r.Method,
						"path", r.URL.Path,
						"panic", rec,
						"stack", string(debug.Stack()),
					)
					WriteError(w, r, New(CodeInternal, http.StatusInternalServerError, "internal error"))
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// LoggingMiddleware 记录带有 request-id、状态码和持续时间的访问日志。
//
// 安全：仅记录方法/路径/状态码/持续时间，绝不记录 openid、手机号、
// Cookie/Bearer、支付密文或密钥（PRD §18）。
func LoggingMiddleware(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r)
			log.Info("http request",
				"request_id", RequestIDFrom(r.Context()),
				"method", r.Method,
				"path", r.URL.Path,
				"status", rec.status,
				"duration_ms", time.Since(start).Milliseconds(),
			)
		})
	}
}

// SecurityHeadersMiddleware 添加管理 API 响应所需的安全响应头（PRD §18）。
func SecurityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		// 大多数管理 API 响应对敏感数据禁用缓存。
		if r.URL.Path != "/health/live" && r.URL.Path != "/health/ready" {
			h.Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}
