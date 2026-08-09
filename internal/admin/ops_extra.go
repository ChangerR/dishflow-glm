package admin

import (
	"database/sql"
	"net/http"
	"strconv"

	"github.com/dishflow/zshop/internal/authn"
	"github.com/dishflow/zshop/internal/httpx"
)

// ListRefunds GET /api/v1/admin/refunds（PRD §6.4 退款列表）。
func (h *OrderHandlers) ListRefunds(w http.ResponseWriter, r *http.Request) {
	storeID, ok := currentStore(r)
	if !ok {
		authn.Forbidden(w, r)
		return
	}
	rows, err := h.db.QueryContext(r.Context(),
		`SELECT r.id, r.order_id, r.amount_cents, r.reason, r.status, r.trigger_kind, r.mock_refund, r.created_at, r.succeeded_at,
		        o.order_no
		 FROM refunds r JOIN orders o ON o.id=r.order_id
		 WHERE r.store_id=? ORDER BY r.id DESC LIMIT 100`, storeID)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, orderID, amount int64
		var reason, status, trigger, orderNo, created string
		var mock int
		var succeeded sql.NullString
		if err := rows.Scan(&id, &orderID, &amount, &reason, &status, &trigger, &mock, &created, &succeeded, &orderNo); err != nil {
			httpx.WriteError(w, r, err)
			return
		}
		item := map[string]any{
			"id": id, "order_id": orderID, "order_no": orderNo, "amount_cents": amount,
			"reason": reason, "status": status, "trigger_kind": trigger,
			"mock_refund": mock == 1, "created_at": created,
		}
		if succeeded.Valid {
			item["succeeded_at"] = succeeded.String
		}
		items = append(items, item)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

// ListExceptions GET /api/v1/admin/exceptions（PRD §6.4 异常列表：支付/退款失败或异常）。
func (h *OrderHandlers) ListExceptions(w http.ResponseWriter, r *http.Request) {
	storeID, ok := currentStore(r)
	if !ok {
		authn.Forbidden(w, r)
		return
	}
	// 支付异常：status=FAILED。
	rows, err := h.db.QueryContext(r.Context(),
		`SELECT p.id, p.order_id, p.status, p.last_error, p.created_at
		 FROM payments p WHERE p.store_id=? AND p.status IN ('FAILED') ORDER BY p.id DESC LIMIT 100`, storeID)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, orderID int64
		var status, lastErr, created string
		if err := rows.Scan(&id, &orderID, &status, &lastErr, &created); err != nil {
			httpx.WriteError(w, r, err)
			return
		}
		items = append(items, map[string]any{
			"id": id, "order_id": orderID, "type": "payment", "status": status, "error": lastErr, "created_at": created,
		})
	}
	// 退款异常：status=ABNORMAL。
	rows2, err := h.db.QueryContext(r.Context(),
		`SELECT id, order_id, status, last_error, created_at FROM refunds WHERE store_id=? AND status='ABNORMAL' ORDER BY id DESC LIMIT 100`, storeID)
	if err == nil {
		defer rows2.Close()
		for rows2.Next() {
			var id, orderID int64
			var status, lastErr, created string
			if err := rows2.Scan(&id, &orderID, &status, &lastErr, &created); err == nil {
				items = append(items, map[string]any{
					"id": id, "order_id": orderID, "type": "refund", "status": status, "error": lastErr, "created_at": created,
				})
			}
		}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

// RetryException POST /api/v1/admin/exceptions/{id}/retry（PRD §6.4：只把补偿任务加入队列）。
func (h *OrderHandlers) RetryException(w http.ResponseWriter, r *http.Request) {
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
	// 重试查单：把任务加入 outbox（最终状态仍需微信查单确认，PRD §6.4）。
	h.db.ExecContext(r.Context(),
		`INSERT INTO outbox (store_id, event_type, aggregate_type, aggregate_id, payload, status)
		 VALUES (?, 'retry.query', 'exception', ?, '{"action":"query"}', 'PENDING')`, storeID, id)
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "queued"})
}

// ListAuditLogs GET /api/v1/admin/audit-logs（PRD §18 门店审计日志）。
func (h *OrderHandlers) ListAuditLogs(w http.ResponseWriter, r *http.Request) {
	storeID, ok := currentStore(r)
	if !ok {
		authn.Forbidden(w, r)
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := h.db.QueryContext(r.Context(),
		`SELECT id, actor_type, actor_admin_user_id, action, resource_type, resource_id, summary, request_id, created_at
		 FROM audit_logs WHERE store_id=? ORDER BY id DESC LIMIT ?`, storeID, limit)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id int64
		var actorType, action, resType, resID, summary, created string
		var actorID sql.NullInt64
		var reqID string
		if err := rows.Scan(&id, &actorType, &actorID, &action, &resType, &resID, &summary, &reqID, &created); err != nil {
			httpx.WriteError(w, r, err)
			return
		}
		item := map[string]any{
			"id": id, "actor_type": actorType, "action": action,
			"resource_type": resType, "resource_id": resID, "summary": summary,
			"request_id": reqID, "created_at": created,
		}
		if actorID.Valid {
			item["actor_admin_user_id"] = actorID.Int64
		}
		items = append(items, item)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}
