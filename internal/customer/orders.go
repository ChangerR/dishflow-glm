package customer

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"

	"github.com/dishflow/zshop/internal/customerauth"
	"github.com/dishflow/zshop/internal/httpx"
	"github.com/dishflow/zshop/internal/orders"
	"github.com/dishflow/zshop/internal/pricing"
	"github.com/dishflow/zshop/internal/reliability"
)

// OrdersHandlers 顾客订单 handler（PRD §4.7-§4.10）。
type OrdersHandlers struct {
	auth   *customerauth.Store
	quotes *pricing.QuoteStore
	ords   *orders.Store
	db     *sql.DB
}

// NewOrders 构造顾客订单 handler。
func NewOrders(auth *customerauth.Store, quotes *pricing.QuoteStore, ords *orders.Store, db *sql.DB) *OrdersHandlers {
	return &OrdersHandlers{auth: auth, quotes: quotes, ords: ords, db: db}
}

type createOrderReq struct {
	QuoteToken string `json:"quote_token"`
	Remark     string `json:"remark"`
}

// Create POST /api/v1/orders（PRD §4.7）。
func (h *OrdersHandlers) Create(w http.ResponseWriter, r *http.Request) {
	sess, ok := customerauth.SessionFrom(r.Context())
	if !ok {
		httpx.WriteError(w, r, httpx.New(httpx.CodeUnauthorized, http.StatusUnauthorized, "authentication required"))
		return
	}
	var req createOrderReq
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if req.QuoteToken == "" {
		httpx.WriteError(w, r, httpx.New(httpx.CodeQuoteMismatch, http.StatusBadRequest, "quote_token required"))
		return
	}

	summary, err := h.quotes.Lookup(r.Context(), req.QuoteToken)
	if err != nil {
		if errors.Is(err, pricing.ErrQuoteExpired) {
			httpx.WriteError(w, r, httpx.New(httpx.CodeQuoteExpired, http.StatusGone, "quote expired"))
			return
		}
		httpx.WriteError(w, r, httpx.New(httpx.CodeQuoteMismatch, http.StatusBadRequest, err.Error()))
		return
	}
	// 门店/顾客隔离（PRD §2.2）。
	if summary.StoreID != sess.StoreID {
		httpx.WriteError(w, r, httpx.New(httpx.CodeForbidden, http.StatusForbidden, "store mismatch"))
		return
	}
	if summary.CustomerID != 0 && summary.CustomerID != sess.CustomerID {
		httpx.WriteError(w, r, httpx.New(httpx.CodeForbidden, http.StatusForbidden, "customer mismatch"))
		return
	}

	// 业务日：预约单用预约日期，即时单用门店今天（PRD §4.6/§4.7）。
	bizDate := summary.PickupBusinessDate()
	idemKey := reliability.FromContext(r.Context())
	if idemKey == "" {
		// 顾客下单也需幂等（同 quote 只能创建一个订单，PRD §4.7）。
		idemKey = "quote:" + summary.Token
	}

	orderID, err := h.ords.Create(r.Context(), orders.CreateInput{
		StoreID: sess.StoreID, CustomerID: sess.CustomerID, Quote: summary,
		Cart: summaryToCart(summary), Remark: truncate(req.Remark, 100),
		IdempotencyKey: idemKey, BusinessDate: bizDate,
	})
	if err != nil {
		httpx.WriteError(w, r, mapOrderErr(err))
		return
	}
	// 删除报价（同 quote 只能创建一个订单）。
	_ = h.quotes.Delete(r.Context(), summary.Token)

	o, _ := h.ords.GetByID(r.Context(), sess.StoreID, orderID)
	httpx.WriteJSON(w, http.StatusCreated, orders.ToDTO(o))
}

// MyList GET /api/v1/orders（PRD §4.9）。
func (h *OrdersHandlers) MyList(w http.ResponseWriter, r *http.Request) {
	sess, ok := customerauth.SessionFrom(r.Context())
	if !ok {
		httpx.WriteError(w, r, httpx.New(httpx.CodeUnauthorized, http.StatusUnauthorized, "authentication required"))
		return
	}
	status := r.URL.Query().Get("status")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	ords, err := h.ords.ListByCustomer(r.Context(), sess.StoreID, sess.CustomerID, status, limit, 0)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	items := make([]orders.OrderDTO, 0, len(ords))
	for _, o := range ords {
		items = append(items, orders.ToDTO(o))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

// MyDetail GET /api/v1/orders/{id}（PRD §4.9）。
func (h *OrdersHandlers) MyDetail(w http.ResponseWriter, r *http.Request) {
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
	if err != nil {
		httpx.WriteError(w, r, httpx.New(httpx.CodeNotFound, http.StatusNotFound, "order not found"))
		return
	}
	if o.CustomerID != sess.CustomerID {
		httpx.WriteError(w, r, httpx.New(httpx.CodeForbidden, http.StatusForbidden, "not your order"))
		return
	}
	items, _ := h.ords.GetItems(r.Context(), sess.StoreID, id)
	events, _ := h.ords.GetEvents(r.Context(), sess.StoreID, id)
	httpx.WriteJSON(w, http.StatusOK, orders.DetailToDTO(o, items, events))
}

func mapOrderErr(err error) *httpx.Error {
	switch {
	case errors.Is(err, orders.ErrInsufficientStock):
		return httpx.New(httpx.CodeQuoteMismatch, http.StatusConflict, "insufficient stock")
	case errors.Is(err, orders.ErrPickupSlotFull):
		return httpx.New(httpx.CodePickupSlotFull, http.StatusConflict, "pickup slot full")
	case errors.Is(err, pricing.ErrQuoteExpired):
		return httpx.New(httpx.CodeQuoteExpired, http.StatusGone, "quote expired")
	default:
		return httpx.New(httpx.CodeConflict, http.StatusConflict, err.Error())
	}
}

// summaryToCart 把 quote 摘要还原为订单项快照输入。
// 注：完整快照需要菜单名称；这里用最小字段，订单项 sku_name/product_name 由 P5/P6 完善。
func summaryToCart(q pricing.QuoteSummary) []orders.LineItemSnapshot {
	out := make([]orders.LineItemSnapshot, 0, len(q.Lines))
	for _, l := range q.Lines {
		out = append(out, orders.LineItemSnapshot{
			SKUID: l.SKUID, UnitPriceCents: l.UnitPriceCents, Quantity: l.Quantity,
			OptionIDs: l.OptionIDs, LineAmountCents: l.LineAmountCents,
		})
	}
	return out
}

func truncate(s string, max int) string {
	if len(s) > max {
		return s[:max]
	}
	return s
}
