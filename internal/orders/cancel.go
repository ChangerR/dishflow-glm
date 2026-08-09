package orders

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// CancelInput 顾客取消订单输入（PRD §4.10）。
type CancelInput struct {
	StoreID    int64
	OrderID    int64
	CustomerID int64
	Actor      string // customer / system
}

// CancelResult 取消结果。
type CancelResult struct {
	NewState         State
	NeedRefund       bool   // 是否需要发起退款
	RefundTriggerKind string // CUSTOMER_AUTO / CUSTOMER_REVIEW / 不需退款时空
}

// Cancel 处理顾客取消逻辑（PRD §4.10）：
//   - PENDING_PAYMENT：直接取消，释放库存/容量。
//   - PAID 且门店未接单：取消 + 自动发起全额退款（CUSTOMER_AUTO）。
//   - ACCEPTED：进入 CANCEL_REQUESTED，待人工审核。
//   - CANCEL_REQUESTED / REFUNDING：返回处理中（不重复）。
//   - PREPARING 及以后：顾客不能自行取消。
func (s *Store) Cancel(ctx context.Context, in CancelInput) (CancelResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return CancelResult{}, err
	}
	defer func() { _ = tx.Rollback() }()

	// 行锁取订单，校验属主。
	var state string
	var version int64
	var customerID int64
	err = tx.QueryRowContext(ctx,
		`SELECT fulfillment_state, version, customer_id FROM orders WHERE id=? AND store_id=? FOR UPDATE`,
		in.OrderID, in.StoreID).Scan(&state, &version, &customerID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return CancelResult{}, sql.ErrNoRows
		}
		return CancelResult{}, err
	}
	if in.Actor == "customer" && customerID != in.CustomerID {
		return CancelResult{}, errors.New("not your order")
	}

	cur := State(state)
	res := CancelResult{}

	switch cur {
	case StatePendingPayment:
		// 直接取消 + 释放库存/容量。
		if err := cancelAndRelease(tx, in.OrderID, in.StoreID, "customer"); err != nil {
			return CancelResult{}, err
		}
		res.NewState = StateCancelled

	case StatePaid:
		// PAID 未接单：取消订单状态→REFUNDING，发起自动退款。
		if _, err := tx.ExecContext(ctx,
			`UPDATE orders SET fulfillment_state='REFUNDING', version=version+1 WHERE id=? AND version=?`,
			in.OrderID, version); err != nil {
			return CancelResult{}, err
		}
		tx.ExecContext(ctx,
			`INSERT INTO order_events (order_id, store_id, event_type, from_state, to_state, actor_type, summary)
			 VALUES (?, ?, 'cancel.request', 'PAID', 'REFUNDING', 'CUSTOMER', '顾客取消，自动发起退款')`,
			in.OrderID, in.StoreID)
		res.NewState = StateRefunding
		res.NeedRefund = true
		res.RefundTriggerKind = "CUSTOMER_AUTO"

	case StateAccepted:
		// 已接单：进入待审核。
		if _, err := tx.ExecContext(ctx,
			`UPDATE orders SET fulfillment_state='CANCEL_REQUESTED', version=version+1 WHERE id=? AND version=?`,
			in.OrderID, version); err != nil {
			return CancelResult{}, err
		}
		tx.ExecContext(ctx,
			`INSERT INTO order_events (order_id, store_id, event_type, from_state, to_state, actor_type, summary)
			 VALUES (?, ?, 'cancel.request', 'ACCEPTED', 'CANCEL_REQUESTED', 'CUSTOMER', '顾客取消申请，待审核')`,
			in.OrderID, in.StoreID)
		res.NewState = StateCancelRequested

	case StateCancelRequested, StateRefunding:
		// 已在处理中，返回当前状态。
		res.NewState = cur

	default:
		// PREPARING/READY/COMPLETED/REFUNDED/CANCELLED：顾客不能自行取消。
		return CancelResult{}, fmt.Errorf("%w: cannot cancel order in state %s as customer", ErrInvalidTransition, state)
	}

	return res, tx.Commit()
}

// cancelAndRelease 取消订单 + 释放库存 + 释放预约容量（幂等）。
func cancelAndRelease(tx *sql.Tx, orderID, storeID int64, actor string) error {
	// 取消订单。
	if _, err := tx.ExecContext(context.Background(),
		`UPDATE orders SET fulfillment_state='CANCELLED', cancelled_at=UTC_TIMESTAMP(3), version=version+1
		 WHERE id=? AND fulfillment_state IN ('PENDING_PAYMENT')`, orderID); err != nil {
		return err
	}
	// 释放库存（reserved → 0，幂等：只释放 RESERVED 状态的预占）。
	if err := releaseInventory(tx, orderID); err != nil {
		return err
	}
	// 释放预约容量（幂等标记）。
	if err := releasePickupCapacity(tx, orderID); err != nil {
		return err
	}
	tx.ExecContext(context.Background(),
		`INSERT INTO order_events (order_id, store_id, event_type, to_state, actor_type, summary)
		 VALUES (?, ?, 'order.cancelled', 'CANCELLED', ?, '取消并释放库存/容量')`,
		orderID, storeID, actorLabel(actor))
	return nil
}

// releaseInventory 释放订单库存预占和已售（幂等）。
// 释放逻辑：
//   - RESERVED 状态的预占 → reserved_qty -= qty
//   - FULFILLED 状态的已售 → sold_qty -= qty（退款释放，PRD §4.10）
func releaseInventory(tx *sql.Tx, orderID int64) error {
	// 释放预占 reserved（RESERVED 状态）。
	if _, err := tx.ExecContext(context.Background(),
		`UPDATE daily_inventory di
		 INNER JOIN inventory_reservations ir ON ir.sku_id=di.sku_id AND ir.business_date=di.business_date AND ir.store_id=di.store_id
		 SET di.reserved_qty = GREATEST(0, di.reserved_qty - ir.quantity)
		 WHERE ir.order_id=? AND ir.state='RESERVED'`, orderID); err != nil {
		return err
	}
	// 释放已售 sold（FULFILLED 状态，退款场景）。
	if _, err := tx.ExecContext(context.Background(),
		`UPDATE daily_inventory di
		 INNER JOIN inventory_reservations ir ON ir.sku_id=di.sku_id AND ir.business_date=di.business_date AND ir.store_id=di.store_id
		 SET di.sold_qty = GREATEST(0, di.sold_qty - ir.quantity)
		 WHERE ir.order_id=? AND ir.state='FULFILLED'`, orderID); err != nil {
		return err
	}
	// 标记预占为 RELEASED（幂等：仅 RESERVED）。
	if _, err := tx.ExecContext(context.Background(),
		`UPDATE inventory_reservations SET state='RELEASED' WHERE order_id=? AND state='RESERVED'`, orderID); err != nil {
		return err
	}
	// 已售的也标记为 RELEASED（退款释放已售）。
	if _, err := tx.ExecContext(context.Background(),
		`UPDATE inventory_reservations SET state='RELEASED' WHERE order_id=? AND state='FULFILLED'`, orderID); err != nil {
		return err
	}
	return nil
}

// releasePickupCapacity 释放预约容量（幂等标记，PRD §4.6/§4.10）。
func releasePickupCapacity(tx *sql.Tx, orderID int64) error {
	// 标记释放时间（幂等：已标记则跳过）。
	res, err := tx.ExecContext(context.Background(),
		`UPDATE orders SET pickup_capacity_released_at=UTC_TIMESTAMP(3)
		 WHERE id=? AND pickup_capacity_released_at IS NULL AND scheduled_for IS NOT NULL`, orderID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return nil // 无预约 或 已释放。
	}
	// 容量 -1。
	tx.ExecContext(context.Background(),
		`UPDATE pickup_slot_capacity psc
		 INNER JOIN orders o ON o.store_id=psc.store_id AND o.scheduled_for=psc.scheduled_for
		 SET psc.reserved_orders = GREATEST(0, psc.reserved_orders - 1)
		 WHERE o.id=? AND o.pickup_capacity_released_at IS NOT NULL`, orderID)
	return nil
}

// MarkRefunded 标记订单为 REFUNDED（退款成功后由 Worker/回调调用，幂等）。
// 同时释放库存（退款也应释放预占/已售）和预约容量（如未释放）。
func (s *Store) MarkRefunded(ctx context.Context, storeID, orderID int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var state string
	var version int64
	err = tx.QueryRowContext(ctx,
		`SELECT fulfillment_state, version FROM orders WHERE id=? AND store_id=? FOR UPDATE`,
		orderID, storeID).Scan(&state, &version)
	if err != nil {
		return err
	}
	if state == "REFUNDED" {
		return nil // 幂等。
	}
	if state != "REFUNDING" {
		return fmt.Errorf("%w: cannot mark REFUNDED from %s", ErrInvalidTransition, state)
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE orders SET fulfillment_state='REFUNDED', refunded_cents=payable_cents, version=version+1 WHERE id=?`,
		orderID); err != nil {
		return err
	}
	// 释放库存（退款释放已售/预占）。
	if err := releaseInventory(tx, orderID); err != nil {
		return err
	}
	// 释放预约容量。
	if err := releasePickupCapacity(tx, orderID); err != nil {
		return err
	}
	tx.ExecContext(ctx,
		`INSERT INTO order_events (order_id, store_id, event_type, from_state, to_state, actor_type, summary)
		 VALUES (?, ?, 'order.refunded', 'REFUNDING', 'REFUNDED', 'SYSTEM', '退款成功')`,
		orderID, storeID)
	return tx.Commit()
}

// StaffRefund 店长/店主主动退款（ACCEPTED/PREPARING/READY/COMPLETED → REFUNDING）。
func (s *Store) StaffRefund(ctx context.Context, storeID, orderID, staffUserID int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var state string
	var version int64
	err = tx.QueryRowContext(ctx,
		`SELECT fulfillment_state, version FROM orders WHERE id=? AND store_id=? FOR UPDATE`,
		orderID, storeID).Scan(&state, &version)
	if err != nil {
		return err
	}
	cur := State(state)
	if !CanTransition(cur, StateRefunding, "staff") {
		return fmt.Errorf("%w: cannot refund from %s", ErrInvalidTransition, state)
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE orders SET fulfillment_state='REFUNDING', version=version+1 WHERE id=? AND version=?`,
		orderID, version); err != nil {
		return err
	}
	tx.ExecContext(ctx,
		`INSERT INTO order_events (order_id, store_id, event_type, from_state, to_state, actor_type, actor_id, summary)
		 VALUES (?, ?, 'staff.refund', ?, 'REFUNDING', 'STAFF', ?, '门店主动退款')`,
		orderID, storeID, state, staffUserID)
	return tx.Commit()
}

// ReviewCancelRequest 审核顾客取消申请（PRD §6.4）。
// approve=true：通过→进入 REFUNDING（发起退款）。
// approve=false：驳回→恢复 ACCEPTED。
func (s *Store) ReviewCancelRequest(ctx context.Context, storeID, orderID, staffUserID int64, approve bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var state string
	var version int64
	err = tx.QueryRowContext(ctx,
		`SELECT fulfillment_state, version FROM orders WHERE id=? AND store_id=? FOR UPDATE`,
		orderID, storeID).Scan(&state, &version)
	if err != nil {
		return err
	}
	if state != "CANCEL_REQUESTED" {
		return fmt.Errorf("%w: order not in CANCEL_REQUESTED (was %s)", ErrInvalidTransition, state)
	}
	toState := "REFUNDING"
	summary := "审核通过，发起退款"
	if !approve {
		toState = "ACCEPTED"
		summary = "审核驳回，恢复接单"
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE orders SET fulfillment_state=?, version=version+1 WHERE id=? AND version=?`,
		toState, orderID, version); err != nil {
		return err
	}
	tx.ExecContext(ctx,
		`INSERT INTO order_events (order_id, store_id, event_type, from_state, to_state, actor_type, actor_id, summary)
		 VALUES (?, ?, 'cancel.review', 'CANCEL_REQUESTED', ?, 'STAFF', ?, ?)`,
		orderID, storeID, toState, staffUserID, summary)
	return tx.Commit()
}

func actorLabel(actor string) string {
	if actor == "customer" {
		return "CUSTOMER"
	}
	if actor == "system" {
		return "SYSTEM"
	}
	return "STAFF"
}

var _ = time.Now
