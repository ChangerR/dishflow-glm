// Package marketing 实现满减、优惠券模板、领取/核销/兑换与按人群发放（PRD §4.11/§7.4/§4.12）。
//
// 关键规则：
//   - 领取幂等：同顾客+模板+来源一次性（唯一约束）。
//   - 下单核销实际采用的顾客券；未被采用的指定券不核销。
//   - 兑换事务性扣减积分并发券，支持重复兑换但每次幂等。
//   - 入会新人券仅在配置模板当前有效时发放一次。
package marketing

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Store 营销域持久化。
type Store struct {
	db *sql.DB
}

// NewStore 创建营销存储。
func NewStore(db *sql.DB) *Store { return &Store{db: db} }

// ── 满减 ──────────────────────────────────────────────────────────────

// Promotion 满减。
type Promotion struct {
	ID             int64
	StoreID        int64
	Name           string
	ThresholdCents int64
	DiscountCents  int64
	StartsAt       time.Time
	EndsAt         time.Time
	Enabled        bool
}

// CreatePromotion 创建满减（PRD §7.4）。
func (s *Store) CreatePromotion(ctx context.Context, storeID int64, name string, threshold, discount int64, startsAt, endsAt time.Time) (int64, error) {
	if threshold < 0 || discount <= 0 {
		return 0, errors.New("invalid promotion amounts")
	}
	if !endsAt.After(startsAt) {
		return 0, errors.New("ends_at must be after starts_at")
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO promotions (store_id, name, threshold_cents, discount_cents, scope, starts_at, ends_at, enabled)
		 VALUES (?, ?, ?, ?, 'STORE', ?, ?, 1)`,
		storeID, strings.TrimSpace(name), threshold, discount, startsAt, endsAt)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ListActivePromotions 列出门店当前有效满减（PRD §4.5）。
func (s *Store) ListActivePromotions(ctx context.Context, storeID int64, now time.Time) ([]Promotion, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, store_id, name, threshold_cents, discount_cents, starts_at, ends_at
		 FROM promotions WHERE store_id=? AND enabled=1 AND deleted_at IS NULL
		   AND starts_at <= ? AND ends_at >= ? ORDER BY discount_cents DESC, id ASC`,
		storeID, now, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Promotion
	for rows.Next() {
		var p Promotion
		if err := rows.Scan(&p.ID, &p.StoreID, &p.Name, &p.ThresholdCents, &p.DiscountCents, &p.StartsAt, &p.EndsAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ── 优惠券模板 ──────────────────────────────────────────────────────────

// CouponTemplate 券模板。
type CouponTemplate struct {
	ID             int64
	StoreID        int64
	Name           string
	MinSpendCents  int64
	DiscountCents  int64
	StartsAt       time.Time
	EndsAt         time.Time
	Enabled        bool
	PubliclyClaim  bool
	Audience       string
	Redeemable     bool
	PointsCost     int
}

// CreateCouponTemplate 创建券模板（PRD §7.4）。
func (s *Store) CreateCouponTemplate(ctx context.Context, t CouponTemplate) (int64, error) {
	if t.MinSpendCents < 0 || t.DiscountCents <= 0 {
		return 0, errors.New("invalid coupon amounts")
	}
	if !t.EndsAt.After(t.StartsAt) {
		return 0, errors.New("ends_at must be after starts_at")
	}
	if t.Audience == "" {
		t.Audience = "ALL"
	}
	red := 0
	if t.Redeemable {
		red = 1
	}
	pub := 0
	if t.PubliclyClaim {
		pub = 1
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO coupon_templates (store_id, name, min_spend_cents, discount_cents, scope, starts_at, ends_at, enabled, publicly_claimable, audience, redeemable, points_cost)
		 VALUES (?, ?, ?, ?, 'STORE', ?, ?, 1, ?, ?, ?, ?)`,
		t.StoreID, t.Name, t.MinSpendCents, t.DiscountCents, t.StartsAt, t.EndsAt, pub, t.Audience, red, t.PointsCost)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ListPublicCouponOffers 列出可公开领取的券（PRD §4.11：模板启用、公开领取、有效期、顾客未领）。
func (s *Store) ListPublicCouponOffers(ctx context.Context, storeID int64, customerID int64, now time.Time) ([]CouponTemplate, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT ct.id, ct.store_id, ct.name, ct.min_spend_cents, ct.discount_cents, ct.starts_at, ct.ends_at
		 FROM coupon_templates ct
		 WHERE ct.store_id=? AND ct.enabled=1 AND ct.publicly_claimable=1 AND ct.deleted_at IS NULL
		   AND ct.starts_at <= ? AND ct.ends_at >= ?
		   AND NOT EXISTS (SELECT 1 FROM customer_coupons cc WHERE cc.template_id=ct.id AND cc.customer_id=? AND cc.source='CLAIM')
		 ORDER BY ct.id DESC`, storeID, now, now, customerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CouponTemplate
	for rows.Next() {
		var t CouponTemplate
		if err := rows.Scan(&t.ID, &t.StoreID, &t.Name, &t.MinSpendCents, &t.DiscountCents, &t.StartsAt, &t.EndsAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Claim 公开领取券（幂等：同顾客+模板+CLAIM 唯一，PRD §4.11）。
func (s *Store) Claim(ctx context.Context, storeID, customerID, templateID int64) (int64, error) {
	now := time.Now().UTC()
	// 校验模板公开领取且有效。
	var pub, enabled int
	var startsAt, endsAt time.Time
	err := s.db.QueryRowContext(ctx,
		`SELECT publicly_claimable, enabled, starts_at, ends_at FROM coupon_templates WHERE id=? AND store_id=? AND deleted_at IS NULL`,
		templateID, storeID).Scan(&pub, &enabled, &startsAt, &endsAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, errors.New("coupon template not found")
		}
		return 0, err
	}
	if pub != 1 || enabled != 1 {
		return 0, errors.New("coupon not publicly claimable")
	}
	if now.Before(startsAt) || now.After(endsAt) {
		return 0, errors.New("coupon not in valid period")
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO customer_coupons (store_id, customer_id, template_id, status, source, expires_at)
		 VALUES (?, ?, ?, 'AVAILABLE', 'CLAIM', ?)`,
		storeID, customerID, templateID, endsAt)
	if err != nil {
		// 唯一约束冲突 = 已领过（幂等）。
		return 0, ErrAlreadyClaimed
	}
	return res.LastInsertId()
}

// ── 核销（下单成功核销实际采用的券，PRD §4.11）─────────────────────────

// RedeemCoupon 核销顾客券为 USED。
func (s *Store) RedeemCoupon(ctx context.Context, storeID, customerCouponID int64) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE customer_coupons SET status='USED', used_at=UTC_TIMESTAMP(3)
		 WHERE id=? AND store_id=? AND status='AVAILABLE'`, customerCouponID, storeID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errors.New("coupon not redeemable")
	}
	return nil
}

// ── 顾客券查询 ────────────────────────────────────────────────────────

// CustomerCoupon 顾客券。
type CustomerCoupon struct {
	ID            int64
	StoreID       int64
	CustomerID    int64
	TemplateID    int64
	Status        string
	Source        string
	ExpiresAt     time.Time
	UsedAt        sql.NullTime
	TemplateName  string
	MinSpendCents int64
	DiscountCents int64
}

// ListCustomerCoupons 按状态列顾客券（PRD §4.11）。
func (s *Store) ListCustomerCoupons(ctx context.Context, storeID, customerID int64, status string) ([]CustomerCoupon, error) {
	q := `SELECT cc.id, cc.store_id, cc.customer_id, cc.template_id, cc.status, cc.source, cc.expires_at, cc.used_at,
	             ct.name, ct.min_spend_cents, ct.discount_cents
	      FROM customer_coupons cc JOIN coupon_templates ct ON ct.id=cc.template_id
	      WHERE cc.store_id=? AND cc.customer_id=?`
	args := []any{storeID, customerID}
	if status != "" {
		q += ` AND cc.status=?`
		args = append(args, status)
	}
	q += ` ORDER BY cc.id DESC`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CustomerCoupon
	for rows.Next() {
		var c CustomerCoupon
		if err := rows.Scan(&c.ID, &c.StoreID, &c.CustomerID, &c.TemplateID, &c.Status, &c.Source,
			&c.ExpiresAt, &c.UsedAt, &c.TemplateName, &c.MinSpendCents, &c.DiscountCents); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ErrAlreadyClaimed 已领取（幂等）。
var ErrAlreadyClaimed = errors.New("already claimed")

// suppress
var _ = fmt.Sprintf
