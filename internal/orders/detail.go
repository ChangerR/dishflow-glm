package orders

import (
	"context"
	"database/sql"
	"encoding/json"
)

// GetItems 取订单项快照（PRD §4.9）。
func (s *Store) GetItems(ctx context.Context, storeID, orderID int64) ([]map[string]any, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, sku_id, product_id, sku_name, product_name, unit_price_cents, quantity, options_snapshot, packaging_fee_cents, line_amount_cents
		 FROM order_items WHERE order_id = ? AND store_id = ? ORDER BY id`, orderID, storeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var id, sku, product, unit, lineAmt, packaging int64
		var qty int
		var skuName, productName, optionsJSON string
		if err := rows.Scan(&id, &sku, &product, &skuName, &productName, &unit, &qty, &optionsJSON, &packaging, &lineAmt); err != nil {
			return nil, err
		}
		var options any
		_ = unmarshalJSON(optionsJSON, &options)
		out = append(out, map[string]any{
			"id": id, "sku_id": sku, "product_id": product, "sku_name": skuName, "product_name": productName,
			"unit_price_cents": unit, "quantity": qty, "options": options,
			"packaging_fee_cents": packaging, "line_amount_cents": lineAmt,
		})
	}
	return out, rows.Err()
}

// GetEvents 取订单事件时间线（PRD §4.9/§6.1）。
func (s *Store) GetEvents(ctx context.Context, storeID, orderID int64) ([]map[string]any, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, event_type, from_state, to_state, actor_type, actor_id, summary, created_at
		 FROM order_events WHERE order_id = ? AND store_id = ? ORDER BY id`, orderID, storeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var id int64
		var eventType, actorType, summary, created string
		var fromState, toState sql.NullString
		var actorID sql.NullInt64
		if err := rows.Scan(&id, &eventType, &fromState, &toState, &actorType, &actorID, &summary, &created); err != nil {
			return nil, err
		}
		entry := map[string]any{
			"id": id, "event_type": eventType, "actor_type": actorType,
			"summary": summary, "created_at": created,
		}
		if fromState.Valid {
			entry["from_state"] = fromState.String
		}
		if toState.Valid {
			entry["to_state"] = toState.String
		}
		if actorID.Valid {
			entry["actor_id"] = actorID.Int64
		}
		out = append(out, entry)
	}
	return out, rows.Err()
}

// BoardColumn 工作台一列。
type BoardColumn struct {
	State  State    `json:"state"`
	Orders []OrderDTO `json:"orders"`
}

// Board 工作台（PAID/ACCEPTED/PREPARING/READY 四列，PRD §6.1）。
func (s *Store) Board(ctx context.Context, storeID int64) ([]BoardColumn, error) {
	states := []State{StatePaid, StateAccepted, StatePreparing, StateReady}
	cols := make([]BoardColumn, 0, len(states))
	for _, st := range states {
		rows, err := s.db.QueryContext(ctx,
			`SELECT id, store_id, customer_id, order_no, pickup_no, pickup_business_date,
			        scenario, dining_table_id, table_label, pickup_type, scheduled_for,
			        item_amount_cents, packaging_fee_cents, discount_cents, payable_cents, paid_cents, refunded_cents,
			        promotion_id, customer_coupon_id, remark, fulfillment_state, version,
			        quote_token, created_at, paid_at, expires_at
			 FROM orders WHERE store_id = ? AND fulfillment_state = ?
			 ORDER BY (pickup_type='IMMEDIATE') DESC, scheduled_for ASC, id ASC LIMIT 100`, storeID, string(st))
		if err != nil {
			return nil, err
		}
		ords, err := scanOrders(rows)
		rows.Close()
		if err != nil {
			return nil, err
		}
		col := BoardColumn{State: st, Orders: []OrderDTO{}}
		for _, o := range ords {
			col.Orders = append(col.Orders, ToDTO(o))
		}
		cols = append(cols, col)
	}
	return cols, nil
}

func unmarshalJSON(s string, v any) error {
	return json.Unmarshal([]byte(s), v)
}
