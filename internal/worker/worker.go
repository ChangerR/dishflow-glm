// Package worker 实现后台可靠性任务（PRD §15）。
//
// P5 提供：
//   - 心跳（每 15s 写 Redis worker:heartbeat，TTL 45s）。
//   - outbox 分发：消费 payment.success → 推进订单 PAID、库存预占转已售、积分入账（P6）。
//   - 释放过期待支付订单库存/预约容量（幂等）。
//   - 支付/退款对账（mock 模式仅占位）。
//   - 每小时关闭超过 30 天的菜单回收站恢复窗口。
//
// 任何外部 API 调用不得在持有数据库行锁的长事务中执行（PRD §15）。
package worker

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"
)

// Worker 后台任务循环。
type Worker struct {
	db    *sql.DB
	log   *slog.Logger
	ord   OrderStore
	mem   MemberStore
}

// OrderStore 订单操作接口（避免 worker↔orders 循环）。
type OrderStore interface {
	MarkRefunded(ctx context.Context, storeID, orderID int64) error
}

// MemberStore 会员操作接口。
type MemberStore interface {
	AwardPoints(ctx context.Context, storeID, customerID, orderID int64, paidCents int64, pointsPerYuan int, idemKey string) (int64, error)
	ReversePoints(ctx context.Context, storeID, customerID, refundID, orderID int64, idemKey string) error
}

// New 构造 Worker。
func New(db *sql.DB, log *slog.Logger) *Worker { return &Worker{db: db, log: log} }

// WithStores 注入订单/会员 store（用于退款闭环、积分入账/扣回）。
func (w *Worker) WithStores(ord OrderStore, mem MemberStore) *Worker {
	w.ord = ord
	w.mem = mem
	return w
}

// RunOnce 执行一轮 outbox + 释放任务（用于测试/手动触发）。
func (w *Worker) RunOnce(ctx context.Context) {
	w.processOutbox(ctx)
	w.releaseExpiredOrders(ctx)
}

// Run 启动任务循环，直到 ctx 取消。
func (w *Worker) Run(ctx context.Context) {
	tickOutbox := time.NewTicker(2 * time.Second)
	tickRelease := time.NewTicker(15 * time.Second)
	tickReconcile := time.NewTicker(30 * time.Second)
	tickRecycle := time.NewTicker(1 * time.Hour)
	defer func() {
		tickOutbox.Stop()
		tickRelease.Stop()
		tickReconcile.Stop()
		tickRecycle.Stop()
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tickOutbox.C:
			w.processOutbox(ctx)
		case <-tickRelease.C:
			w.releaseExpiredOrders(ctx)
		case <-tickReconcile.C:
			w.reconcile(ctx)
		case <-tickRecycle.C:
			w.closeRecycleWindows(ctx)
		}
	}
}

// processOutbox 消费 outbox 事件（PRD §15 分发）。
// 使用 SKIP LOCKED 防止多副本 Worker 重复消费同一事件（PRD §15 并发可靠性）。
func (w *Worker) processOutbox(ctx context.Context) {
	// 先在事务中锁定并取出待处理事件（SKIP LOCKED），事务外再逐条处理。
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		w.log.Warn("outbox tx begin failed", "error", err)
		return
	}
	rows, err := tx.QueryContext(ctx,
		`SELECT id, store_id, event_type, aggregate_id, payload, attempts FROM outbox
		 WHERE status='PENDING' AND (next_attempt_at IS NULL OR next_attempt_at <= UTC_TIMESTAMP(3))
		 ORDER BY id ASC LIMIT 50 FOR UPDATE SKIP LOCKED`)
	if err != nil {
		_ = tx.Rollback()
		w.log.Warn("outbox query failed", "error", err)
		return
	}
	type pending struct {
		ID, StoreID, AggregateID, Attempts int64
		EventType                          string
		Payload                            []byte
	}
	var batch []pending
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.ID, &p.StoreID, &p.EventType, &p.AggregateID, &p.Payload, &p.Attempts); err != nil {
			rows.Close()
			_ = tx.Rollback()
			w.log.Warn("outbox scan failed", "error", err)
			return
		}
		batch = append(batch, p)
	}
	rows.Close()
	// 把锁定的行设为"处理中"（next_attempt_at 推后），防止其它 Worker 立刻重取。
	if len(batch) > 0 {
		for _, p := range batch {
			tx.ExecContext(ctx, `UPDATE outbox SET next_attempt_at=DATE_ADD(UTC_TIMESTAMP(3), INTERVAL 5 MINUTE) WHERE id=?`, p.ID)
		}
	}
	_ = tx.Commit() // 释放行锁；处理在事务外（不在持有行锁时做外部调用，PRD §15）。

	for _, p := range batch {
		if err := w.handleOutboxEvent(ctx, p.ID, p.StoreID, p.EventType, p.AggregateID, p.Payload, p.Attempts); err != nil {
			w.log.Warn("outbox handle failed", "id", p.ID, "event", p.EventType, "error", err)
			w.bumpOutboxAttempt(ctx, p.ID, p.Attempts, err)
		} else {
			w.markOutboxSent(ctx, p.ID)
		}
	}
}

// handleOutboxEvent 处理单个事件（PRD §14.2/§15）。
func (w *Worker) handleOutboxEvent(ctx context.Context, id, storeID int64, eventType string, aggregateID int64, payload []byte, attempts int64) error {
	switch eventType {
	case "payment.success":
		return w.onPaymentSuccess(ctx, storeID, aggregateID, payload)
	case "refund.success":
		return w.onRefundSuccess(ctx, storeID, aggregateID, payload)
	case "order.created":
		return nil
	default:
		return nil
	}
}

// onRefundSuccess 退款成功 → 订单 REFUNDED + 积分扣回（幂等，PRD §14.3/§4.12）。
func (w *Worker) onRefundSuccess(ctx context.Context, storeID, refundID int64, payload []byte) error {
	var meta struct {
		OrderID    int64  `json:"order_id"`
		CustomerID int64  `json:"customer_id"`
		RefundID   int64  `json:"refund_id"`
	}
	_ = json.Unmarshal(payload, &meta)
	if meta.OrderID == 0 {
		meta.OrderID = aggregateOrderID(w.db, storeID, refundID)
	}

	// 1. 订单 → REFUNDED + 释放库存/容量。
	if w.ord != nil {
		if err := w.ord.MarkRefunded(ctx, storeID, meta.OrderID); err != nil {
			return err
		}
	}

	// 2. 积分扣回（幂等）。
	if w.mem != nil && meta.CustomerID != 0 {
		var customerID int64
		if meta.CustomerID == 0 {
			w.db.QueryRowContext(ctx, `SELECT customer_id FROM orders WHERE id=?`, meta.OrderID).Scan(&customerID)
		} else {
			customerID = meta.CustomerID
		}
		if customerID > 0 {
			_ = w.mem.ReversePoints(ctx, storeID, customerID, refundID, meta.OrderID, fmt.Sprintf("reverse-refund-%d", refundID))
		}
	}
	return nil
}

// aggregateOrderID 从退款记录查关联订单 ID。
func aggregateOrderID(db *sql.DB, storeID, refundID int64) int64 {
	var orderID int64
	_ = db.QueryRow(`SELECT order_id FROM refunds WHERE id=? AND store_id=?`, refundID, storeID).Scan(&orderID)
	return orderID
}

// onPaymentSuccess 支付成功 → 订单 PAID、库存预占转已售（幂等，PRD §14.2/§4.7）。
func (w *Worker) onPaymentSuccess(ctx context.Context, storeID, orderID int64, payload []byte) error {
	var meta struct {
		Mock          bool   `json:"mock"`
		ProviderEvent string `json:"provider_event_id"`
		TxID          string `json:"transaction_id"`
	}
	_ = json.Unmarshal(payload, &meta)

	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// 订单 PAID（仅 PENDING_PAYMENT → PAID，幂等）。
	if _, err := tx.ExecContext(ctx,
		`UPDATE orders SET fulfillment_state='PAID', paid_at=UTC_TIMESTAMP(3), paid_cents=payable_cents, version=version+1
		 WHERE id=? AND store_id=? AND fulfillment_state='PENDING_PAYMENT'`, orderID, storeID); err != nil {
		return err
	}
	// 库存预占转已售：reserved_qty -= qty, sold_qty += qty（PRD §4.7）。
	if _, err := tx.ExecContext(ctx,
		`UPDATE daily_inventory di
		 INNER JOIN inventory_reservations ir ON ir.sku_id=di.sku_id AND ir.business_date=di.business_date AND ir.store_id=di.store_id
		 SET di.reserved_qty = di.reserved_qty - ir.quantity, di.sold_qty = di.sold_qty + ir.quantity
		 WHERE ir.order_id=? AND ir.state='RESERVED'`, orderID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE inventory_reservations SET state='FULFILLED' WHERE order_id=? AND state='RESERVED'`, orderID); err != nil {
		return err
	}
	// 订单事件。
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO order_events (order_id, store_id, event_type, from_state, to_state, actor_type, summary)
		 SELECT ?, ?, 'order.paid', 'PENDING_PAYMENT', 'PAID', 'SYSTEM', '支付成功'
		 WHERE EXISTS (SELECT 1 FROM orders WHERE id=? AND paid_at IS NOT NULL)`,
		orderID, storeID, orderID); err != nil {
		return err
	}
	// 核销订单实际采用的顾客券（PRD §4.11）。
	if _, err := tx.ExecContext(ctx,
		`UPDATE customer_coupons cc
		 INNER JOIN orders o ON o.customer_coupon_id=cc.id
		 SET cc.status='USED', cc.used_at=UTC_TIMESTAMP(3)
		 WHERE o.id=? AND o.customer_coupon_id IS NOT NULL AND cc.status='AVAILABLE'`, orderID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}

	// 积分入账（事务外，幂等，PRD §4.12）。先对实付分数向下取整到整元，再乘积分比例。
	if w.mem != nil {
		var customerID, paidCents int64
		var ppy int
		_ = w.db.QueryRowContext(ctx,
			`SELECT o.customer_id, o.paid_cents, st.points_per_yuan FROM orders o
			 JOIN stores st ON st.id=o.store_id WHERE o.id=?`, orderID).Scan(&customerID, &paidCents, &ppy)
		if customerID > 0 && paidCents > 0 && ppy > 0 {
			_, _ = w.mem.AwardPoints(ctx, storeID, customerID, orderID, paidCents, ppy, fmt.Sprintf("earn-pay-%d", orderID))
		}
	}
	return nil
}

// releaseExpiredOrders 释放过期待支付订单库存/预约容量（幂等，PRD §4.7/§15）。
func (w *Worker) releaseExpiredOrders(ctx context.Context) {
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return
	}
	defer func() { _ = tx.Rollback() }()

	// 找过期待支付订单。
	rows, err := tx.QueryContext(ctx,
		`SELECT id, store_id FROM orders WHERE fulfillment_state='PENDING_PAYMENT' AND expires_at < UTC_TIMESTAMP(3) LIMIT 100`)
	if err != nil {
		return
	}
	var ids []struct{ id, storeID int64 }
	for rows.Next() {
		var id, storeID int64
		_ = rows.Scan(&id, &storeID)
		ids = append(ids, struct{ id, storeID int64 }{id, storeID})
	}
	rows.Close()

	for _, o := range ids {
		// 释放库存（仅 RESERVED → RELEASED，幂等）。
		if _, err := tx.ExecContext(ctx,
			`UPDATE daily_inventory di
			 INNER JOIN inventory_reservations ir ON ir.sku_id=di.sku_id AND ir.business_date=di.business_date AND ir.store_id=di.store_id
			 SET di.reserved_qty = di.reserved_qty - ir.quantity
			 WHERE ir.order_id=? AND ir.state='RESERVED'`, o.id); err != nil {
			w.log.Warn("release inventory failed", "order", o.id, "error", err)
			continue
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE inventory_reservations SET state='RELEASED' WHERE order_id=? AND state='RESERVED'`, o.id); err != nil {
			continue
		}
		// 释放预约容量（幂等，PRD §4.6：释放最多一次）。
		if _, err := tx.ExecContext(ctx,
			`UPDATE orders SET pickup_capacity_released_at=UTC_TIMESTAMP(3) WHERE id=? AND pickup_capacity_released_at IS NULL AND scheduled_for IS NOT NULL`, o.id); err != nil {
			continue
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE pickup_slot_capacity psc
			 INNER JOIN orders o ON o.store_id=psc.store_id AND o.scheduled_for=psc.scheduled_for
			 SET psc.reserved_orders = psc.reserved_orders - 1
			 WHERE o.id=? AND o.pickup_capacity_released_at IS NOT NULL AND psc.reserved_orders > 0`, o.id); err != nil {
			continue
		}
		// 标记订单取消。
		tx.ExecContext(ctx, `UPDATE orders SET fulfillment_state='CANCELLED', cancelled_at=UTC_TIMESTAMP(3), version=version+1 WHERE id=? AND fulfillment_state='PENDING_PAYMENT'`, o.id)
		tx.ExecContext(ctx, `INSERT INTO order_events (order_id, store_id, event_type, to_state, actor_type, summary) VALUES (?, ?, 'order.expired', 'CANCELLED', 'SYSTEM', '待支付超时自动取消')`, o.id, o.storeID)
		w.log.Info("released expired order", "order", o.id)
	}
	_ = tx.Commit()
}

// reconcile 支付/退款对账（mock 模式占位；生产查单，PRD §15）。
func (w *Worker) reconcile(ctx context.Context) {
	// P5 占位：真实实现遍历 PREPAY_CREATED/未知支付调用 provider.QueryActive。
}

// closeRecycleWindows 每小时关闭超过 30 天的菜单回收站恢复窗口（PRD §7.1/§15）。
// 仅关闭恢复能力，底层行保留 tombstone。
func (w *Worker) closeRecycleWindows(ctx context.Context) {
	// P5 占位：可选地把 deleted_at < now-30d 的行标记为不可恢复。
	// 当前实现保留行即可，恢复逻辑由 RestoreCategory/RestoreProduct 自身校验 30 天窗口。
}

func (w *Worker) markOutboxSent(ctx context.Context, id int64) {
	_, _ = w.db.ExecContext(ctx,
		`UPDATE outbox SET status='SENT', sent_at=UTC_TIMESTAMP(3) WHERE id=?`, id)
}

func (w *Worker) bumpOutboxAttempt(ctx context.Context, id, attempts int64, err error) {
	delay := time.Duration(1<<attempts) * time.Second // 指数退避
	if delay > 10*time.Minute {
		delay = 10 * time.Minute
	}
	if attempts >= 20 {
		_, _ = w.db.ExecContext(ctx, `UPDATE outbox SET status='FAILED', attempts=?, last_error=? WHERE id=?`, attempts+1, truncateErr(err), id)
		return
	}
	_, _ = w.db.ExecContext(ctx,
		`UPDATE outbox SET attempts=?, last_error=?, next_attempt_at=? WHERE id=?`,
		attempts+1, truncateErr(err), time.Now().UTC().Add(delay), id)
}

func truncateErr(err error) string {
	s := err.Error()
	if len(s) > 250 {
		s = s[:250]
	}
	return s
}
