// Package web 提供跨域 HTTP 辅助：分页参数解析、cursor 解码、JSON 写出。
package web

import (
	"encoding/base64"
	"net/http"
	"strconv"
	"strings"

	"github.com/dishflow/zshop/internal/httpx"
)

// PageParams 解析后的分页参数。
type PageParams struct {
	Limit  int
	Cursor string
	Offset int
}

// DefaultLimit 默认每页条数。
const DefaultLimit = 20

// ParsePage 从查询串解析分页（limit/cursor）。
func ParsePage(r *http.Request) PageParams {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > 100 {
		limit = DefaultLimit
	}
	return PageParams{Limit: limit, Cursor: q.Get("cursor")}
}

// EncodeCursor 把原始 cursor 字符串编码为对外不透明的 base64。
func EncodeCursor(raw string) string {
	if raw == "" {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// DecodeCursor 解码对外 cursor。
func DecodeCursor(s string) (string, bool) {
	if s == "" {
		return "", true
	}
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return "", false
	}
	return string(b), true
}

// WriteList 写出标准列表响应 {items,next_cursor}（PRD §16）。
func WriteList(w http.ResponseWriter, items any, nextCursor string) {
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"items":       items,
		"next_cursor": EncodeCursor(nextCursor),
	})
}

// TrimTitle 清理字符串边界空白并限制长度。
func TrimTitle(s string, max int) string {
	s = strings.TrimSpace(s)
	if max > 0 && len(s) > max {
		s = s[:max]
	}
	return s
}
