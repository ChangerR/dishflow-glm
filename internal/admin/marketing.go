package admin

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/dishflow/zshop/internal/authn"
	"github.com/dishflow/zshop/internal/httpx"
	"github.com/dishflow/zshop/internal/marketing"
	"github.com/dishflow/zshop/internal/members"
	"github.com/dishflow/zshop/internal/reliability"
	"github.com/dishflow/zshop/internal/tables"
)

// MarketingHandlers 营销/会员/桌台后台 handler（PRD §7.4/§8/§10.1）。
type MarketingHandlers struct {
	mkt *marketing.Store
	mem *members.Store
	tbl *tables.Store
}

// NewMarketingHandlers 构造。
func NewMarketingHandlers(mkt *marketing.Store, mem *members.Store, tbl *tables.Store) *MarketingHandlers {
	return &MarketingHandlers{mkt: mkt, mem: mem, tbl: tbl}
}

// ── 满减 ──────────────────────────────────────────────────────────────

func (h *MarketingHandlers) ListPromotions(w http.ResponseWriter, r *http.Request) {
	storeID, ok := currentStore(r)
	if !ok {
		authn.Forbidden(w, r)
		return
	}
	promos, err := h.mkt.ListActivePromotions(r.Context(), storeID, time.Now())
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	items := make([]map[string]any, 0, len(promos))
	for _, p := range promos {
		items = append(items, map[string]any{
			"id": p.ID, "name": p.Name, "threshold_cents": p.ThresholdCents, "discount_cents": p.DiscountCents,
			"starts_at": p.StartsAt, "ends_at": p.EndsAt,
		})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *MarketingHandlers) CreatePromotion(w http.ResponseWriter, r *http.Request) {
	storeID, ok := currentStore(r)
	if !ok {
		authn.Forbidden(w, r)
		return
	}
	var req struct {
		Name           string `json:"name"`
		ThresholdCents int64  `json:"threshold_cents"`
		DiscountCents  int64  `json:"discount_cents"`
		StartsAt       string `json:"starts_at"`
		EndsAt         string `json:"ends_at"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	start, err := time.Parse(time.RFC3339, req.StartsAt)
	if err != nil {
		httpx.WriteError(w, r, httpx.New(httpx.CodeBadRequest, http.StatusBadRequest, "invalid starts_at"))
		return
	}
	end, err := time.Parse(time.RFC3339, req.EndsAt)
	if err != nil {
		httpx.WriteError(w, r, httpx.New(httpx.CodeBadRequest, http.StatusBadRequest, "invalid ends_at"))
		return
	}
	id, err := h.mkt.CreatePromotion(r.Context(), storeID, req.Name, req.ThresholdCents, req.DiscountCents, start, end)
	if err != nil {
		httpx.WriteError(w, r, httpx.New(httpx.CodeBadRequest, http.StatusBadRequest, err.Error()))
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]int64{"id": id})
}

// ── 会员积分人工调整 ──────────────────────────────────────────────────

func (h *MarketingHandlers) PointsAdjustment(w http.ResponseWriter, r *http.Request) {
	storeID, ok := currentStore(r)
	if !ok {
		authn.Forbidden(w, r)
		return
	}
	customerID, err := strconv.ParseInt(r.PathValue("customerId"), 10, 64)
	if err != nil || customerID <= 0 {
		httpx.WriteError(w, r, httpx.New(httpx.CodeBadRequest, http.StatusBadRequest, "invalid customer_id"))
		return
	}
	var req struct {
		Delta  int64  `json:"delta"`
		Reason string `json:"reason"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	m, err := h.mem.GetByCustomer(r.Context(), storeID, customerID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httpx.WriteError(w, r, httpx.New(httpx.CodeNotFound, http.StatusNotFound, "membership not found"))
			return
		}
		httpx.WriteError(w, r, err)
		return
	}
	user, _ := authn.AdminUserFrom(r.Context())
	idemKey := reliability.FromContext(r.Context())
	if idemKey == "" {
		httpx.WriteError(w, r, httpx.New(httpx.CodeBadRequest, http.StatusBadRequest, "Idempotency-Key required"))
		return
	}
	bal, err := h.mem.AdjustPoints(r.Context(), m.ID, storeID, customerID, user.ID, req.Delta, req.Reason, idemKey)
	if err != nil {
		if errors.Is(err, members.ErrInsufficientPoints) {
			httpx.WriteError(w, r, httpx.New(httpx.CodeInsufficientPoints, http.StatusConflict, "insufficient points"))
			return
		}
		httpx.WriteError(w, r, httpx.New(httpx.CodeConflict, http.StatusConflict, err.Error()))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]int64{"balance_after": bal})
}

// ── 桌台 ──────────────────────────────────────────────────────────────

func (h *MarketingHandlers) ListTables(w http.ResponseWriter, r *http.Request) {
	storeID, ok := currentStore(r)
	if !ok {
		authn.Forbidden(w, r)
		return
	}
	ts, err := h.tbl.List(r.Context(), storeID)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	items := make([]map[string]any, 0, len(ts))
	for _, t := range ts {
		items = append(items, map[string]any{
			"id": t.ID, "store_id": t.StoreID, "table_no": t.TableNo, "area": t.Area,
			"enabled": t.Enabled, "table_token": t.TableToken, "scene": t.Scene,
		})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *MarketingHandlers) CreateTable(w http.ResponseWriter, r *http.Request) {
	storeID, ok := currentStore(r)
	if !ok {
		authn.Forbidden(w, r)
		return
	}
	var req struct {
		TableNo string `json:"table_no"`
		Area    string `json:"area"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	t, err := h.tbl.CreateTable(r.Context(), storeID, req.TableNo, req.Area)
	if err != nil {
		httpx.WriteError(w, r, httpx.New(httpx.CodeConflict, http.StatusConflict, err.Error()))
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"id": t.ID, "table_no": t.TableNo, "table_token": t.TableToken})
}

func (h *MarketingHandlers) RotateTableToken(w http.ResponseWriter, r *http.Request) {
	storeID, ok := currentStore(r)
	if !ok {
		authn.Forbidden(w, r)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		httpx.WriteError(w, r, httpx.New(httpx.CodeBadRequest, http.StatusBadRequest, "invalid id"))
		return
	}
	t, err := h.tbl.RotateToken(r.Context(), storeID, id)
	if err != nil {
		httpx.WriteError(w, r, httpx.New(httpx.CodeNotFound, http.StatusNotFound, err.Error()))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"id": t.ID, "table_token": t.TableToken})
}

// suppress unused
var _ = json.Marshal
