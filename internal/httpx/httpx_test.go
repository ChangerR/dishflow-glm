package httpx

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewRequestID_HexLen(t *testing.T) {
	id := NewRequestID()
	if len(id) != 32 {
		t.Fatalf("request id len = %d, want 32", len(id))
	}
	for _, c := range id {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Fatalf("request id %q contains non-hex char", id)
		}
	}
}

func TestSanitizeRequestID(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"abc123_X-Y.0:1", "abc123_X-Y.0:1"},
		{"a b", ""},            // 空格非法
		{"<script>", ""},       // 非法
		{string(make([]byte, 65)), ""}, // 超长（但全是零字节，正则也不匹配）
		{"a:b-c.d_e", "a:b-c.d_e"},
	}
	for _, c := range cases {
		if got := SanitizeRequestID(c.in); got != c.want {
			t.Errorf("SanitizeRequestID(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestRequestIDMiddleware_GeneratesWhenAbsent(t *testing.T) {
	called := false
	h := RequestIDMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if id := RequestIDFrom(r.Context()); id == "" {
			t.Fatal("expected generated request id in context")
		}
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health/live", nil)
	h.ServeHTTP(rec, req)
	if !called {
		t.Fatal("handler not called")
	}
	if rec.Header().Get(RequestIDHeader) == "" {
		t.Fatal("response missing X-Request-Id header")
	}
}

func TestRequestIDMiddleware_PreservesClientID(t *testing.T) {
	const cid = "client-req-123"
	h := RequestIDMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if id := RequestIDFrom(r.Context()); id != cid {
			t.Fatalf("got %q, want %q", id, cid)
		}
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(RequestIDHeader, cid)
	h.ServeHTTP(rec, req)
	if got := rec.Header().Get(RequestIDHeader); got != cid {
		t.Fatalf("echo header = %q, want %q", got, cid)
	}
}

func TestRequestIDMiddleware_RejectsUnsafeClientID(t *testing.T) {
	h := RequestIDMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := RequestIDFrom(r.Context())
		// 仍应有服务端生成的 id，而不是原样保留不安全输入。
		if id == "bad id with space" {
			t.Fatal("unsafe client id leaked into context")
		}
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(RequestIDHeader, "bad id with space")
	h.ServeHTTP(rec, req)
}

func TestWriteError_Envelope(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = req.WithContext(WithRequestID(context.Background(), "rid-1"))

	WriteError(rec, req, New(CodeStateConflict, http.StatusConflict, "version mismatch"))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusConflict)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Fatalf("content-type = %q", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("cache-control = %q, want no-store", cc)
	}
	var body errorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Code != CodeStateConflict {
		t.Fatalf("code = %q", body.Code)
	}
	if body.RequestID != "rid-1" {
		t.Fatalf("request_id = %q", body.RequestID)
	}
	if body.Message != "version mismatch" {
		t.Fatalf("message = %q", body.Message)
	}
}

func TestWriteError_FallsBackToInternal(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	WriteError(rec, req, errPlain("boom"))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	var body errorResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body.Code != CodeInternal {
		t.Fatalf("code = %q, want INTERNAL", body.Code)
	}
}

type errPlain string

func (e errPlain) Error() string { return string(e) }
