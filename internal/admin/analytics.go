package admin

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/dishflow/zshop/internal/analytics"
	"github.com/dishflow/zshop/internal/authn"
	"github.com/dishflow/zshop/internal/export"
	"github.com/dishflow/zshop/internal/httpx"
)

// AnalyticsHandlers 分析/导出 handler（PRD §9/§11）。
type AnalyticsHandlers struct {
	an  *analytics.Store
	exp *export.Store
}

// NewAnalyticsHandlers 构造。
func NewAnalyticsHandlers(an *analytics.Store, exp *export.Store) *AnalyticsHandlers {
	return &AnalyticsHandlers{an: an, exp: exp}
}

func parseRange(r *http.Request) (time.Time, time.Time, error) {
	startStr := r.URL.Query().Get("start")
	endStr := r.URL.Query().Get("end")
	if startStr == "" || endStr == "" {
		// 默认最近 7 天。
		end := time.Now().UTC()
		return end.AddDate(0, 0, -7), end, nil
	}
	start, err := time.Parse(time.RFC3339, startStr)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	end, err := time.Parse(time.RFC3339, endStr)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	return start, end, nil
}

func (h *AnalyticsHandlers) Overview(w http.ResponseWriter, r *http.Request) {
	storeID, ok := currentStore(r)
	if !ok {
		authn.Forbidden(w, r)
		return
	}
	start, end, err := parseRange(r)
	if err != nil {
		httpx.WriteError(w, r, httpx.New(httpx.CodeBadRequest, http.StatusBadRequest, "invalid range"))
		return
	}
	o, err := h.an.Overview(r.Context(), storeID, start, end)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, o)
}

func (h *AnalyticsHandlers) Trends(w http.ResponseWriter, r *http.Request) {
	storeID, ok := currentStore(r)
	if !ok {
		authn.Forbidden(w, r)
		return
	}
	start, end, err := parseRange(r)
	if err != nil {
		httpx.WriteError(w, r, httpx.New(httpx.CodeBadRequest, http.StatusBadRequest, "invalid range"))
		return
	}
	hourly := r.URL.Query().Has("hourly")
	pts, err := h.an.Trends(r.Context(), storeID, start, end, hourly)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"points": pts})
}

func (h *AnalyticsHandlers) Breakdown(w http.ResponseWriter, r *http.Request) {
	storeID, ok := currentStore(r)
	if !ok {
		authn.Forbidden(w, r)
		return
	}
	start, end, err := parseRange(r)
	if err != nil {
		httpx.WriteError(w, r, httpx.New(httpx.CodeBadRequest, http.StatusBadRequest, "invalid range"))
		return
	}
	b, err := h.an.Breakdown(r.Context(), storeID, start, end)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, b)
}

// Export GET /api/v1/admin/store/export（PRD §11）。
func (h *AnalyticsHandlers) Export(w http.ResponseWriter, r *http.Request) {
	storeID, ok := currentStore(r)
	if !ok {
		authn.Forbidden(w, r)
		return
	}
	f, err := h.exp.Export(r.Context(), storeID)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, f)
}

// Import POST /api/v1/admin/store/import（仅 OWNER，PRD §11）。
func (h *AnalyticsHandlers) Import(w http.ResponseWriter, r *http.Request) {
	storeID, ok := currentStore(r)
	if !ok {
		authn.Forbidden(w, r)
		return
	}
	if m, ok := authn.MembershipFrom(r.Context()); !ok || m.Role != authn.RoleOwner {
		authn.Forbidden(w, r)
		return
	}
	// 读取 body（限 1.5MB）。
	r.Body = http.MaxBytesReader(w, r.Body, 1.5*1024*1024)
	buf := make([]byte, 0, 4096)
	chunk := make([]byte, 4096)
	for {
		n, err := r.Body.Read(chunk)
		if n > 0 {
			buf = append(buf, chunk[:n]...)
		}
		if err != nil {
			break
		}
	}
	f, err := export.ParseFile(buf)
	if err != nil {
		httpx.WriteError(w, r, httpx.New(httpx.CodeBadRequest, http.StatusBadRequest, err.Error()))
		return
	}
	overwrite := r.URL.Query().Has("overwrite_appid")
	appid := r.URL.Query().Get("appid")
	if err := h.exp.Import(r.Context(), storeID, f, overwrite, appid); err != nil {
		if errors.Is(err, errAppIDConflict) || containsStr(err.Error(), "WECHAT_APPID_CONFLICT") {
			httpx.WriteError(w, r, httpx.New(httpx.CodeWechatAppidConflict, http.StatusConflict, err.Error()))
			return
		}
		httpx.WriteError(w, r, httpx.New(httpx.CodeConflict, http.StatusConflict, err.Error()))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "imported"})
}

var errAppIDConflict = errors.New("appid conflict")

func containsStr(s, sub string) bool {
	if len(sub) == 0 {
		return false
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// 抑制未用
var _ = strconv.Atoi
