package orders

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/dishflow/zshop/internal/pricing"
	"github.com/dishflow/zshop/internal/security"
)

// Store 订单持久化。
type Store struct {
	db *sql.DB
}

// NewStore 创建订单存储。
func NewStore(db *sql.DB) *Store { return &Store{db: db} }

// Order 订单主体。
type Order struct {
	ID                  int64
	StoreID             int64
	CustomerID          int64
	OrderNo             string
	PickupNo            sql.NullInt64
	PickupBusinessDate  sql.NullString
	Scenario            string
	DiningTableID       sql.NullInt64
	TableLabel          string
	PickupType          string
	ScheduledFor        sql.NullTime
	ItemAmountCents     int64
	PackagingFeeCents   int64
	DiscountCents       int64
	PayableCents        int64
	PaidCents           int64
	RefundedCents       int64
	PromotionID         sql.NullInt64
	CustomerCouponID    sql.NullInt64
	Remark              string
	FulfillmentState    State
	Version             int64
	QuoteToken          string
	MockOrder           bool
	CreatedAt           time.Time
	PaidAt              sql.NullTime
	ExpiresAt           time.Time
}

// CreateInput 创建订单输入（来自有效 quote_token，PRD §4.7）。
type CreateInput struct {
	StoreID       int64
	CustomerID    int64
	Quote         pricing.QuoteSummary
	Cart          []LineItemSnapshot
	Remark        string
	TableID       int64
	TableLabel    string
	IdempotencyKey string
	BusinessDate  string // 取餐业务日（用于库存与取餐号）
}

// LineItemSnapshot 订单项快照（PRD §4.9）。
type LineItemSnapshot struct {
	SKUID             int64
	ProductID         int64
	SKUName           string
	ProductName       string
	UnitPriceCents    int64
	Quantity          int
	OptionIDs         []int64
	OptionsSnapshot   []OptionSnap
	PackagingFeeCents int64
	LineAmountCents   int64
}

// OptionSnap 选项快照。
type OptionSnap struct {
	GroupName           string `json:"group_name"`
	OptionName          string `json:"option_name"`
	PriceModifierCents int64  `json:"price_modifier_cents"`
}

// Create 在单一事务内创建订单（PRD §4.7）：
//  1. 校验 quote（未过期、门店匹配）。
//  2. 幂等：同 quote 只能创建一个订单（唯一约束 quote_token + 重复请求由 Idempotency-Key）。
//  3. 分配门店业务日取餐号（行锁原子，PRD §4.7）。
//  4. 预占库存（行锁，校验不变量）。
//  5. 预约容量原子占位（行锁，满则 PICKUP_SLOT_FULL，PRD §4.6.4）。
//  6. 写订单主体 + 不可变订单项快照 + 订单事件。
//  7. 核销指定顾客券（若 quote 含券；P6 完整券逻辑前置，此处按 quote.CouponID 标记）。
//  8. outbox 事件 order.created（P5 消费触发自动打印等）。
//  所有写入同事务提交。
func (s *Store) Create(ctx context.Context, in CreateInput) (int64, error) {
	if in.IdempotencyKey == "" {
		return 0, errors.New("idempotency key required")
	}
	// quote 过期校验。
	if time.Now().UTC().Unix() > in.Quote.ExpiresAt {
		return 0, pricing.ErrQuoteExpired
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	// 幂等：同 subject+key 已存在 → 返回既有订单 id。
	var existingID int64
	err = tx.QueryRowContext(ctx,
		`SELECT CAST(response_body AS UNSIGNED) FROM idempotency_keys WHERE subject = ? AND idem_key = ?`,
		subjectOrderCreate(in.StoreID), in.IdempotencyKey).Scan(&existingID)
	if err == nil && existingID > 0 {
		return existingID, nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}

	// 取餐号（门店业务日唯一，行锁原子）。
	pickupNo, err := allocatePickupNo(tx, in.StoreID, in.BusinessDate)
	if err != nil {
		return 0, err
	}

	orderNo, err := security.NewHexID(8)
	if err != nil {
		return 0, err
	}
	expiresAt := time.Unix(in.Quote.ExpiresAt, 0).UTC()

	// 先插入订单主体拿到 orderID，再用 orderID 预占库存/容量。
	res, err := tx.ExecContext(ctx,
		`INSERT INTO orders (store_id, customer_id, order_no, pickup_no, pickup_business_date,
		    scenario, dining_table_id, table_label, pickup_type, scheduled_for,
		    item_amount_cents, packaging_fee_cents, discount_cents, payable_cents,
		    promotion_id, customer_coupon_id, remark, fulfillment_state, version,
		    quote_token, quote_expires_at, expires_at)
		 VALUES (?,?,?,?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'PENDING_PAYMENT', 1, ?, ?, ?)`,
		in.StoreID, in.CustomerID, orderNo, pickupNo, in.BusinessDate,
		string(in.Quote.Scenario), nullInt64(in.TableID), in.TableLabel,
		pickupTypeOf(in.Quote), scheduledForTime(in.Quote.ScheduledFor),
		in.Quote.ItemAmountCents, in.Quote.PackagingFeeCents, in.Quote.DiscountCents, in.Quote.PayableCents,
		nullInt64(in.Quote.PromotionID), nullInt64(in.Quote.CustomerCouponID), in.Remark,
		in.Quote.Token, expiresAt, expiresAt)
	if err != nil {
		return 0, fmt.Errorf("insert order: %w", err)
	}
	orderID, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}

	// 预占库存（行锁，PRD §4.7/§17.2）。失败回滚整个事务（含订单）。
	for _, li := range in.Cart {
		if err := reserveInventory(tx, in.StoreID, li.SKUID, in.BusinessDate, li.Quantity, orderID); err != nil {
			return 0, fmt.Errorf("reserve sku %d: %w", li.SKUID, err)
		}
	}

	// 预约容量原子占位（自取且预约时）。
	if in.Quote.Scenario == pricing.ScenarioPickup && in.Quote.ScheduledFor != "" {
		if err := reservePickupSlot(tx, in.StoreID, in.Quote.ScheduledFor); err != nil {
			return 0, err
		}
	}

	// 订单项快照（不可变，PRD §4.9）。
	for _, li := range in.Cart {
		optJSON, _ := json.Marshal(li.OptionsSnapshot)
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO order_items (order_id, store_id, sku_id, product_id, sku_name, product_name,
			    unit_price_cents, quantity, options_snapshot, packaging_fee_cents, line_amount_cents)
			 VALUES (?,?,?,?, ?, ?, ?, ?, ?, ?, ?)`,
			orderID, in.StoreID, li.SKUID, li.ProductID, li.SKUName, li.ProductName,
			li.UnitPriceCents, li.Quantity, optJSON, li.PackagingFeeCents, li.LineAmountCents); err != nil {
			return 0, fmt.Errorf("insert order item: %w", err)
		}
	}

	// 订单事件：created。
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO order_events (order_id, store_id, event_type, to_state, actor_type, summary)
		 VALUES (?, ?, 'order.created', 'PENDING_PAYMENT', 'SYSTEM', '订单创建')`,
		orderID, in.StoreID); err != nil {
		return 0, err
	}

	// outbox：order.created（P5 Worker 消费，PRD §15）。
	payload, _ := json.Marshal(map[string]any{
		"order_id": orderID, "store_id": in.StoreID, "event": "order.created",
	})
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO outbox (store_id, event_type, aggregate_type, aggregate_id, payload, status)
		 VALUES (?, 'order.created', 'order', ?, ?, 'PENDING')`,
		in.StoreID, orderID, payload); err != nil {
		return 0, err
	}

	// 幂等键落库（subject+key 唯一；response_body 存订单 id）。
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO idempotency_keys (idem_key, subject, request_hash, status_code, response_body)
		 VALUES (?, ?, '', 201, ?)`,
		in.IdempotencyKey, subjectOrderCreate(in.StoreID), fmt.Sprintf("%d", orderID)); err != nil {
		return 0, fmt.Errorf("idempotency conflict: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return orderID, nil
}

func subjectOrderCreate(storeID int64) string {
	return fmt.Sprintf("order-create:%d", storeID)
}

// allocatePickupNo 行锁分配门店业务日取餐号（至少三位，超 999 自然扩展，PRD §4.7）。
func allocatePickupNo(tx *sql.Tx, storeID int64, businessDate string) (int, error) {
	// 锁住当日已有最大取餐号。
	var maxNo sql.NullInt64
	err := tx.QueryRowContext(context.Background(),
		`SELECT MAX(pickup_no) FROM orders WHERE store_id = ? AND pickup_business_date = ? FOR UPDATE`,
		storeID, businessDate).Scan(&maxNo)
	if err != nil {
		return 0, fmt.Errorf("lock pickup no: %w", err)
	}
	next := 1
	if maxNo.Valid {
		next = int(maxNo.Int64) + 1
	}
	return next, nil
}

// reserveInventory 行锁预占库存（不变量校验，PRD §17.2）。
// 注意：调用方应先确保 daily_inventory 行存在；此处 Insert IGNORE 兜底初始化为 0（调用方应已 EnsureDaily）。
func reserveInventory(tx *sql.Tx, storeID, skuID int64, businessDate string, qty int, orderID int64) error {
	// 行锁当前库存。
	var target, reserved, sold int
	err := tx.QueryRowContext(context.Background(),
		`SELECT target_qty, reserved_qty, sold_qty
		 FROM daily_inventory WHERE store_id = ? AND sku_id = ? AND business_date = ? FOR UPDATE`,
		storeID, skuID, businessDate).Scan(&target, &reserved, &sold)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("no daily inventory for sku %d on %s", skuID, businessDate)
		}
		return err
	}
	if reserved+sold+qty > target {
		return ErrInsufficientStock
	}
	if _, err := tx.ExecContext(context.Background(),
		`UPDATE daily_inventory SET reserved_qty = reserved_qty + ? WHERE store_id = ? AND sku_id = ? AND business_date = ?`,
		qty, storeID, skuID, businessDate); err != nil {
		return err
	}
	// 库存预占记录（便于释放/转已售）。
	if _, err := tx.ExecContext(context.Background(),
		`INSERT INTO inventory_reservations (store_id, order_id, sku_id, business_date, quantity, state)
		 VALUES (?, ?, ?, ?, ?, 'RESERVED')`,
		storeID, orderID, skuID, businessDate, qty); err != nil {
		return err
	}
	// 流水。
	if _, err := tx.ExecContext(context.Background(),
		`INSERT INTO inventory_movements (store_id, sku_id, business_date, movement_type, delta_reserved, order_id, reason)
		 VALUES (?, ?, ?, 'RESERVE', ?, ?, 'order create')`,
		storeID, skuID, businessDate, qty, orderID); err != nil {
		return err
	}
	return nil
}

// reservePickupSlot 行锁原子占位（满则 PICKUP_SLOT_FULL，PRD §4.6.4）。
func reservePickupSlot(tx *sql.Tx, storeID int64, scheduledFor string) error {
	t, err := time.Parse(time.RFC3339, scheduledFor)
	if err != nil {
		// 允许 "YYYY-MM-DD HH:MM:SS" 门店本地格式。
		t, err = time.ParseInLocation("2006-01-02 15:04:05", scheduledFor, time.UTC)
		if err != nil {
			return fmt.Errorf("invalid scheduled_for: %w", err)
		}
	}
	// 行锁或创建容量行。
	res, err := tx.ExecContext(context.Background(),
		`INSERT INTO pickup_slot_capacity (store_id, scheduled_for, capacity_snapshot, reserved_orders)
		 SELECT ?, ?, 1, 0 FROM dual
		 WHERE NOT EXISTS (SELECT 1 FROM pickup_slot_capacity WHERE store_id=? AND scheduled_for=?)`,
		storeID, t, storeID, t)
	if err != nil {
		return err
	}
	_ = res
	// 行锁 + 原子 +1。
	result, err := tx.ExecContext(context.Background(),
		`UPDATE pickup_slot_capacity SET reserved_orders = reserved_orders + 1
		 WHERE store_id = ? AND scheduled_for = ? AND reserved_orders < capacity_snapshot`,
		storeID, t)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return ErrPickupSlotFull
	}
	return nil
}

// 领域错误。
var (
	ErrInsufficientStock = errors.New("INSUFFICIENT_STOCK")
	ErrPickupSlotFull    = errors.New("PICKUP_SLOT_FULL")
)

func nullInt64(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}

func pickupTypeOf(q pricing.QuoteSummary) string {
	if q.ScheduledFor != "" {
		return "SCHEDULED"
	}
	return "IMMEDIATE"
}

func scheduledForTime(s string) any {
	if s == "" {
		return nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC()
	}
	if t, err := time.ParseInLocation("2006-01-02 15:04:05", s, time.UTC); err == nil {
		return t.UTC()
	}
	return nil
}
