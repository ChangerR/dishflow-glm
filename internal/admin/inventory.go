package admin

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/dishflow/zshop/internal/authn"
	"github.com/dishflow/zshop/internal/httpx"
	"github.com/dishflow/zshop/internal/inventory"
	"github.com/dishflow/zshop/internal/reliability"
)

// InventoryHandlers 库存调整 handler（PRD §7.3）。
type InventoryHandlers struct {
	inv *inventory.Store
}

// NewInventoryHandlers 构造库存 handler。
func NewInventoryHandlers(inv *inventory.Store) *InventoryHandlers { return &InventoryHandlers{inv: inv} }

type adjustReq struct {
	BusinessDate string `json:"business_date"`
	Delta        int    `json:"delta"`
	Reason       string `json:"reason"`
}

// AdjustStock POST /api/v1/admin/dishes/{id}/stock-adjustments
// 路径 {id} 为 SKU ID（PRD §16.4: dishes/{id}/stock-adjustments 针对每日库存 SKU）。
func (h *InventoryHandlers) AdjustStock(w http.ResponseWriter, r *http.Request) {
	storeID, ok := currentStore(r)
	if !ok {
		authn.Forbidden(w, r)
		return
	}
	skuID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || skuID <= 0 {
		httpx.WriteError(w, r, httpx.New(httpx.CodeBadRequest, http.StatusBadRequest, "invalid sku id"))
		return
	}
	var req adjustReq
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if req.BusinessDate == "" {
		httpx.WriteError(w, r, httpx.New(httpx.CodeBadRequest, http.StatusBadRequest, "business_date required"))
		return
	}

	// 幂等键来自请求头（PRD §7.3/§16）。
	idemKey := reliability.FromContext(r.Context())
	if idemKey == "" {
		httpx.WriteError(w, r, httpx.New(httpx.CodeBadRequest, http.StatusBadRequest, "Idempotency-Key required"))
		return
	}

	user, _ := authn.AdminUserFrom(r.Context())
	var opID int64
	if user.ID > 0 {
		opID = user.ID
	}
	if err := h.inv.Adjust(r.Context(), inventory.AdjustInput{
		StoreID: storeID, SKUID: skuID, BusinessDate: req.BusinessDate,
		Delta: req.Delta, Reason: req.Reason, OperatorID: opID, IdempotencyKey: idemKey,
	}); err != nil {
		// 区分原因码：违反不变量 → STATE_CONFLICT；幂等冲突 → CONFLICT。
		httpx.WriteError(w, r, mapInventoryErr(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

// ListMovements GET /api/v1/admin/dishes/{id}/stock-adjustments（流水）
func (h *InventoryHandlers) ListMovements(w http.ResponseWriter, r *http.Request) {
	storeID, ok := currentStore(r)
	if !ok {
		authn.Forbidden(w, r)
		return
	}
	skuID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || skuID <= 0 {
		httpx.WriteError(w, r, httpx.New(httpx.CodeBadRequest, http.StatusBadRequest, "invalid sku id"))
		return
	}
	date := r.URL.Query().Get("business_date")
	movs, err := h.inv.ListMovements(r.Context(), storeID, skuID, date, 50, 0)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": movs})
}

// mapInventoryErr 把库存领域错误映射到 HTTP 错误码。
func mapInventoryErr(err error) *httpx.Error {
	msg := err.Error()
	var he *httpx.Error
	if errors.As(err, &he) {
		return he
	}
	switch {
	case strings.Contains(msg, "invariant"), strings.Contains(msg, "violates"):
		return httpx.New(httpx.CodeStateConflict, http.StatusConflict, msg)
	case strings.Contains(msg, "idempotency"):
		return httpx.New(httpx.CodeConflict, http.StatusConflict, msg)
	default:
		return httpx.New(httpx.CodeBadRequest, http.StatusBadRequest, msg)
	}
}
