//go:build integration

// 取消退款闭环端到端测试（PRD §4.10/§14.3）。
// 运行：SHOP_DATABASE_DSN=... go test -tags=integration -race ./internal/integrationtest/
package integrationtest

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/dishflow/zshop/internal/members"
	"github.com/dishflow/zshop/internal/orders"
	"github.com/dishflow/zshop/internal/payments"
	"github.com/dishflow/zshop/internal/pricing"
	"github.com/dishflow/zshop/internal/refunds"
	"github.com/dishflow/zshop/internal/security"
	"github.com/dishflow/zshop/internal/worker"
)

func seedCancelOrder(t *testing.T, db *sql.DB) (storeID, customerID, skuID int64) {
	t.Helper()
	ctx := context.Background()
	c := uniq()
	res, _ := db.ExecContext(ctx, `INSERT INTO stores (name, timezone, scheduled_pickup_enabled, points_per_yuan) VALUES (?, 'Asia/Shanghai', 1, 1)`, fmt.Sprintf("取消店_%d", c))
	storeID, _ = res.LastInsertId()
	res, _ = db.ExecContext(ctx, `INSERT INTO categories (store_id, name, enabled, sort_order) VALUES (?, '主食', 1, 1)`, storeID)
	catID, _ := res.LastInsertId()
	res, _ = db.ExecContext(ctx, `INSERT INTO products (store_id, category_id, name, enabled) VALUES (?, ?, '套餐', 1)`, storeID, catID)
	prodID, _ := res.LastInsertId()
	res, _ = db.ExecContext(ctx, `INSERT INTO skus (store_id, product_id, name, price_cents, inventory_mode, daily_stock, enabled, is_default) VALUES (?, ?, '常规', 1000, 'DAILY', 20, 1, 1)`, storeID, prodID)
	skuID, _ = res.LastInsertId()
	res, _ = db.ExecContext(ctx, `INSERT INTO customers (store_id, wechat_openid) VALUES (?, ?)`, storeID, fmt.Sprintf("oid_%d", c))
	customerID, _ = res.LastInsertId()
	db.ExecContext(ctx, `INSERT INTO daily_inventory (store_id, sku_id, business_date, target_qty) VALUES (?, ?, '2026-08-02', 20)`, storeID, skuID)
	return storeID, customerID, skuID
}

func TestCancelRefund_PaidToRefunded(t *testing.T) {
	db := dbOnly(t)
	defer db.Close()
	ctx := context.Background()
	storeID, customerID, skuID := seedCancelOrder(t, db)
	ordStore := orders.NewStore(db)
	payStore := payments.NewStore(db, payments.MockProvider{})
	refStore := refunds.NewStore(db)

	// 1. 创建订单 + 预占库存。
	q := pricing.QuoteSummary{
		Token: fmt.Sprintf("qt_%d", uniq()), StoreID: storeID, Scenario: pricing.ScenarioPickup,
		ItemAmountCents: 2000, PayableCents: 2000, ExpiresAt: time.Now().Add(10 * time.Minute).Unix(),
	}
	cart := []orders.LineItemSnapshot{{SKUID: skuID, UnitPriceCents: 1000, Quantity: 2, LineAmountCents: 2000}}
	orderID, err := ordStore.Create(ctx, orders.CreateInput{
		StoreID: storeID, CustomerID: customerID, Quote: q, Cart: cart,
		IdempotencyKey: fmt.Sprintf("idem_%d", uniq()), BusinessDate: "2026-08-02",
	})
	if err != nil {
		t.Fatalf("create order: %v", err)
	}

	// 2. mock 支付确认。
	payStore.Prepay(ctx, payments.PrepayInput{StoreID: storeID, OrderID: orderID, OutTradeNo: fmt.Sprintf("ON%d", orderID), AmountCents: 2000})
	payStore.ConfirmMockPayment(ctx, storeID, orderID)

	// 3. Worker → PAID + 积分入账。
	enc, _ := security.NewEncryptor([]byte("0123456789abcdef0123456789abcdef"))
	memStore := members.NewStore(db, enc)
	memStore.Join(ctx, storeID, customerID, fmt.Sprintf("139%07d", uniq()%10000000), "86")
	w := worker.New(db, newTestLogger()).WithStores(ordStore, memStore)
	w.RunOnce(ctx)

	o, _ := ordStore.GetByID(ctx, storeID, orderID)
	if o.FulfillmentState != orders.StatePaid {
		t.Fatalf("state = %s, want PAID", o.FulfillmentState)
	}
	m, _ := memStore.GetByCustomer(ctx, storeID, customerID)
	if m.PointsBalance != 20 {
		t.Fatalf("points = %d, want 20", m.PointsBalance)
	}

	// 4. 顾客取消（PAID 未接单 → REFUNDING + NeedRefund）。
	result, err := ordStore.Cancel(ctx, orders.CancelInput{
		StoreID: storeID, OrderID: orderID, CustomerID: customerID, Actor: "customer",
	})
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if !result.NeedRefund || result.NewState != orders.StateRefunding {
		t.Fatalf("cancel result = %+v, want REFUNDING", result)
	}

	// 5. 退款意图 + mock 成功 + outbox。
	var payID, amount int64
	db.QueryRowContext(ctx, `SELECT id, amount_cents FROM payments WHERE order_id=? AND status='SUCCESS'`, orderID).Scan(&payID, &amount)
	refundID, err := refStore.Create(ctx, refunds.CreateInput{
		StoreID: storeID, OrderID: orderID, PaymentID: payID, AmountCents: amount,
		Reason: "顾客取消", TriggerKind: "CUSTOMER_AUTO", Mock: true,
	})
	if err != nil {
		t.Fatalf("create refund: %v", err)
	}
	tx, _ := db.BeginTx(ctx, nil)
	refStore.MarkSuccess(ctx, tx, storeID, refundID, "mock_tx", "")
	tx.ExecContext(ctx,
		`INSERT INTO outbox (store_id, event_type, aggregate_type, aggregate_id, payload, status)
		 VALUES (?, 'refund.success', 'refund', ?, ?, 'PENDING')`,
		storeID, refundID, []byte(fmt.Sprintf(`{"order_id":%d,"customer_id":%d,"refund_id":%d}`, orderID, customerID, refundID)))
	tx.Commit()

	// 6. Worker → REFUNDED + 积分扣回 + 库存释放。
	w.RunOnce(ctx)

	o, _ = ordStore.GetByID(ctx, storeID, orderID)
	if o.FulfillmentState != orders.StateRefunded {
		t.Fatalf("state = %s, want REFUNDED", o.FulfillmentState)
	}
	m2, _ := memStore.GetByCustomer(ctx, storeID, customerID)
	if m2.PointsBalance != 0 {
		t.Fatalf("points after refund = %d, want 0", m2.PointsBalance)
	}
	var sold int
	db.QueryRowContext(ctx, `SELECT sold_qty FROM daily_inventory WHERE store_id=? AND sku_id=? AND business_date='2026-08-02'`, storeID, skuID).Scan(&sold)
	if sold != 0 {
		t.Fatalf("sold_qty = %d, want 0 (released on refund)", sold)
	}
}

func TestCancelRefund_AcceptedToCancelRequested(t *testing.T) {
	db := dbOnly(t)
	defer db.Close()
	ctx := context.Background()
	storeID, customerID, _ := seedCancelOrder(t, db)
	ordStore := orders.NewStore(db)

	// 建 ACCEPTED 订单（用 AUTO_INCREMENT id）。
	c2 := uniq()
	res, err := db.ExecContext(ctx,
		`INSERT INTO orders (store_id, customer_id, order_no, scenario, pickup_type, item_amount_cents, payable_cents, paid_cents, fulfillment_state, version, quote_token, quote_expires_at, expires_at, paid_at)
		 VALUES (?, ?, ?, 'PICKUP','IMMEDIATE', 1000,1000,1000,'ACCEPTED',1,?,?,?, NOW())`,
		storeID, customerID, fmt.Sprintf("AC%d", c2), fmt.Sprintf("qA%d", c2), "2030-01-01", "2030-01-01")
	if err != nil {
		t.Fatalf("insert accepted order: %v (storeID=%d)", err, storeID)
	}
	id1, _ := res.LastInsertId()

	// 顾客取消 → CANCEL_REQUESTED。
	result, err := ordStore.Cancel(ctx, orders.CancelInput{StoreID: storeID, OrderID: id1, CustomerID: customerID, Actor: "customer"})
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if result.NewState != orders.StateCancelRequested {
		t.Fatalf("state = %s, want CANCEL_REQUESTED", result.NewState)
	}

	// 店长审核通过 → REFUNDING。
	if err := ordStore.ReviewCancelRequest(ctx, storeID, id1, 1, true); err != nil {
		t.Fatalf("approve: %v", err)
	}
	o, _ := ordStore.GetByID(ctx, storeID, id1)
	if o.FulfillmentState != orders.StateRefunding {
		t.Fatalf("after approve state = %s, want REFUNDING", o.FulfillmentState)
	}

	// 驳回 → 恢复 ACCEPTED。
	res2, err := db.ExecContext(ctx,
		`INSERT INTO orders (store_id, customer_id, order_no, scenario, pickup_type, item_amount_cents, payable_cents, paid_cents, fulfillment_state, version, quote_token, quote_expires_at, expires_at, paid_at)
		 VALUES (?, ?, ?, 'PICKUP','IMMEDIATE', 1000,1000,1000,'CANCEL_REQUESTED',1,?,?,?, NOW())`,
		storeID, customerID, fmt.Sprintf("RJ%d", c2), fmt.Sprintf("qR%d", c2), "2030-01-01", "2030-01-01")
	if err != nil {
		t.Fatalf("insert cancel_requested order: %v", err)
	}
	id2, _ := res2.LastInsertId()
	if err := ordStore.ReviewCancelRequest(ctx, storeID, id2, 1, false); err != nil {
		t.Fatalf("reject: %v", err)
	}
	o2, _ := ordStore.GetByID(ctx, storeID, id2)
	if o2.FulfillmentState != orders.StateAccepted {
		t.Fatalf("after reject state = %s, want ACCEPTED", o2.FulfillmentState)
	}
}
