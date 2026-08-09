package orders

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// GetByID 取订单（含门店/顾客隔离校验）。
func (s *Store) GetByID(ctx context.Context, storeID, orderID int64) (Order, error) {
	var o Order
	var scenario, state string
	var pickupType string
	var scheduledFor sql.NullTime
	err := s.db.QueryRowContext(ctx,
		`SELECT id, store_id, customer_id, order_no, pickup_no, pickup_business_date,
		        scenario, dining_table_id, table_label, pickup_type, scheduled_for,
		        item_amount_cents, packaging_fee_cents, discount_cents, payable_cents, paid_cents, refunded_cents,
		        promotion_id, customer_coupon_id, remark, fulfillment_state, version,
		        quote_token, created_at, paid_at, expires_at
		 FROM orders WHERE id = ? AND store_id = ?`, orderID, storeID).
		Scan(&o.ID, &o.StoreID, &o.CustomerID, &o.OrderNo, &o.PickupNo, &o.PickupBusinessDate,
			&scenario, &o.DiningTableID, &o.TableLabel, &pickupType, &scheduledFor,
			&o.ItemAmountCents, &o.PackagingFeeCents, &o.DiscountCents, &o.PayableCents, &o.PaidCents, &o.RefundedCents,
			&o.PromotionID, &o.CustomerCouponID, &o.Remark, &state, &o.Version,
			&o.QuoteToken, &o.CreatedAt, &o.PaidAt, &o.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Order{}, sql.ErrNoRows
	}
	o.Scenario = scenario
	o.FulfillmentState = State(state)
	o.PickupType = pickupType
	o.ScheduledFor = scheduledFor
	return o, err
}

// ListByCustomer 顾客订单列表（PRD §4.9），按状态过滤。
func (s *Store) ListByCustomer(ctx context.Context, storeID, customerID int64, statusFilter string, limit, offset int) ([]Order, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	q := `SELECT id, store_id, customer_id, order_no, pickup_no, pickup_business_date,
	             scenario, dining_table_id, table_label, pickup_type, scheduled_for,
	             item_amount_cents, packaging_fee_cents, discount_cents, payable_cents, paid_cents, refunded_cents,
	             promotion_id, customer_coupon_id, remark, fulfillment_state, version,
	             quote_token, created_at, paid_at, expires_at
	      FROM orders WHERE store_id = ? AND customer_id = ?`
	args := []any{storeID, customerID}
	if statusFilter != "" {
		q += ` AND fulfillment_state = ?`
		args = append(args, statusFilter)
	}
	q += ` ORDER BY id DESC LIMIT ? OFFSET ?`
	args = append(args, limit, offset)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanOrders(rows)
}

func scanOrders(rows *sql.Rows) ([]Order, error) {
	var out []Order
	for rows.Next() {
		var o Order
		var scenario, state, pickupType string
		var scheduledFor sql.NullTime
		if err := rows.Scan(&o.ID, &o.StoreID, &o.CustomerID, &o.OrderNo, &o.PickupNo, &o.PickupBusinessDate,
			&scenario, &o.DiningTableID, &o.TableLabel, &pickupType, &scheduledFor,
			&o.ItemAmountCents, &o.PackagingFeeCents, &o.DiscountCents, &o.PayableCents, &o.PaidCents, &o.RefundedCents,
			&o.PromotionID, &o.CustomerCouponID, &o.Remark, &state, &o.Version,
			&o.QuoteToken, &o.CreatedAt, &o.PaidAt, &o.ExpiresAt); err != nil {
			return nil, err
		}
		o.Scenario = scenario
		o.FulfillmentState = State(state)
		o.PickupType = pickupType
		o.ScheduledFor = scheduledFor
		out = append(out, o)
	}
	return out, rows.Err()
}

// TransitionInput 状态推进输入。
type TransitionInput struct {
	StoreID         int64
	OrderID         int64
	To              State
	Actor           string // customer/staff/system
	ActorUserID     int64  // 店员时填
	ExpectedVersion int64  // 乐观锁（PRD §6.1）
}

// Transition 乐观锁推进订单状态（PRD §6.1/§6.2）。
// 两人同时操作只允许一个成功。
func (s *Store) Transition(ctx context.Context, in TransitionInput) (Order, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Order{}, err
	}
	defer func() { _ = tx.Rollback() }()

	var curState string
	var version int64
	err = tx.QueryRowContext(ctx,
		`SELECT fulfillment_state, version FROM orders WHERE id = ? AND store_id = ? FOR UPDATE`,
		in.OrderID, in.StoreID).Scan(&curState, &version)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Order{}, sql.ErrNoRows
		}
		return Order{}, err
	}
	if version != in.ExpectedVersion {
		return Order{}, ErrVersionConflict
	}
	if !CanTransition(State(curState), in.To, in.Actor) {
		return Order{}, fmt.Errorf("%w: %s → %s (actor %s)", ErrInvalidTransition, curState, in.To, in.Actor)
	}
	res, err := tx.ExecContext(ctx,
		`UPDATE orders SET fulfillment_state = ?, version = version + 1 WHERE id = ? AND version = ?`,
		string(in.To), in.OrderID, in.ExpectedVersion)
	if err != nil {
		return Order{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Order{}, ErrVersionConflict
	}
	// 时间戳推进（接单/完成等）。
	if in.To == StateAccepted {
		tx.ExecContext(ctx, `UPDATE orders SET accepted_at = UTC_TIMESTAMP(3) WHERE id = ? AND accepted_at IS NULL`, in.OrderID)
	}
	if in.To == StateCompleted {
		tx.ExecContext(ctx, `UPDATE orders SET completed_at = UTC_TIMESTAMP(3) WHERE id = ? AND completed_at IS NULL`, in.OrderID)
	}
	// 事件。
	actorType := "SYSTEM"
	if in.Actor == "customer" {
		actorType = "CUSTOMER"
	} else if in.Actor == "staff" {
		actorType = "STAFF"
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO order_events (order_id, store_id, event_type, from_state, to_state, actor_type, actor_id, summary)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		in.OrderID, in.StoreID, "state."+string(in.To), curState, string(in.To), actorType, nullInt64(in.ActorUserID),
		fmt.Sprintf("%s → %s", curState, in.To)); err != nil {
		return Order{}, err
	}
	if err := tx.Commit(); err != nil {
		return Order{}, err
	}
	return s.GetByID(ctx, in.StoreID, in.OrderID)
}

// 版本冲突。
var ErrVersionConflict = errors.New("STATE_CONFLICT")
