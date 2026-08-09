package customer

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/dishflow/zshop/internal/customerauth"
	"github.com/dishflow/zshop/internal/httpx"
	"github.com/dishflow/zshop/internal/marketing"
	"github.com/dishflow/zshop/internal/members"
)

// MeHandlers 顾客“我的”/会员/优惠券 handler（PRD §4.11/§4.12/§4.13）。
type MeHandlers struct {
	mkt *marketing.Store
	mem *members.Store
}

// NewMe 构造顾客“我的”handler。
func NewMe(mkt *marketing.Store, mem *members.Store) *MeHandlers { return &MeHandlers{mkt: mkt, mem: mem} }

// CouponOffers GET /api/v1/coupon-offers（PRD §4.11）。
// 注：此方法挂在主 Handlers（含 storefront），公开无需 Bearer。
func (h *Handlers) CouponOffers(w http.ResponseWriter, r *http.Request) {
	b, ok := h.resolveStoreByAppid(w, r)
	if !ok {
		return
	}
	offers, err := h.mkt.ListPublicCouponOffers(r.Context(), b.StoreID, 0, timeNowUTC())
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	items := make([]map[string]any, 0, len(offers))
	for _, o := range offers {
		items = append(items, map[string]any{
			"template_id": o.ID, "name": o.Name,
			"min_spend_cents": o.MinSpendCents, "discount_cents": o.DiscountCents,
			"starts_at": o.StartsAt, "ends_at": o.EndsAt,
		})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

// Claim POST /api/v1/coupon-offers/{template_id}/claim（PRD §4.11 幂等）。
func (h *MeHandlers) Claim(w http.ResponseWriter, r *http.Request) {
	sess, ok := customerauth.SessionFrom(r.Context())
	if !ok {
		httpx.WriteError(w, r, httpx.New(httpx.CodeUnauthorized, http.StatusUnauthorized, "authentication required"))
		return
	}
	tid, err := strconv.ParseInt(r.PathValue("template_id"), 10, 64)
	if err != nil || tid <= 0 {
		httpx.WriteError(w, r, httpx.New(httpx.CodeBadRequest, http.StatusBadRequest, "invalid template_id"))
		return
	}
	id, err := h.mkt.Claim(r.Context(), sess.StoreID, sess.CustomerID, tid)
	if err != nil {
		if errors.Is(err, marketing.ErrAlreadyClaimed) {
			httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "already_claimed"})
			return
		}
		httpx.WriteError(w, r, httpx.New(httpx.CodeConflict, http.StatusConflict, err.Error()))
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]int64{"id": id, "status": 1})
}

// MyCoupons GET /api/v1/coupons（PRD §4.11）。
func (h *MeHandlers) MyCoupons(w http.ResponseWriter, r *http.Request) {
	sess, ok := customerauth.SessionFrom(r.Context())
	if !ok {
		httpx.WriteError(w, r, httpx.New(httpx.CodeUnauthorized, http.StatusUnauthorized, "authentication required"))
		return
	}
	status := r.URL.Query().Get("status")
	coupons, err := h.mkt.ListCustomerCoupons(r.Context(), sess.StoreID, sess.CustomerID, status)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	items := make([]map[string]any, 0, len(coupons))
	for _, c := range coupons {
		items = append(items, map[string]any{
			"id": c.ID, "template_id": c.TemplateID, "name": c.TemplateName,
			"status": c.Status, "min_spend_cents": c.MinSpendCents, "discount_cents": c.DiscountCents,
			"expires_at": c.ExpiresAt,
		})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

// Me GET /api/v1/me（PRD §4.13）。
func (h *MeHandlers) Me(w http.ResponseWriter, r *http.Request) {
	sess, ok := customerauth.SessionFrom(r.Context())
	if !ok {
		httpx.WriteError(w, r, httpx.New(httpx.CodeUnauthorized, http.StatusUnauthorized, "authentication required"))
		return
	}
	resp := map[string]any{"customer_id": sess.CustomerID, "store_id": sess.StoreID}
	if m, err := h.mem.GetByCustomer(r.Context(), sess.StoreID, sess.CustomerID); err == nil {
		resp["member_no"] = m.MemberNo
		resp["points_balance"] = m.PointsBalance
		resp["membership_status"] = m.Status
	}
	httpx.WriteJSON(w, http.StatusOK, resp)
}

// PointsLedger GET /api/v1/me/points（PRD §4.12/§8.1）。
func (h *MeHandlers) PointsLedger(w http.ResponseWriter, r *http.Request) {
	sess, ok := customerauth.SessionFrom(r.Context())
	if !ok {
		httpx.WriteError(w, r, httpx.New(httpx.CodeUnauthorized, http.StatusUnauthorized, "authentication required"))
		return
	}
	entries, err := h.mem.ListPointsLedger(r.Context(), sess.StoreID, sess.CustomerID, 50, 0)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": entries})
}

// JoinMembership POST /api/v1/me/membership（PRD §4.12 入会手机号验证）。
// body: {phone, country_code}。真实手机号 code 验证由 phoneCodeExchanger（P6 完整）。
func (h *MeHandlers) JoinMembership(w http.ResponseWriter, r *http.Request) {
	sess, ok := customerauth.SessionFrom(r.Context())
	if !ok {
		httpx.WriteError(w, r, httpx.New(httpx.CodeUnauthorized, http.StatusUnauthorized, "authentication required"))
		return
	}
	var req struct {
		Phone       string `json:"phone"`
		CountryCode string `json:"country_code"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if req.Phone == "" {
		httpx.WriteError(w, r, httpx.New(httpx.CodeBadRequest, http.StatusBadRequest, "phone required"))
		return
	}
	m, isNew, err := h.mem.Join(r.Context(), sess.StoreID, sess.CustomerID, req.Phone, req.CountryCode)
	if err != nil {
		if errors.Is(err, members.ErrPhoneBound) {
			httpx.WriteError(w, r, httpx.New(httpx.CodeConflict, http.StatusConflict, "phone bound to another member"))
			return
		}
		httpx.WriteError(w, r, httpx.New(httpx.CodeBadRequest, http.StatusBadRequest, err.Error()))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"member_no": m.MemberNo, "is_new": isNew, "points_balance": m.PointsBalance,
	})
}

// Rewards GET /api/v1/me/rewards（积分兑换券模板，PRD §4.12）。
func (h *MeHandlers) Rewards(w http.ResponseWriter, r *http.Request) {
	// 简化：返回可兑换券模板列表（P6 完整查询在 mkt 扩展）。
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": []any{}})
}
