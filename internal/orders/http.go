package orders

import (
	"encoding/json"
)

// OrderDTO 订单对外 DTO。
type OrderDTO struct {
	ID                int64           `json:"id"`
	StoreID           int64           `json:"store_id"`
	OrderNo           string          `json:"order_no"`
	PickupNo          *int            `json:"pickup_no,omitempty"`
	Scenario          string          `json:"scenario"`
	TableLabel        string          `json:"table_label,omitempty"`
	PickupType        string          `json:"pickup_type"`
	ScheduledFor      string          `json:"scheduled_for,omitempty"`
	ItemAmountCents   int64           `json:"item_amount_cents"`
	PackagingFeeCents int64           `json:"packaging_fee_cents"`
	DiscountCents     int64           `json:"discount_cents"`
	PayableCents      int64           `json:"payable_cents"`
	PaidCents         int64           `json:"paid_cents"`
	Remark            string          `json:"remark"`
	FulfillmentState  string          `json:"fulfillment_state"`
	Version           int64           `json:"version"`
	CreatedAt         string          `json:"created_at"`
	PaidAt            string          `json:"paid_at,omitempty"`
}

// ToDTO 转 DTO。
func ToDTO(o Order) OrderDTO {
	dto := OrderDTO{
		ID: o.ID, StoreID: o.StoreID, OrderNo: o.OrderNo, Scenario: o.Scenario,
		TableLabel: o.TableLabel, PickupType: o.PickupType,
		ItemAmountCents: o.ItemAmountCents, PackagingFeeCents: o.PackagingFeeCents,
		DiscountCents: o.DiscountCents, PayableCents: o.PayableCents, PaidCents: o.PaidCents,
		Remark: o.Remark, FulfillmentState: string(o.FulfillmentState), Version: o.Version,
		CreatedAt: o.CreatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
	}
	if o.PickupNo.Valid {
		v := int(o.PickupNo.Int64)
		dto.PickupNo = &v
	}
	if o.ScheduledFor.Valid {
		dto.ScheduledFor = o.ScheduledFor.Time.Format("2006-01-02T15:04:05Z07:00")
	}
	if o.PaidAt.Valid {
		dto.PaidAt = o.PaidAt.Time.Format("2006-01-02T15:04:05Z07:00")
	}
	return dto
}

// OrderDetailDTO 含订单项快照与事件。
type OrderDetailDTO struct {
	OrderDTO
	Items  []map[string]any `json:"items"`
	Events []map[string]any `json:"events"`
}

// DetailToDTO 转详情 DTO（含快照与事件，PRD §4.9）。
func DetailToDTO(o Order, items []map[string]any, events []map[string]any) OrderDetailDTO {
	return OrderDetailDTO{OrderDTO: ToDTO(o), Items: items, Events: events}
}

// suppress unused
var _ = json.Marshal
