// coupon_pricing.go 提供算价入口的顾客券查询与校验（PRD §4.5/§4.11）。
package marketing

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// CouponForPricing 算价用顾客券（已校验属主/状态/模板/有效期）。
type CouponForPricing struct {
	CustomerCouponID int64
	DiscountCents    int64
	MinSpendCents    int64
}

// GetCouponForPricing 查并校验顾客券（PRD §4.5：券必须属于顾客+门店、状态可用、模板启用、有效期、最低消费）。
// 不满足条件返回对应原因（调用方传给算价引擎展示"不可用优惠券原因"，PRD §4.4.5）。
func (s *Store) GetCouponForPricing(ctx context.Context, storeID, customerID, customerCouponID int64) (CouponForPricing, error) {
	var (
		ccStatus   string
		ccExpire   time.Time
		tplEnabled int
		tplStart   time.Time
		tplEnd     time.Time
		minSpend   int64
		discount   int64
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT cc.status, cc.expires_at, ct.enabled, ct.starts_at, ct.ends_at, ct.min_spend_cents, ct.discount_cents
		 FROM customer_coupons cc
		 JOIN coupon_templates ct ON ct.id = cc.template_id
		 WHERE cc.id=? AND cc.store_id=? AND cc.customer_id=?`,
		customerCouponID, storeID, customerID).
		Scan(&ccStatus, &ccExpire, &tplEnabled, &tplStart, &tplEnd, &minSpend, &discount)
	if errors.Is(err, sql.ErrNoRows) {
		return CouponForPricing{}, errors.New("coupon not found or not yours")
	}
	if err != nil {
		return CouponForPricing{}, err
	}
	now := time.Now().UTC()
	if ccStatus != "AVAILABLE" {
		return CouponForPricing{}, errors.New("coupon not available (status: " + ccStatus + ")")
	}
	if tplEnabled != 1 {
		return CouponForPricing{}, errors.New("coupon template disabled")
	}
	if now.Before(tplStart) || now.After(tplEnd) {
		return CouponForPricing{}, errors.New("coupon not in valid period")
	}
	if now.After(ccExpire) {
		return CouponForPricing{}, errors.New("coupon expired")
	}
	return CouponForPricing{CustomerCouponID: customerCouponID, DiscountCents: discount, MinSpendCents: minSpend}, nil
}
