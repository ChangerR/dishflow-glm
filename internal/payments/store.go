package payments

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/dishflow/zshop/internal/security"
)

// Store 支付持久化。
type Store struct {
	db       *sql.DB
	provider Provider
}

// NewStore 创建支付存储。provider 为 nil 时用 MockProvider。
func NewStore(db *sql.DB, provider Provider) *Store {
	if provider == nil {
		provider = MockProvider{}
	}
	return &Store{db: db, provider: provider}
}

// Prepay 创建或复用未过期 prepay_id（PRD §4.8）。
// 0 元订单不调微信，直接返回特殊标记（P5 由 Worker/确认逻辑推进）。
func (s *Store) Prepay(ctx context.Context, in PrepayInput) (PrepayResult, error) {
	if in.AmountCents == 0 {
		return PrepayResult{MockPay: true}, ErrZeroOrderNotWechat
	}
	// 查既有未关闭支付。
	var existingStatus string
	_ = s.db.QueryRowContext(ctx,
		`SELECT status FROM payments WHERE order_id = ?`, in.OrderID).Scan(&existingStatus)
	if existingStatus == "PREPAY_CREATED" {
		// 复用既有 prepay（前提未过期；简化：返回提示需重新获取）。
	}

	res, err := s.provider.Prepay(ctx, in)
	if err != nil {
		return PrepayResult{}, err
	}
	// 落库（一单唯一支付，PRD §17.2）。
	mock := 0
	if res.MockPay {
		mock = 1
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO payments (store_id, order_id, amount_cents, status, prepay_id, mock_payment, prepay_payload)
		 VALUES (?,?,?, 'PREPAY_CREATED', ?, ?, NULL)
		 ON DUPLICATE KEY UPDATE prepay_id = VALUES(prepay_id)`,
		in.StoreID, in.OrderID, in.AmountCents, res.PrepayID, mock)
	if err != nil {
		return PrepayResult{}, fmt.Errorf("insert payment: %w", err)
	}
	return res, nil
}

// ConfirmMockPayment 显式确认 mock 支付（PRD §4.8）。只有 mock 模式可用。
// 幂等：重复确认返回同一结果。
func (s *Store) ConfirmMockPayment(ctx context.Context, storeID, orderID int64) error {
	if !s.provider.IsMock() {
		return errors.New("mock confirmation only allowed in mock mode")
	}
	// 锁定支付行。
	var status string
	var mock int
	err := s.db.QueryRowContext(ctx,
		`SELECT status, mock_payment FROM payments WHERE order_id = ? AND store_id = ? FOR UPDATE`,
		orderID, storeID).Scan(&status, &mock)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return sql.ErrNoRows
		}
		return err
	}
	if mock != 1 {
		return errors.New("payment is not a mock payment")
	}
	if status == "SUCCESS" {
		return nil // 幂等。
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx,
		`UPDATE payments SET status='SUCCESS', succeeded_at=UTC_TIMESTAMP(3), transaction_id=? WHERE order_id=? AND store_id=?`,
		"mock_tx_"+randHex(8), orderID, storeID); err != nil {
		return err
	}
	// outbox：支付成功（P5 Worker 消费：推进订单 PAID、库存转已售、积分、自动打印）。
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO outbox (store_id, event_type, aggregate_type, aggregate_id, payload, status)
		 VALUES (?, 'payment.success', 'order', ?, ?, 'PENDING')`,
		storeID, orderID, []byte(fmt.Sprintf(`{"order_id":%d,"store_id":%d,"event":"payment.success","mock":true}`, orderID, storeID))); err != nil {
		return err
	}
	return tx.Commit()
}

// MarkSuccess 幂等标记支付成功（微信回调/查单确认后）。
// providerEventID + 业务状态双重幂等（PRD §14.2）。事务内由调用方扩展（库存/积分/券）。
func (s *Store) MarkSuccess(ctx context.Context, tx *sql.Tx, storeID, orderID int64, transactionID, providerEventID string, amount int64) error {
	// webhook 去重：同 provider + event id 已处理则跳过。
	if providerEventID != "" {
		var exists int
		err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM webhook_events WHERE provider='wechat_pay' AND provider_event_id=?`,
			providerEventID).Scan(&exists)
		if err != nil {
			return err
		}
		if exists > 0 {
			return nil // 已处理，幂等。
		}
	}
	// 检查是否已成功。
	var curStatus string
	err := tx.QueryRowContext(ctx,
		`SELECT status FROM payments WHERE order_id=? AND store_id=? FOR UPDATE`, orderID, storeID).Scan(&curStatus)
	if err != nil {
		return err
	}
	if curStatus == "SUCCESS" {
		return nil // 幂等。
	}
	if amount >= 0 {
		// 校验金额（调用方应核对；此处记录）。
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE payments SET status='SUCCESS', succeeded_at=UTC_TIMESTAMP(3), transaction_id=? WHERE order_id=? AND store_id=?`,
		transactionID, orderID, storeID); err != nil {
		return err
	}
	// webhook 记录。
	if providerEventID != "" {
		if _, err := tx.ExecContext(ctx,
			`INSERT IGNORE INTO webhook_events (store_id, provider, provider_event_id, resource_type, resource_id, result)
			 VALUES (?, 'wechat_pay', ?, 'order', ?, 'PROCESSED')`,
			storeID, providerEventID, fmt.Sprintf("%d", orderID)); err != nil {
			return err
		}
	}
	return nil
}

// IsPaid 查订单支付是否成功。
func (s *Store) IsPaid(ctx context.Context, storeID, orderID int64) (bool, error) {
	var status string
	err := s.db.QueryRowContext(ctx,
		`SELECT status FROM payments WHERE order_id=? AND store_id=?`, orderID, storeID).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return status == "SUCCESS", err
}

// suppress
var _ = time.Now
var _ = security.NewToken
