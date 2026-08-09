// quote.go 实现 quote_token 的签发与验证（PRD §4.4.6/§4.7）。
//
// quote_token 绑定门店、顾客/匿名主体、场景、桌号、购物车摘要、优惠结果、预约时间与过期时间。
// Redis 只存十分钟；丢失需重新算价，不允许从客户端恢复可信金额（PRD §22）。
package pricing

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

// QuoteSummary 是报价的服务端权威快照（PRD §4.4.5）。
type QuoteSummary struct {
	Token            string   `json:"token"`
	StoreID          int64    `json:"store_id"`
	CustomerID       int64    `json:"customer_id,omitempty"` // 0=匿名
	Scenario         Scenario `json:"scenario"`
	TableToken       string   `json:"table_token,omitempty"`
	ScheduledFor     string   `json:"scheduled_for,omitempty"`
	CartHash         string   `json:"cart_hash"`
	ItemAmountCents  int64    `json:"item_amount_cents"`
	PackagingFeeCents int64   `json:"packaging_fee_cents"`
	DiscountCents    int64    `json:"discount_cents"`
	PayableCents     int64    `json:"payable_cents"`
	PromotionID      int64    `json:"promotion_id,omitempty"`
	CustomerCouponID int64    `json:"customer_coupon_id,omitempty"`
	ExpiresAt        int64    `json:"expires_at"` // unix
	// 订单快照用：报价时的商品行（PRD §4.7 快照来自 quote）。
	Lines            []LineItem `json:"lines,omitempty"`
	// 门店时区（用于业务日计算，PRD §4.6/§4.7）。
	Timezone         string     `json:"timezone,omitempty"`
}

// PickupBusinessDate 返回订单取餐业务日（YYYY-MM-DD）。
// 预约单用预约日期；即时单用门店今天（PRD §4.6.6）。
func (q QuoteSummary) PickupBusinessDate() string {
	if q.ScheduledFor != "" {
		if t, err := time.Parse(time.RFC3339, q.ScheduledFor); err == nil {
			if q.Timezone != "" {
				if loc, e := time.LoadLocation(q.Timezone); e == nil {
					return t.In(loc).Format("2006-01-02")
				}
			}
			return t.UTC().Format("2006-01-02")
		}
		// "YYYY-MM-DD HH:MM:SS" 本地格式。
		return q.ScheduledFor[:10]
	}
	// 即时单：门店今天。
	if q.Timezone != "" {
		if loc, err := time.LoadLocation(q.Timezone); err == nil {
			return time.Now().In(loc).Format("2006-01-02")
		}
	}
	return time.Now().UTC().Format("2006-01-02")
}

// QuoteStore 基于 Redis 的报价存储 + HMAC 签名 token。
type QuoteStore struct {
	rdb   *redis.Client
	key   []byte // HMAC key
	ttl   time.Duration
}

// NewQuoteStore 创建报价存储。signingKey 至少 32 字节（生产由 config 保证）。
func NewQuoteStore(rdb *redis.Client, signingKey []byte, ttl time.Duration) *QuoteStore {
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	return &QuoteStore{rdb: rdb, key: signingKey, ttl: ttl}
}

// Issue 签发报价：生成 token、写 Redis（十分钟），返回带签名的 token（PRD §4.4.5/§4.4.6）。
func (q *QuoteStore) Issue(ctx context.Context, storeID, customerID int64, scenario Scenario, tableToken, scheduledFor, cartHash string,
	itemAmount, packaging, discount, payable int64, promotionID, couponID int64) (QuoteSummary, error) {
	return q.IssueWithLines(ctx, storeID, customerID, scenario, tableToken, scheduledFor, cartHash,
		itemAmount, packaging, discount, payable, promotionID, couponID, nil, "")
}

// IssueWithLines 同 Issue，但附带商品行快照与时区（订单创建快照用）。
func (q *QuoteStore) IssueWithLines(ctx context.Context, storeID, customerID int64, scenario Scenario, tableToken, scheduledFor, cartHash string,
	itemAmount, packaging, discount, payable int64, promotionID, couponID int64, lines []LineItem, tz string) (QuoteSummary, error) {
	now := time.Now().UTC()
	rawToken, err := randomHex(16)
	if err != nil {
		return QuoteSummary{}, err
	}
	sig := q.sign(rawToken)
	token := rawToken + "." + sig
	summary := QuoteSummary{
		Token: token, StoreID: storeID, CustomerID: customerID, Scenario: scenario,
		TableToken: tableToken, ScheduledFor: scheduledFor, CartHash: cartHash,
		ItemAmountCents: itemAmount, PackagingFeeCents: packaging, DiscountCents: discount,
		PayableCents: payable, PromotionID: promotionID, CustomerCouponID: couponID,
		ExpiresAt: now.Add(q.ttl).Unix(), Lines: lines, Timezone: tz,
	}
	body, err := json.Marshal(summary)
	if err != nil {
		return QuoteSummary{}, err
	}
	if q.rdb != nil {
		if err := q.rdb.Set(ctx, "quote:"+token, body, q.ttl).Err(); err != nil {
			return QuoteSummary{}, err
		}
	}
	return summary, nil
}

// Lookup 取并验证报价（PRD §4.7：创建订单只信任有效 quote_token）。
func (q *QuoteStore) Lookup(ctx context.Context, token string) (QuoteSummary, error) {
	if !q.verifySig(token) {
		return QuoteSummary{}, errors.New("invalid quote token signature")
	}
	if q.rdb == nil {
		return QuoteSummary{}, errors.New("quote storage unavailable")
	}
	body, err := q.rdb.Get(ctx, "quote:"+token).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return QuoteSummary{}, ErrQuoteExpired
		}
		return QuoteSummary{}, err
	}
	var s QuoteSummary
	if err := json.Unmarshal(body, &s); err != nil {
		return QuoteSummary{}, err
	}
	if time.Now().UTC().Unix() > s.ExpiresAt {
		return QuoteSummary{}, ErrQuoteExpired
	}
	return s, nil
}

// Delete 删除报价（同 quote 只能创建一个订单，PRD §4.7）。
func (q *QuoteStore) Delete(ctx context.Context, token string) error {
	if q.rdb == nil {
		return nil
	}
	return q.rdb.Del(ctx, "quote:"+token).Err()
}

// ErrQuoteExpired 报价过期/不存在。
var ErrQuoteExpired = errors.New("quote expired")

func (q *QuoteStore) sign(raw string) string {
	mac := hmac.New(sha256.New, q.key)
	mac.Write([]byte(raw))
	return hex.EncodeToString(mac.Sum(nil))
}

func (q *QuoteStore) verifySig(token string) bool {
	idx := -1
	for i := len(token) - 1; i >= 0; i-- {
		if token[i] == '.' {
			idx = i
			break
		}
	}
	if idx <= 0 || idx >= len(token)-1 {
		return false
	}
	raw := token[:idx]
	sig := token[idx+1:]
	expected := q.sign(raw)
	return hmac.Equal([]byte(sig), []byte(expected))
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
