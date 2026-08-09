// Package httpx contains shared HTTP utilities: the unified error envelope,
// request-id handling, paging, and response helpers (PRD §16).
//
// 约定：
//   - 业务 API 前缀 /api/v1；JSON 使用 snake_case。
//   - 成功直接返回业务对象或 {items,next_cursor,total}。
//   - 错误统一 {code,message,request_id,details}。
//   - 所有金额 int64 分；时间用 RFC3339。
package httpx

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"regexp"
)

// ErrorCode 是可操作的错误码字符串（PRD §16）。
type ErrorCode string

const (
	CodeInternal            ErrorCode = "INTERNAL"
	CodeBadRequest          ErrorCode = "BAD_REQUEST"
	CodeUnauthorized        ErrorCode = "UNAUTHORIZED"
	CodeForbidden           ErrorCode = "FORBIDDEN"
	CodeNotFound            ErrorCode = "NOT_FOUND"
	CodeConflict            ErrorCode = "CONFLICT"
	CodeStateConflict       ErrorCode = "STATE_CONFLICT"
	CodeRateLimited         ErrorCode = "RATE_LIMITED"
	// 业务可操作错误码（PRD §16 末尾示例）
	CodeQuoteExpired        ErrorCode = "QUOTE_EXPIRED"
	CodeQuoteMismatch       ErrorCode = "QUOTE_MISMATCH"
	CodeTableNotFound       ErrorCode = "TABLE_NOT_FOUND"
	CodeTableDisabled       ErrorCode = "TABLE_DISABLED"
	CodePickupSlotFull      ErrorCode = "PICKUP_SLOT_FULL"
	CodePickupTimeInvalid   ErrorCode = "PICKUP_TIME_INVALID"
	CodePaymentUnavailable  ErrorCode = "PAYMENT_UNAVAILABLE"
	CodeRefundConflict      ErrorCode = "REFUND_CONFLICT"
	CodeInsufficientPoints  ErrorCode = "INSUFFICIENT_POINTS"
	CodeWechatAppidConflict ErrorCode = "WECHAT_APPID_CONFLICT"
)

// Error 实现了 error，并带有可在 API 响应中呈现的 code/details。
type Error struct {
	Code    ErrorCode   `json:"code"`
	Message string      `json:"message"`
	Status  int         `json:"-"`
	Details interface{} `json:"details,omitempty"`
}

func (e *Error) Error() string {
	return string(e.Code) + ": " + e.Message
}

// New 返回带有给定 HTTP 状态的 *Error。
func New(code ErrorCode, status int, msg string) *Error {
	return &Error{Code: code, Message: msg, Status: status}
}

// WithDetails 附加任意 details（不会泄露敏感信息）。
func (e *Error) WithDetails(d interface{}) *Error {
	e.Details = d
	return e
}

// errorResponse 是 {code,message,request_id,details} 包络。
type errorResponse struct {
	Code      ErrorCode   `json:"code"`
	Message   string      `json:"message"`
	RequestID string      `json:"request_id"`
	Details   interface{} `json:"details,omitempty"`
}

// WriteError 写入 *Error（或通用内部错误），使用统一包络。
func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	he, ok := err.(*Error)
	if !ok {
		he = New(CodeInternal, http.StatusInternalServerError, "internal error")
	}
	status := he.Status
	if status == 0 {
		status = http.StatusInternalServerError
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	// 敏感响应禁止缓存（PRD §18）。
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorResponse{
		Code:      he.Code,
		Message:   he.Message,
		RequestID: RequestIDFrom(r.Context()),
		Details:   he.Details,
	})
}

// Page 是标准列表响应 {items,next_cursor,total}（PRD §16）。
type Page[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"next_cursor,omitempty"`
	Total      *int64 `json:"total,omitempty"`
}

// WriteJSON 会将带有 snake_case-friendly 编码的 v 作为 JSON 写入。
func WriteJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// DecodeJSON 会解码请求体并在错误时返回 BAD_REQUEST。
func DecodeJSON(r *http.Request, dst interface{}) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return New(CodeBadRequest, http.StatusBadRequest, "invalid JSON: "+err.Error())
	}
	return nil
}

// ── request id ────────────────────────────────────────────────────────
// 规则：只接受安全字符与有限长度的客户端 X-Request-Id；否则服务端生成
// 32 字符 hex（PRD §16）。

const (
	RequestIDHeader = "X-Request-Id"
	RequestIDMaxLen = 64
)

var requestIDSafeRe = regexp.MustCompile(`^[A-Za-z0-9._:\-]{1,64}$`)

// NewRequestID 生成 32 字符的 hex id。
func NewRequestID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// SanitizeRequestID 返回可接受的客户端 request-id 或 ""。
func SanitizeRequestID(s string) string {
	if s == "" || len(s) > RequestIDMaxLen || !requestIDSafeRe.MatchString(s) {
		return ""
	}
	return s
}

type ctxKey int

const requestIDCtxKey ctxKey = 1

// WithRequestID 将 request-id 存储在 ctx 中。
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDCtxKey, id)
}

// RequestIDFrom 从 ctx 中提取 request-id（缺失时返回 ""）。
func RequestIDFrom(ctx context.Context) string {
	if v, ok := ctx.Value(requestIDCtxKey).(string); ok {
		return v
	}
	return ""
}

// RequestIDMiddleware 读取或生成 request-id 并将其放在 ctx 和响应头中。
func RequestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := SanitizeRequestID(r.Header.Get(RequestIDHeader))
		if id == "" {
			id = NewRequestID()
		}
		w.Header().Set(RequestIDHeader, id)
		r = r.WithContext(WithRequestID(r.Context(), id))
		next.ServeHTTP(w, r)
	})
}
