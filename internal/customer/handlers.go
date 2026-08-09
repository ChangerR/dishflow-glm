// Package customer 实现顾客侧 HTTP handler（PRD §4/§16.1）。
//
// 这些接口走 /api/v1 前缀，鉴权方式：Bearer token + X-Wechat-Appid（PRD §2.2）。
package customer

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/dishflow/zshop/internal/customerauth"
	"github.com/dishflow/zshop/internal/httpx"
	"github.com/dishflow/zshop/internal/marketing"
	"github.com/dishflow/zshop/internal/pickup"
	"github.com/dishflow/zshop/internal/pricing"
	"github.com/dishflow/zshop/internal/storefront"
)

// Handlers 顾客侧 handler 集合。
type Handlers struct {
	sf     *storefront.Store
	auth   *customerauth.Store
	quotes *pricing.QuoteStore
	mkt    marketingReader
}

// marketingReader 营销读取接口（解耦，便于测试）。
type marketingReader interface {
	ListPublicCouponOffers(ctx context.Context, storeID, customerID int64, now time.Time) ([]marketing.CouponTemplate, error)
	GetCouponForPricing(ctx context.Context, storeID, customerID, customerCouponID int64) (marketing.CouponForPricing, error)
}

// New 构造顾客 handler。
func New(sf *storefront.Store, auth *customerauth.Store, quotes *pricing.QuoteStore) *Handlers {
	return &Handlers{sf: sf, auth: auth, quotes: quotes}
}

// WithMarketing 注入营销读取器（CouponOffers 用）。
func (h *Handlers) WithMarketing(m marketingReader) *Handlers { h.mkt = m; return h }

// appidFrom 从 X-Wechat-Appid 取门店标识（PRD §2.2）。
func appidFrom(r *http.Request) string {
	return r.Header.Get("X-Wechat-Appid")
}

// resolveStoreByAppid 解析门店，失败返回 false（已写入错误响应）。
func (h *Handlers) resolveStoreByAppid(w http.ResponseWriter, r *http.Request) (storefront.Bootstrap, bool) {
	b, err := h.sf.BootstrapByAppID(r.Context(), appidFrom(r))
	if err != nil {
		httpx.WriteError(w, r, httpx.New(httpx.CodeNotFound, http.StatusNotFound, "store not found for appid"))
		return storefront.Bootstrap{}, false
	}
	return b, true
}

// Bootstrap GET /api/v1/storefront/bootstrap
func (h *Handlers) Bootstrap(w http.ResponseWriter, r *http.Request) {
	b, err := h.sf.BootstrapByAppID(r.Context(), appidFrom(r))
	if err != nil {
		httpx.WriteError(w, r, httpx.New(httpx.CodeNotFound, http.StatusNotFound, "store not found for appid"))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"store_id":      b.StoreID,
		"store_name":    b.StoreName,
		"brand_name":    b.BusinessName,
		"theme_color":   b.ThemeColor,
		"logo_url":      b.LogoURL,
		"business_open": b.BusinessOpen,
		"announcement":  b.Announcement,
		"business_hours": b.BusinessHours,
		"timezone":      b.Timezone,
	})
}

// StoreInfo GET /api/v1/store
func (h *Handlers) StoreInfo(w http.ResponseWriter, r *http.Request) {
	b, ok := h.resolveStoreByAppid(w, r)
	if !ok {
		return
	}
	si, err := h.sf.GetStoreInfo(r.Context(), b.StoreID)
	if err != nil {
		httpx.WriteError(w, r, httpx.New(httpx.CodeNotFound, http.StatusNotFound, "store not found"))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, si)
}

// WechatSession POST /api/v1/auth/wechat/session
// body: {code}。返回 {token, expires_at}（PRD §4.1.5/§16.1）。
func (h *Handlers) WechatSession(w http.ResponseWriter, r *http.Request) {
	b, ok := h.resolveStoreByAppid(w, r)
	if !ok {
		return
	}
	var req struct {
		Code string `json:"code"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if req.Code == "" {
		httpx.WriteError(w, r, httpx.New(httpx.CodeBadRequest, http.StatusBadRequest, "code required"))
		return
	}
	token, sess, err := h.auth.IssueSession(r.Context(), b.StoreID, b.AppID, req.Code)
	if err != nil {
		httpx.WriteError(w, r, httpx.New(httpx.CodeUnauthorized, http.StatusUnauthorized, "code exchange failed"))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"token":      token,
		"expires_at": sess.ExpiresAt.Format(time.RFC3339),
	})
}

// Menu GET /api/v1/menu（PRD §4.2）
func (h *Handlers) Menu(w http.ResponseWriter, r *http.Request) {
	b, ok := h.resolveStoreByAppid(w, r)
	if !ok {
		return
	}
	// 业务日 = 门店今天（简化）。
	bizDate := todayInTZ(b.Timezone)
	menu, err := h.sf.GetPublicMenu(r.Context(), b.StoreID, bizDate)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, menu)
}

// ResolveTable GET /api/v1/tables/resolve?token=xxx（PRD §4.1.2）
func (h *Handlers) ResolveTable(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	info, err := h.sf.ResolveTable(r.Context(), token)
	if err != nil {
		// 无效/轮换/停用 → 明确提示重新扫码（PRD §4.1.3）。
		httpx.WriteError(w, r, httpx.New(httpx.CodeTableNotFound, http.StatusNotFound, "table not found; please rescan"))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, info)
}

// PickupSlots GET /api/v1/pickup-slots?date=YYYY-MM-DD（reservation-pickup §7.1）
func (h *Handlers) PickupSlots(w http.ResponseWriter, r *http.Request) {
	b, ok := h.resolveStoreByAppid(w, r)
	if !ok {
		return
	}
	dateStr := r.URL.Query().Get("date")
	if dateStr == "" {
		httpx.WriteError(w, r, httpx.New(httpx.CodeBadRequest, http.StatusBadRequest, "date required"))
		return
	}
	// 取门店预约配置（复用 BootstrapByAppID 后查 stores）。
	cfg, err := h.sf.PickupConfig(r.Context(), b.StoreID)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	date, err := pickup.ParseDate(dateStr, b.Timezone)
	if err != nil {
		httpx.WriteError(w, r, httpx.New(httpx.CodePickupTimeInvalid, http.StatusBadRequest, "invalid date"))
		return
	}
	now := time.Now()
	if loc, e := time.LoadLocation(b.Timezone); e == nil {
		now = now.In(loc)
	}
	reserved, err := h.sf.ReservedBySlot(r.Context(), b.StoreID, dateStr)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	slots, err := pickup.GenerateSlots(cfg, date, now, reserved)
	if err != nil {
		httpx.WriteError(w, r, httpx.New(httpx.CodePickupTimeInvalid, http.StatusBadRequest, err.Error()))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"store_id": b.StoreID,
		"date":     dateStr,
		"timezone": b.Timezone,
		"slots":    slots,
	})
}

// todayInTZ 返回门店时区今天的 YYYY-MM-DD。
func todayInTZ(tz string) string {
	loc, err := time.LoadLocation(tz)
	if err != nil {
		loc = time.UTC
	}
	return time.Now().In(loc).Format("2006-01-02")
}

// timeNowUTC 返回当前 UTC 时间。
func timeNowUTC() time.Time { return time.Now().UTC() }

// 兼容：strconv 可能用于未来 query 参数。
var _ = strconv.Atoi
var _ = context.Background
