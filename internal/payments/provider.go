// Package payments 实现微信支付 v3 预支付、回调验签、退款与 mock 模式（PRD §4.8/§14/§15）。
//
// 真实微信支付 v3 需要 AppSecret/商户私钥/APIv3（P6 安全中心配置）。本包定义 Provider
// 接口，生产实现走 wechatpay-go；MockProvider 用于 dev/mock 联调。生产必须 fail closed。
package payments

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
)

// Provider 微信支付能力接口。
type Provider interface {
	// Prepay 创建预支付，返回供 wx.requestPayment 的参数与微信 prepay_id。
	Prepay(ctx context.Context, in PrepayInput) (PrepayResult, error)
	// QueryActive 主动查单确认真实状态（PRD §15）。
	QueryActive(ctx context.Context, storeID int64, outTradeNo string) (QueryResult, error)
	// Refund 发起全额退款。
	Refund(ctx context.Context, in RefundInput) (RefundResult, error)
	// QueryRefund 主动查退款状态。
	QueryRefund(ctx context.Context, storeID int64, refundNo string) (RefundStatus, error)
	// Mock 标记。
	IsMock() bool
}

// PrepayInput 预支付输入。
type PrepayInput struct {
	StoreID      int64
	OrderID      int64
	OutTradeNo   string // 商户订单号 = order_no
	AmountCents  int64
	Description  string
	OpenID       string // 小程序 JSAPI 支付需要 openid
	NotifyURL    string // 含 store_id 的回调路径（PRD §14.2）
}

// PrepayResult 预支付结果。
type PrepayResult struct {
	PrepayID  string
	MockPay   bool
	// 小程序 wx.requestPayment 参数。
	JSAPIPayload map[string]string
}

// QueryResult 查单结果。
type QueryResult struct {
	State        QueryState
	TransactionID string
	AmountCents  int64
}

// QueryState 支付查单状态。
type QueryState string

const (
	QueryUnknown   QueryState = "UNKNOWN"
	QuerySuccess   QueryState = "SUCCESS"
	QueryClosed    QueryState = "CLOSED"
	QueryNotPaid   QueryState = "NOT_PAID"
	QueryPayError  QueryState = "PAY_ERROR"
	QueryRevoked   QueryState = "REVOKED"
)

// RefundInput 退款输入。
type RefundInput struct {
	StoreID        int64
	OrderID        int64
	OutTradeNo     string
	OutRefundNo    string // 商户退款号
	AmountCents    int64
	Reason         string
	NotifyURL      string
}

// RefundResult 退款结果。
type RefundResult struct {
	RefundIDWX string
	State      RefundState
}

// RefundState 退款状态（PRD §14.3）。
type RefundState string

const (
	RefundCreated    RefundState = "CREATED"
	RefundProcessing RefundState = "PROCESSING"
	RefundSuccess    RefundState = "SUCCESS"
	RefundAbnormal   RefundState = "ABNORMAL"
)

// ── MockProvider（dev/mock 联调，PRD §14.4）───────────────────────────

// MockProvider 所有操作返回确定性成功；mock 标记贯穿订单/退款/审计。
type MockProvider struct{}

// IsMock 始终 true。
func (MockProvider) IsMock() bool { return true }

// Prepay 返回 mock prepay_id；调用方需调用 mock 确认接口才推进支付（PRD §4.8）。
func (MockProvider) Prepay(_ context.Context, in PrepayInput) (PrepayResult, error) {
	return PrepayResult{
		PrepayID: "mock_prepay_" + randHex(8),
		MockPay:  true,
		JSAPIPayload: map[string]string{
			"mock_payment": "true",
		},
	}, nil
}

// QueryActive mock 下默认未知（mock 支付靠显式确认接口推进）。
func (MockProvider) QueryActive(_ context.Context, _ int64, _ string) (QueryResult, error) {
	return QueryResult{State: QueryUnknown}, nil
}

// Refund mock 退款直接成功（仍走真实数据库状态机/库存/积分/审计，PRD §14.4）。
func (MockProvider) Refund(_ context.Context, in RefundInput) (RefundResult, error) {
	return RefundResult{RefundIDWX: "mock_refund_" + randHex(8), State: RefundSuccess}, nil
}

// QueryRefund mock 退款查单直接成功。
func (MockProvider) QueryRefund(_ context.Context, _ int64, _ string) (RefundStatus, error) {
	return RefundStatus{State: RefundSuccess}, nil
}

// RefundStatus 查退款结果。
type RefundStatus struct {
	State        RefundState
	RefundIDWX   string
	AmountCents  int64
}

// ErrZeroOrderNotWechat 0 元订单不调微信收款（PRD §4.5）。
var ErrZeroOrderNotWechat = errors.New("zero amount order does not call wechat pay")

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// suppress
var _ = fmt.Sprintf
