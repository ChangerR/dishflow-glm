//go:build integration

// 支付流程集成测试：下单 → 预支付(mock) → mock 确认 → Worker 消费 outbox → 订单 PAID + 库存转已售。
// 运行：SHOP_DATABASE_DSN=... go test -tags=integration -race ./internal/integrationtest/
package integrationtest

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"

	"github.com/dishflow/zshop/internal/orders"
	"github.com/dishflow/zshop/internal/payments"
	"github.com/dishflow/zshop/internal/pricing"
	"github.com/dishflow/zshop/internal/refunds"
	"github.com/dishflow/zshop/internal/worker"
)

func newTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func dbOnly(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("SHOP_DATABASE_DSN")
	if dsn == "" {
		dsn = "root:rootpw@tcp(127.0.0.1:3307)/dishflow?parseTime=true&loc=UTC&charset=utf8mb4"
	}
	dbx, err := sqlx.Connect("mysql", dsn)
	if err != nil {
		t.Skipf("mysql unavailable: %v", err)
	}
	return dbx.DB
}

func TestPaymentFlow_MockConfirmToPaid(t *testing.T) {
	db := dbOnly(t)
	defer db.Close()
	ctx := context.Background()
	c := uniq()

	// seed：门店/分类/菜品/SKU/顾客 + 每日库存。
	res, _ := db.ExecContext(ctx, `INSERT INTO stores (name, timezone, scheduled_pickup_enabled) VALUES (?, 'Asia/Shanghai', 1)`, fmt.Sprintf("支付店_%d", c))
	storeID, _ := res.LastInsertId()
	res, _ = db.ExecContext(ctx, `INSERT INTO categories (store_id, name, enabled, sort_order) VALUES (?, '主食', 1, 1)`, storeID)
	catID, _ := res.LastInsertId()
	res, _ = db.ExecContext(ctx, `INSERT INTO products (store_id, category_id, name, enabled) VALUES (?, ?, '套餐', 1)`, storeID, catID)
	prodID, _ := res.LastInsertId()
	res, _ = db.ExecContext(ctx, `INSERT INTO skus (store_id, product_id, name, price_cents, inventory_mode, daily_stock, enabled, is_default) VALUES (?, ?, '常规', 1000, 'DAILY', 5, 1, 1)`, storeID, prodID)
	skuID, _ := res.LastInsertId()
	res, _ = db.ExecContext(ctx, `INSERT INTO customers (store_id, wechat_openid) VALUES (?, ?)`, storeID, fmt.Sprintf("oid_%d", c))
	customerID, _ := res.LastInsertId()
	db.ExecContext(ctx, `INSERT INTO daily_inventory (store_id, sku_id, business_date, target_qty) VALUES (?, ?, '2026-08-01', 5)`, storeID, skuID)

	ordStore := orders.NewStore(db)
	payStore := payments.NewStore(db, payments.MockProvider{})

	// 1. 创建订单。
	quote := pricing.QuoteSummary{
		Token: fmt.Sprintf("qt_%d", c), StoreID: storeID, Scenario: pricing.ScenarioPickup,
		ItemAmountCents: 1000, PayableCents: 1000, ExpiresAt: time.Now().Add(10 * time.Minute).Unix(),
	}
	cart := []orders.LineItemSnapshot{{SKUID: skuID, UnitPriceCents: 1000, Quantity: 2, LineAmountCents: 2000}}
	orderID, err := ordStore.Create(ctx, orders.CreateInput{
		StoreID: storeID, CustomerID: customerID, Quote: quote, Cart: cart,
		IdempotencyKey: fmt.Sprintf("idem_%d", c), BusinessDate: "2026-08-01",
	})
	if err != nil {
		t.Fatalf("create order: %v", err)
	}

	// 2. 预支付(mock)。
	_, err = payStore.Prepay(ctx, payments.PrepayInput{
		StoreID: storeID, OrderID: orderID, OutTradeNo: fmt.Sprintf("ON%d", orderID),
		AmountCents: 2000, Description: "test",
	})
	if err != nil {
		t.Fatalf("prepay: %v", err)
	}

	// 3. mock 确认（生成 payment.success outbox）。
	if err := payStore.ConfirmMockPayment(ctx, storeID, orderID); err != nil {
		t.Fatalf("confirm mock: %v", err)
	}

	// 4. 验证 outbox 已有 payment.success。
	var outboxCnt int
	db.QueryRowContext(ctx, `SELECT COUNT(*) FROM outbox WHERE event_type='payment.success' AND aggregate_id=?`, orderID).Scan(&outboxCnt)
	if outboxCnt == 0 {
		t.Fatal("expected payment.success outbox event")
	}

	// 5. 验证 payment 状态。
	var payStatus string
	db.QueryRowContext(ctx, `SELECT status FROM payments WHERE order_id=?`, orderID).Scan(&payStatus)
	if payStatus != "SUCCESS" {
		t.Fatalf("payment status = %s, want SUCCESS", payStatus)
	}

	// 6. Worker 消费 outbox：订单应变为 PAID，库存转已售。
	w := worker.New(db, newTestLogger())
	w.RunOnce(ctx)

	var orderState string
	db.QueryRowContext(ctx, `SELECT fulfillment_state FROM orders WHERE id=?`, orderID).Scan(&orderState)
	if orderState != "PAID" {
		t.Fatalf("order state = %s, want PAID", orderState)
	}
	var sold int
	db.QueryRowContext(ctx, `SELECT sold_qty FROM daily_inventory WHERE store_id=? AND sku_id=? AND business_date='2026-08-01'`, storeID, skuID).Scan(&sold)
	if sold != 2 {
		t.Fatalf("sold_qty = %d, want 2", sold)
	}
	var reserved int
	db.QueryRowContext(ctx, `SELECT reserved_qty FROM daily_inventory WHERE store_id=? AND sku_id=? AND business_date='2026-08-01'`, storeID, skuID).Scan(&reserved)
	if reserved != 0 {
		t.Fatalf("reserved_qty = %d, want 0 (fulfilled)", reserved)
	}
}

func TestRefund_OneActiveIntent(t *testing.T) {
	db := dbOnly(t)
	defer db.Close()
	ctx := context.Background()
	c := uniq()

	res, _ := db.ExecContext(ctx, `INSERT INTO stores (name, timezone) VALUES (?, 'Asia/Shanghai')`, fmt.Sprintf("退款店_%d", c))
	storeID, _ := res.LastInsertId()
	res, _ = db.ExecContext(ctx, `INSERT INTO customers (store_id, wechat_openid) VALUES (?, ?)`, storeID, fmt.Sprintf("oid_%d", c))
	customerID, _ := res.LastInsertId()
	res, _ = db.ExecContext(ctx, `INSERT INTO orders (store_id, customer_id, order_no, scenario, pickup_type, item_amount_cents, payable_cents, paid_cents, fulfillment_state, quote_token, quote_expires_at, expires_at) VALUES (?, ?, ?, 'PICKUP', 'IMMEDIATE', 1000, 1000, 1000, 'PAID', ?, ?, ?)`, storeID, customerID, fmt.Sprintf("Q%d", c), fmt.Sprintf("Q%d", c), time.Now().Add(10*time.Minute), time.Now().Add(10*time.Minute))
	orderID, _ := res.LastInsertId()
	res, _ = db.ExecContext(ctx, `INSERT INTO payments (store_id, order_id, amount_cents, status) VALUES (?, ?, 1000, 'SUCCESS')`, storeID, orderID)
	payID, _ := res.LastInsertId()

	refStore := refunds.NewStore(db)
	// 第一个退款意图成功。
	_, err := refStore.Create(ctx, refunds.CreateInput{
		StoreID: storeID, OrderID: orderID, PaymentID: payID, AmountCents: 1000,
		Reason: "测试", TriggerKind: "STAFF_MANUAL",
	})
	if err != nil {
		t.Fatalf("first refund: %v", err)
	}
	// 第二个应被拒（一单最多一个有效退款意图，PRD §14.3/§17.2）。
	_, err = refStore.Create(ctx, refunds.CreateInput{
		StoreID: storeID, OrderID: orderID, PaymentID: payID, AmountCents: 1000,
		Reason: "测试2", TriggerKind: "STAFF_MANUAL",
	})
	if err != refunds.ErrExistingRefund {
		t.Fatalf("expected ErrExistingRefund, got %v", err)
	}
}
