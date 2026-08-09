// Package members 实现顾客会员、入会手机号加密、积分流水与兑券（PRD §4.12/§8.1）。
//
// 安全（PRD §18）：
//   - 手机号加密保存（AES-GCM），同时保存门店范围不可逆 hash、后四位、区号。
//   - 同门店同手机号只能绑定一个顾客；重复入会返回现有会员，不重复发新人礼。
//   - 后台永不返回完整手机号明文。
package members

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/dishflow/zshop/internal/security"
)

// Store 会员域持久化。
type Store struct {
	db  *sql.DB
	enc *security.Encryptor
}

// NewStore 创建会员存储。enc 为 AES-GCM 加密器。
func NewStore(db *sql.DB, enc *security.Encryptor) *Store {
	return &Store{db: db, enc: enc}
}

// Membership 会员。
type Membership struct {
	ID           int64
	StoreID      int64
	CustomerID   int64
	MemberNo     string
	Status       string
	PointsBalance int64
}

// Join 入会（PRD §4.12）：
//  - 验证手机号同意协议。
//  - 服务端调用微信接口验证手机号（P6 真实实现由 phoneCodeExchanger）。
//  - 手机号加密 + hash + 后四位 + 区号。
//  - 同门店同手机号唯一；重复返回现有会员，不重复发新人礼。
//  - 会员号门店范围唯一稳定。
func (s *Store) Join(ctx context.Context, storeID, customerID int64, phone string, countryCode string) (Membership, bool, error) {
	if phone == "" {
		return Membership{}, false, errors.New("phone required")
	}
	if s.enc == nil {
		return Membership{}, false, errors.New("credential encryptor unavailable")
	}
	// hash + 后四位 + 加密。
	hashBytes := security.HashToken(countryCode + phone)
	last4 := phone
	if len(last4) > 4 {
		last4 = last4[len(last4)-4:]
	}
	encPhone, nonce, err := s.enc.Seal([]byte(phone))
	if err != nil {
		return Membership{}, false, err
	}

	// 检查同门店同手机号是否已绑定其它顾客。
	var existingCustomerID int64
	err = s.db.QueryRowContext(ctx,
		`SELECT id FROM customers WHERE store_id=? AND phone_hash=? AND phone_hash<>''`,
		storeID, hashBytes).Scan(&existingCustomerID)
	if err == nil && existingCustomerID != customerID {
		return Membership{}, false, ErrPhoneBound
	}

	// 更新顾客手机号字段。
	if _, err := s.db.ExecContext(ctx,
		`UPDATE customers SET phone_encrypted=?, phone_nonce=?, phone_hash=?, phone_last4=?, phone_country_code=?
		 WHERE id=? AND store_id=?`,
		encPhone, nonce, hashBytes, last4, countryCode, customerID, storeID); err != nil {
		return Membership{}, false, err
	}

	// 是否已是会员？
	var m Membership
	err = s.db.QueryRowContext(ctx,
		`SELECT id, store_id, customer_id, member_no, status, points_balance FROM customer_memberships WHERE customer_id=?`, customerID).
		Scan(&m.ID, &m.StoreID, &m.CustomerID, &m.MemberNo, &m.Status, &m.PointsBalance)
	if err == nil {
		// 已是会员：返回现有，不重复发新人礼（PRD §4.12）。
		return m, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Membership{}, false, err
	}

	// 新会员：分配会员号（门店范围唯一）。
	memberNo, err := s.allocateMemberNo(ctx, storeID)
	if err != nil {
		return Membership{}, false, err
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO customer_memberships (store_id, customer_id, member_no, status, points_balance) VALUES (?, ?, ?, 'ACTIVE', 0)`,
		storeID, customerID, memberNo)
	if err != nil {
		return Membership{}, false, err
	}
	id, _ := res.LastInsertId()
	m = Membership{ID: id, StoreID: storeID, CustomerID: customerID, MemberNo: memberNo, Status: "ACTIVE", PointsBalance: 0}
	// 新人礼在调用方按门店配置发放一次（PRD §4.12）。
	return m, true, nil
}

func (s *Store) allocateMemberNo(ctx context.Context, storeID int64) (string, error) {
	// 门店范围自增序号。
	var cnt int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM customer_memberships WHERE store_id=?`, storeID).Scan(&cnt); err != nil {
		return "", err
	}
	return fmt.Sprintf("M%06d", cnt+1), nil
}

// ErrPhoneBound 同门店同手机号已绑定其它顾客。
var ErrPhoneBound = errors.New("phone already bound to another customer in this store")

// ── 积分流水（不可变，带 balance_after，PRD §4.12）─────────────────────

// AdjustPoints 人工调整积分（delta≠0，余额不得<0，幂等+审计，PRD §8.1）。
func (s *Store) AdjustPoints(ctx context.Context, membershipID, storeID, customerID, operatorID int64, delta int64, reason, idemKey string) (int64, error) {
	if delta == 0 {
		return 0, errors.New("delta must be non-zero")
	}
	if reason == "" {
		return 0, errors.New("reason required")
	}
	if idemKey == "" {
		return 0, errors.New("idempotency key required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	var balance int64
	if err := tx.QueryRowContext(ctx,
		`SELECT points_balance FROM customer_memberships WHERE id=? AND store_id=? FOR UPDATE`, membershipID, storeID).Scan(&balance); err != nil {
		return 0, err
	}
	newBal := balance + delta
	if newBal < 0 {
		return 0, ErrInsufficientPoints
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE customer_memberships SET points_balance=? WHERE id=?`, newBal, membershipID); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO member_points_ledger (store_id, membership_id, customer_id, delta, balance_after, entry_type, reason, operator_admin_user_id, idempotency_key)
		 VALUES (?, ?, ?, ?, ?, 'ADJUST', ?, ?, ?)`,
		storeID, membershipID, customerID, delta, newBal, reason, operatorIDOrNull(operatorID), idemKey); err != nil {
		return 0, fmt.Errorf("idempotency or ledger: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return newBal, nil
}

// AwardPoints 支付成功入账（幂等，PRD §4.12：先对实付分数向下取整到整元，再乘积分比例）。
func (s *Store) AwardPoints(ctx context.Context, storeID, customerID, orderID int64, paidCents int64, pointsPerYuan int, idemKey string) (int64, error) {
	if paidCents <= 0 || pointsPerYuan <= 0 {
		return 0, nil
	}
	// 向下取整到整元（PRD §4.12）。
	yuan := paidCents / 100
	pts := yuan * int64(pointsPerYuan)
	if pts <= 0 {
		return 0, nil
	}
	var m Membership
	err := s.db.QueryRowContext(ctx,
		`SELECT id, store_id, customer_id, member_no, status, points_balance FROM customer_memberships WHERE customer_id=? AND store_id=?`, customerID, storeID).
		Scan(&m.ID, &m.StoreID, &m.CustomerID, &m.MemberNo, &m.Status, &m.PointsBalance)
	if err != nil {
		return 0, nil // 未入会不入账。
	}
	if m.Status != "ACTIVE" {
		return 0, nil // 非正常会员不入账。
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	newBal := m.PointsBalance + pts
	if _, err := tx.ExecContext(ctx, `UPDATE customer_memberships SET points_balance=? WHERE id=?`, newBal, m.ID); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO member_points_ledger (store_id, membership_id, customer_id, delta, balance_after, entry_type, order_id, reason, idempotency_key)
		 VALUES (?, ?, ?, ?, ?, 'EARN', ?, '支付入账', ?)`,
		storeID, m.ID, customerID, pts, newBal, orderID, idemKey); err != nil {
		return 0, fmt.Errorf("award ledger: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return newBal, nil
}

// ReversePoints 退款扣回本单积分且只执行一次（PRD §4.12）。
func (s *Store) ReversePoints(ctx context.Context, storeID, customerID, refundID, orderID int64, idemKey string) error {
	var m Membership
	err := s.db.QueryRowContext(ctx,
		`SELECT id, store_id, customer_id, member_no, status, points_balance FROM customer_memberships WHERE customer_id=? AND store_id=?`, customerID, storeID).
		Scan(&m.ID, &m.StoreID, &m.CustomerID, &m.MemberNo, &m.Status, &m.PointsBalance)
	if err != nil {
		return nil // 未入会无扣回。
	}
	// 查本单已入账积分。
	var earned sql.NullInt64
	err = s.db.QueryRowContext(ctx,
		`SELECT delta FROM member_points_ledger WHERE customer_id=? AND order_id=? AND entry_type='EARN' LIMIT 1`, customerID, orderID).Scan(&earned)
	if err != nil || !earned.Valid || earned.Int64 <= 0 {
		return nil
	}
	pts := earned.Int64
	newBal := m.PointsBalance - pts
	if newBal < 0 {
		newBal = 0
		pts = m.PointsBalance
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `UPDATE customer_memberships SET points_balance=? WHERE id=?`, newBal, m.ID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO member_points_ledger (store_id, membership_id, customer_id, delta, balance_after, entry_type, refund_id, reason, idempotency_key)
		 VALUES (?, ?, ?, ?, ?, 'REVERSE', ?, '退款扣回', ?)`,
		storeID, m.ID, customerID, -pts, newBal, refundID, idemKey); err != nil {
		return fmt.Errorf("reverse ledger: %w", err)
	}
	return tx.Commit()
}

// ListPointsLedger 列积分流水（PRD §8.1）。
func (s *Store) ListPointsLedger(ctx context.Context, storeID, customerID int64, limit, offset int) ([]map[string]any, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, delta, balance_after, entry_type, order_id, refund_id, reason, created_at
		 FROM member_points_ledger WHERE store_id=? AND customer_id=? ORDER BY id DESC LIMIT ? OFFSET ?`,
		storeID, customerID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var id, delta, balance int64
		var etype, reason, created string
		var orderID, refundID sql.NullInt64
		if err := rows.Scan(&id, &delta, &balance, &etype, &orderID, &refundID, &reason, &created); err != nil {
			return nil, err
		}
		e := map[string]any{"id": id, "delta": delta, "balance_after": balance, "entry_type": etype, "reason": reason, "created_at": created}
		if orderID.Valid {
			e["order_id"] = orderID.Int64
		}
		if refundID.Valid {
			e["refund_id"] = refundID.Int64
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// GetByCustomer 取顾客会员。
func (s *Store) GetByCustomer(ctx context.Context, storeID, customerID int64) (Membership, error) {
	var m Membership
	err := s.db.QueryRowContext(ctx,
		`SELECT id, store_id, customer_id, member_no, status, points_balance FROM customer_memberships WHERE customer_id=? AND store_id=?`,
		customerID, storeID).Scan(&m.ID, &m.StoreID, &m.CustomerID, &m.MemberNo, &m.Status, &m.PointsBalance)
	if errors.Is(err, sql.ErrNoRows) {
		return Membership{}, sql.ErrNoRows
	}
	return m, err
}

// ErrInsufficientPoints 积分不足。
var ErrInsufficientPoints = errors.New("INSUFFICIENT_POINTS")

func operatorIDOrNull(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}
