package admin

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"

	"github.com/dishflow/zshop/internal/authn"
	"github.com/dishflow/zshop/internal/httpx"
	"github.com/dishflow/zshop/internal/orders"
)

// OrderHandlers 后台订单工作台/历史/状态推进（PRD §6）。
type OrderHandlers struct {
	ords *orders.Store
	db   *sql.DB
}

// NewOrderHandlers 构造订单 handler。
func NewOrderHandlers(ords *orders.Store, db *sql.DB) *OrderHandlers {
	return &OrderHandlers{ords: ords, db: db}
}

// Board GET /api/v1/admin/orders/board（PRD §6.1）。
func (h *OrderHandlers) Board(w http.ResponseWriter, r *http.Request) {
	storeID, ok := currentStore(r)
	if !ok {
		authn.Forbidden(w, r)
		return
	}
	cols, err := h.ords.Board(r.Context(), storeID)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"columns": cols})
}

// Detail GET /api/v1/admin/orders/{id}（PRD §6.1/§6.3）。
func (h *OrderHandlers) Detail(w http.ResponseWriter, r *http.Request) {
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
	o, err := h.ords.GetByID(r.Context(), storeID, id)
	if err != nil {
		httpx.WriteError(w, r, httpx.New(httpx.CodeNotFound, http.StatusNotFound, "order not found"))
		return
	}
	items, _ := h.ords.GetItems(r.Context(), storeID, id)
	events, _ := h.ords.GetEvents(r.Context(), storeID, id)
	httpx.WriteJSON(w, http.StatusOK, orders.DetailToDTO(o, items, events))
}

type transitionReq struct {
	To              string `json:"to"`
	ExpectedVersion int64  `json:"expected_version"`
}

// Transition POST /api/v1/admin/orders/{id}/transitions（PRD §6.1 乐观锁）。
func (h *OrderHandlers) Transition(w http.ResponseWriter, r *http.Request) {
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
	var req transitionReq
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	user, _ := authn.AdminUserFrom(r.Context())
	o, err := h.ords.Transition(r.Context(), orders.TransitionInput{
		StoreID: storeID, OrderID: id, To: orders.State(req.To), Actor: "staff",
		ActorUserID: user.ID, ExpectedVersion: req.ExpectedVersion,
	})
	if err != nil {
		httpx.WriteError(w, r, mapTransitionErr(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, orders.ToDTO(o))
}

func mapTransitionErr(err error) *httpx.Error {
	switch {
	case errors.Is(err, orders.ErrInvalidTransition):
		return httpx.New(httpx.CodeStateConflict, http.StatusConflict, err.Error())
	case errors.Is(err, orders.ErrVersionConflict):
		return httpx.New(httpx.CodeStateConflict, http.StatusConflict, "version conflict; refresh and retry")
	default:
		return httpx.New(httpx.CodeBadRequest, http.StatusBadRequest, err.Error())
	}
}

// StaffRefund POST /api/v1/admin/orders/{id}/refunds（PRD §6.3 门店主动退款）。
func (h *OrderHandlers) StaffRefund(w http.ResponseWriter, r *http.Request) {
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
	user, _ := authn.AdminUserFrom(r.Context())
	if err := h.ords.StaffRefund(r.Context(), storeID, id, user.ID); err != nil {
		httpx.WriteError(w, r, httpx.New(httpx.CodeStateConflict, http.StatusConflict, err.Error()))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "REFUNDING"})
}

// ReviewCancel POST /api/v1/admin/refunds/{id}/review（PRD §6.4 顾客取消退款审核）。
func (h *OrderHandlers) ReviewCancel(w http.ResponseWriter, r *http.Request) {
	storeID, ok := currentStore(r)
	if !ok {
		authn.Forbidden(w, r)
		return
	}
	// refund id 查关联订单。
	var orderID int64
	if err := h.db.QueryRowContext(r.Context(),
		`SELECT order_id FROM refunds WHERE id=?`, r.PathValue("id")).Scan(&orderID); err != nil {
		httpx.WriteError(w, r, httpx.New(httpx.CodeNotFound, http.StatusNotFound, "refund not found"))
		return
	}
	var req struct {
		Approve bool `json:"approve"`
	}
	httpx.DecodeJSON(r, &req)
	user, _ := authn.AdminUserFrom(r.Context())
	if err := h.ords.ReviewCancelRequest(r.Context(), storeID, orderID, user.ID, req.Approve); err != nil {
		httpx.WriteError(w, r, httpx.New(httpx.CodeStateConflict, http.StatusConflict, err.Error()))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
