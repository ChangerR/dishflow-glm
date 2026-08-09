// Package inventory 实现每日库存调整与库存流水（PRD §7.3/§4.7/§17.2）。
//
// 规则：
//   - 对每日库存 SKU 支持正数增加、负数扣减，调整原因必填。
//   - 调整必须验证不会使可用库存违反 reserved + sold <= target（不变量）。
//   - 请求必须带幂等键；写库存流水和审计。
//   - 人工售罄优先于剩余库存；无限库存不应因每日库存表缺失被判售罄（PRD §7.3）。
package inventory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// MovementType 库存流水类型。
type MovementType string

const (
	MovementReserve MovementType = "RESERVE"
	MovementFulfill MovementType = "FULFILL"
	MovementRelease MovementType = "RELEASE"
	MovementAdjust  MovementType = "ADJUST"
)

// Store 库存域持久化。
type Store struct {
	db *sql.DB
}

// NewStore 创建库存存储。
func NewStore(db *sql.DB) *Store { return &Store{db: db} }

// DailyInventory 表示某 SKU 某业务日的库存。
type DailyInventory struct {
	StoreID      int64
	SKUID        int64
	BusinessDate string // YYYY-MM-DD（门店本地业务日）
	TargetQty    int
	ReservedQty  int
	SoldQty      int
}

// EnsureDaily 确保某 SKU 某业务日的 daily_inventory 行存在；
// 若不存在则用 SKU 的 daily_stock 初始化（PRD reservation-pickup §5.3）。
func (s *Store) EnsureDaily(ctx context.Context, storeID, skuID int64, businessDate string, defaultQty int) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT IGNORE INTO daily_inventory (store_id, sku_id, business_date, target_qty, reserved_qty, sold_qty)
		 VALUES (?,?,?, ?, 0, 0)`,
		storeID, skuID, businessDate, defaultQty)
	return err
}

// GetDaily 取某 SKU 某业务日库存。
func (s *Store) GetDaily(ctx context.Context, storeID, skuID int64, businessDate string) (DailyInventory, error) {
	var di DailyInventory
	err := s.db.QueryRowContext(ctx,
		`SELECT store_id, sku_id, business_date, target_qty, reserved_qty, sold_qty
		 FROM daily_inventory WHERE store_id = ? AND sku_id = ? AND business_date = ?`,
		storeID, skuID, businessDate).
		Scan(&di.StoreID, &di.SKUID, &di.BusinessDate, &di.TargetQty, &di.ReservedQty, &di.SoldQty)
	if errors.Is(err, sql.ErrNoRows) {
		return DailyInventory{}, sql.ErrNoRows
	}
	return di, err
}

// AdjustInput 库存调整输入（PRD §7.3）。
type AdjustInput struct {
	StoreID     int64
	SKUID       int64
	BusinessDate string
	Delta       int // 正数增加、负数扣减
	Reason      string
	OperatorID  int64 // 后台账号 ID（0=系统）
	IdempotencyKey string
}

// Adjust 调整每日库存目标数（PRD §7.3）。
// 事务内：行锁校验不变量、更新 target、写流水、幂等键。
// 失败原因：reason 空、delta=0、违反不变量、重复幂等键。
func (s *Store) Adjust(ctx context.Context, in AdjustInput) error {
	if in.Delta == 0 {
		return errors.New("delta must be non-zero")
	}
	if in.Reason == "" {
		return errors.New("reason required")
	}
	if in.IdempotencyKey == "" {
		return errors.New("idempotency key required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// 行锁取当前库存。
	var target, reserved, sold int
	err = tx.QueryRowContext(ctx,
		`SELECT target_qty, reserved_qty, sold_qty
		 FROM daily_inventory WHERE store_id = ? AND sku_id = ? AND business_date = ? FOR UPDATE`,
		in.StoreID, in.SKUID, in.BusinessDate).Scan(&target, &reserved, &sold)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("no daily inventory for sku %d on %s", in.SKUID, in.BusinessDate)
		}
		return err
	}
	newTarget := target + in.Delta
	if newTarget < 0 {
		return fmt.Errorf("adjusted target %d must be >= 0", newTarget)
	}
	// 不变量：reserved + sold <= target（PRD §17.2）。
	if reserved+sold > newTarget {
		return fmt.Errorf("adjustment would violate invariant: need target>=%d, got %d", reserved+sold, newTarget)
	}

	res, err := tx.ExecContext(ctx,
		`UPDATE daily_inventory SET target_qty = ? WHERE store_id = ? AND sku_id = ? AND business_date = ?`,
		newTarget, in.StoreID, in.SKUID, in.BusinessDate)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	// 流水（delta_sold=0，调整只动 target；记录 target 变化便于审计）。
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO inventory_movements (store_id, sku_id, business_date, movement_type, delta_reserved, delta_sold, reason, operator_admin_user_id)
		 VALUES (?, ?, ?, 'ADJUST', 0, ?, ?, ?)`,
		in.StoreID, in.SKUID, in.BusinessDate, in.Delta, in.Reason, nullInt64(in.OperatorID)); err != nil {
		return err
	}
	// 幂等键（subject 含 sku+date，key 唯一）。
	subject := fmt.Sprintf("stock-adjust:%d:%d:%s", in.StoreID, in.SKUID, in.BusinessDate)
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO idempotency_keys (idem_key, subject, request_hash, status_code, response_body)
		 VALUES (?, ?, '', 200, NULL)`, in.IdempotencyKey, subject); err != nil {
		return fmt.Errorf("idempotency conflict: %w", err)
	}
	return tx.Commit()
}

// ListMovements 列出 SKU 库存流水（倒序）。
func (s *Store) ListMovements(ctx context.Context, storeID, skuID int64, businessDate string, limit, offset int) ([]map[string]any, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, sku_id, business_date, movement_type, delta_reserved, delta_sold, order_id, reason, operator_admin_user_id, created_at
		 FROM inventory_movements WHERE store_id = ? AND sku_id = ? AND (? = '' OR business_date = ?)
		 ORDER BY id DESC LIMIT ? OFFSET ?`,
		storeID, skuID, businessDate, businessDate, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var id, dr, ds int64
		var sku int64
		var date, mtype, reason, created string
		var orderID, opID sql.NullInt64
		if err := rows.Scan(&id, &sku, &date, &mtype, &dr, &ds, &orderID, &reason, &opID, &created); err != nil {
			return nil, err
		}
		entry := map[string]any{
			"id": id, "sku_id": sku, "business_date": date, "movement_type": mtype,
			"delta_reserved": dr, "delta_sold": ds, "reason": reason, "created_at": created,
		}
		if orderID.Valid {
			entry["order_id"] = orderID.Int64
		}
		if opID.Valid {
			entry["operator_admin_user_id"] = opID.Int64
		}
		out = append(out, entry)
	}
	return out, rows.Err()
}

func nullInt64(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}

// 抑制 time 未用（预留扩展）。
var _ = time.Now
