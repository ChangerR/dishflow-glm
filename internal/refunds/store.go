// Package refunds 实现全额退款意图、回调确认与扣积分幂等（PRD §14.3）。
package refunds

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Store 退款持久化。
type Store struct {
	db *sql.DB
}

// NewStore 创建退款存储。
func NewStore(db *sql.DB) *Store { return &Store{db: db} }

// Refund 退款主体。
type Refund struct {
	ID           int64
	StoreID      int64
	OrderID      int64
	PaymentID    int64
	AmountCents  int64
	Status       string
	RefundNo     string
	RefundIDWX   string
	Reason       string
	TriggerKind  string
	MockRefund   bool
	CreatedAt    time.Time
	SucceededAt  sql.NullTime
}

// CreateInput 退款意图输入。
type CreateInput struct {
	StoreID    int64
	OrderID    int64
	PaymentID  int64
	AmountCents int64
	Reason     string
	TriggerKind string // CUSTOMER_AUTO / CUSTOMER_REVIEW / STAFF_MANUAL
	Mock       bool
}

// Create 落本地退款意图（PRD §14.3：先落本地再调微信）。
// 一单最多一个有效退款意图 + 唯一商户退款号。
func (s *Store) Create(ctx context.Context, in CreateInput) (int64, error) {
	if in.AmountCents <= 0 {
		return 0, errors.New("refund amount must be positive")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	// 检查是否已有有效退款意图。
	var cnt int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM refunds WHERE order_id=? AND status IN ('CREATED','PROCESSING')`, in.OrderID).Scan(&cnt); err != nil {
		return 0, err
	}
	if cnt > 0 {
		return 0, ErrExistingRefund
	}
	refundNo := fmt.Sprintf("RF%d%d", in.OrderID, time.Now().UnixNano())
	mock := 0
	if in.Mock {
		mock = 1
	}
	res, err := tx.ExecContext(ctx,
		`INSERT INTO refunds (store_id, order_id, payment_id, amount_cents, status, refund_no, reason, trigger_kind, mock_refund)
		 VALUES (?,?,?,?, 'CREATED', ?, ?, ?, ?)`,
		in.StoreID, in.OrderID, in.PaymentID, in.AmountCents, refundNo, in.Reason, in.TriggerKind, mock)
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	return id, tx.Commit()
}

// MarkSuccess 幂等标记退款成功（PRD §14.3：只有微信 SUCCESS 才推进）。
// 扣回本单积分且只执行一次（积分回扣在 P6 完整实现，此处只标记退款）。
func (s *Store) MarkSuccess(ctx context.Context, tx *sql.Tx, storeID, refundID int64, refundIDWX, providerEventID string) error {
	var status string
	err := tx.QueryRowContext(ctx,
		`SELECT status FROM refunds WHERE id=? AND store_id=? FOR UPDATE`, refundID, storeID).Scan(&status)
	if err != nil {
		return err
	}
	if status == "SUCCESS" {
		return nil // 幂等。
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE refunds SET status='SUCCESS', succeeded_at=UTC_TIMESTAMP(3), refund_id_wx=? WHERE id=? AND store_id=?`,
		refundIDWX, refundID, storeID); err != nil {
		return err
	}
	if providerEventID != "" {
		if _, err := tx.ExecContext(ctx,
			`INSERT IGNORE INTO webhook_events (store_id, provider, provider_event_id, resource_type, resource_id, result)
			 VALUES (?, 'wechat_refund', ?, 'refund', ?, 'PROCESSED')`,
			storeID, providerEventID, fmt.Sprintf("%d", refundID)); err != nil {
			return err
		}
	}
	return nil
}

// GetByOrder 取订单当前有效退款。
func (s *Store) GetByOrder(ctx context.Context, storeID, orderID int64) (Refund, error) {
	var r Refund
	var status, no, wxID, reason, trigger string
	var mock int
	err := s.db.QueryRowContext(ctx,
		`SELECT id, store_id, order_id, payment_id, amount_cents, status, refund_no, refund_id_wx,
		        reason, trigger_kind, mock_refund, created_at, succeeded_at
		 FROM refunds WHERE order_id=? AND store_id=? ORDER BY id DESC LIMIT 1`, orderID, storeID).
		Scan(&r.ID, &r.StoreID, &r.OrderID, &r.PaymentID, &r.AmountCents, &status, &no, &wxID,
			&reason, &trigger, &mock, &r.CreatedAt, &r.SucceededAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Refund{}, sql.ErrNoRows
	}
	r.Status, r.RefundNo, r.RefundIDWX, r.Reason, r.TriggerKind = status, no, wxID, reason, trigger
	r.MockRefund = mock == 1
	return r, err
}

// ErrExistingRefund 已有有效退款意图。
var ErrExistingRefund = errors.New("REFUND_CONFLICT: existing refund intent")
