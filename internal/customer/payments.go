package customer

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/dishflow/zshop/internal/customerauth"
	"github.com/dishflow/zshop/internal/httpx"
	"github.com/dishflow/zshop/internal/orders"
	"github.com/dishflow/zshop/internal/payments"
)

// PaymentHandlers 顾客支付 handler（PRD §4.8）。
type PaymentHandlers struct {
	auth *customerauth.Store
	ords *orders.Store
	pays *payments.Store
}

// NewPayments 构造顾客支付 handler。
func NewPayments(auth *customerauth.Store, ords *orders.Store, pays *payments.Store) *PaymentHandlers {
	return &PaymentHandlers{auth: auth, ords: ords, pays: pays}
}

// Prepay POST /api/v1/orders/{id}/prepay（PRD §4.8）。
func (h *PaymentHandlers) Prepay(w http.ResponseWriter, r *http.Request) {
	sess, ok := customerauth.SessionFrom(r.Context())
	if !ok {
		httpx.WriteError(w, r, httpx.New(httpx.CodeUnauthorized, http.StatusUnauthorized, "authentication required"))
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		httpx.WriteError(w, r, httpx.New(httpx.CodeBadRequest, http.StatusBadRequest, "invalid id"))
		return
	}
	o, err := h.ords.GetByID(r.Context(), sess.StoreID, id)
	if err != nil || o.CustomerID != sess.CustomerID {
		httpx.WriteError(w, r, httpx.New(httpx.CodeNotFound, http.StatusNotFound, "order not found"))
		return
	}
	if o.FulfillmentState != orders.StatePendingPayment {
		httpx.WriteError(w, r, httpx.New(httpx.CodeStateConflict, http.StatusConflict, "order not payable"))
		return
	}
	cust, _ := h.auth.GetCustomer(r.Context(), sess.CustomerID)
	res, err := h.pays.Prepay(r.Context(), payments.PrepayInput{
		StoreID: sess.StoreID, OrderID: o.ID, OutTradeNo: o.OrderNo,
		AmountCents: o.PayableCents, OpenID: cust.OpenID, Description: "DishFlow 订单",
	})
	if err != nil && !errors.Is(err, payments.ErrZeroOrderNotWechat) {
		httpx.WriteError(w, r, httpx.New(httpx.CodePaymentUnavailable, http.StatusServiceUnavailable, err.Error()))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"prepay_id":      res.PrepayID,
		"mock_payment":   res.MockPay,
		"jsapi_payload":  res.JSAPIPayload,
	})
}

// ConfirmMockPayment POST /api/v1/orders/{id}/mock-payment/confirm（PRD §4.8/§14.4）。
// 只有 mock 模式可用；显式确认接口，不得把任意响应当作 mock 成功。
func (h *PaymentHandlers) ConfirmMockPayment(w http.ResponseWriter, r *http.Request) {
	sess, ok := customerauth.SessionFrom(r.Context())
	if !ok {
		httpx.WriteError(w, r, httpx.New(httpx.CodeUnauthorized, http.StatusUnauthorized, "authentication required"))
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		httpx.WriteError(w, r, httpx.New(httpx.CodeBadRequest, http.StatusBadRequest, "invalid id"))
		return
	}
	o, err := h.ords.GetByID(r.Context(), sess.StoreID, id)
	if err != nil || o.CustomerID != sess.CustomerID {
		httpx.WriteError(w, r, httpx.New(httpx.CodeNotFound, http.StatusNotFound, "order not found"))
		return
	}
	if o.FulfillmentState != orders.StatePendingPayment {
		httpx.WriteError(w, r, httpx.New(httpx.CodeStateConflict, http.StatusConflict, "order not payable"))
		return
	}
	if err := h.pays.ConfirmMockPayment(r.Context(), sess.StoreID, o.ID); err != nil {
		httpx.WriteError(w, r, httpx.New(httpx.CodeBadRequest, http.StatusBadRequest, err.Error()))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "confirmed", "state": "PROCESSING"})
}
