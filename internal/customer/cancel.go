package customer

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"

	"github.com/dishflow/zshop/internal/customerauth"
	"github.com/dishflow/zshop/internal/httpx"
	"github.com/dishflow/zshop/internal/orders"
	"github.com/dishflow/zshop/internal/payments"
	"github.com/dishflow/zshop/internal/refunds"
)

// CancelOrder POST /api/v1/orders/{id}/cancel（PRD §4.10）。
func (h *OrdersHandlers) CancelOrder(w http.ResponseWriter, r *http.Request) {
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

	result, err := h.ords.Cancel(r.Context(), orders.CancelInput{
		StoreID: sess.StoreID, OrderID: id, CustomerID: sess.CustomerID, Actor: "customer",
	})
	if err != nil {
		httpx.WriteError(w, r, mapCancelErr(err))
		return
	}

	// PAID 未接单：自动发起退款（PRD §4.10）。
	if result.NeedRefund {
		if e := h.initiateRefund(r, sess.StoreID, id, sess.CustomerID, result.RefundTriggerKind); e != nil {
			httpx.WriteError(w, r, httpx.New(httpx.CodeRefundConflict, http.StatusConflict, e.Error()))
			return
		}
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"status":      string(result.NewState),
		"need_refund": result.NeedRefund,
	})
}

// initiateRefund 落退款意图 + mock 确认成功 + outbox（Worker 推进 REFUNDED + 积分扣回）。
func (h *OrdersHandlers) initiateRefund(r *http.Request, storeID, orderID, customerID int64, triggerKind string) error {
	refStore := refunds.NewStore(h.db)
	var payID, amount int64
	err := h.db.QueryRowContext(r.Context(),
		`SELECT id, amount_cents FROM payments WHERE order_id=? AND status='SUCCESS'`, orderID).Scan(&payID, &amount)
	if err == sql.ErrNoRows || payID == 0 {
		return errors.New("no successful payment to refund")
	}
	refundID, err := refStore.Create(r.Context(), refunds.CreateInput{
		StoreID: storeID, OrderID: orderID, PaymentID: payID, AmountCents: amount,
		Reason: "顾客取消自动退款", TriggerKind: triggerKind, Mock: true,
	})
	if err != nil {
		return err
	}
	// mock 退款确认成功。
	tx, err := h.db.BeginTx(r.Context(), nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := refStore.MarkSuccess(r.Context(), tx, storeID, refundID, "mock_refund_"+strconv.FormatInt(refundID, 10), ""); err != nil {
		return err
	}
	payload := `{"order_id":` + strconv.FormatInt(orderID, 10) +
		`,"customer_id":` + strconv.FormatInt(customerID, 10) +
		`,"refund_id":` + strconv.FormatInt(refundID, 10) + `}`
	if _, err := tx.ExecContext(r.Context(),
		`INSERT INTO outbox (store_id, event_type, aggregate_type, aggregate_id, payload, status)
		 VALUES (?, 'refund.success', 'refund', ?, ?, 'PENDING')`, storeID, refundID, []byte(payload)); err != nil {
		return err
	}
	return tx.Commit()
}

// 防止 payments 未用（mock provider 引用）。
var _ = payments.MockProvider{}

func mapCancelErr(err error) *httpx.Error {
	if errors.Is(err, orders.ErrInvalidTransition) {
		return httpx.New(httpx.CodeStateConflict, http.StatusConflict, err.Error())
	}
	return httpx.New(httpx.CodeBadRequest, http.StatusBadRequest, err.Error())
}
