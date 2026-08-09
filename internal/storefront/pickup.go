package storefront

import (
	"context"
	"database/sql"
	"errors"

	"github.com/dishflow/zshop/internal/pickup"
)

// PickupConfig 取门店预约配置（reservation-pickup §5.1）。
func (s *Store) PickupConfig(ctx context.Context, storeID int64) (pickup.Config, error) {
	var cfg pickup.Config
	var enabled int
	var hours string
	err := s.db.QueryRowContext(ctx,
		`SELECT scheduled_pickup_enabled, business_hours, pickup_advance_days, pickup_slot_minutes,
		        pickup_slot_capacity, pickup_min_lead_minutes, timezone
		 FROM stores WHERE id = ?`, storeID).
		Scan(&enabled, &hours, &cfg.AdvanceDays, &cfg.SlotMinutes, &cfg.SlotCapacity, &cfg.MinLeadMinutes, &cfg.Timezone)
	if errors.Is(err, sql.ErrNoRows) {
		return pickup.Config{}, sql.ErrNoRows
	}
	cfg.Enabled = enabled == 1
	cfg.BusinessHours = hours
	return cfg, nil
}

// ReservedBySlot 取某日期各时段已占用订单数（key=门店本地 "YYYY-MM-DD HH:MM"，PRD §4.6）。
func (s *Store) ReservedBySlot(ctx context.Context, storeID int64, dateStr string) (map[string]int, error) {
	// 当日所有 pickup_slot_capacity 行（reserved_orders）。
	rows, err := s.db.QueryContext(ctx,
		`SELECT scheduled_for, reserved_orders FROM pickup_slot_capacity WHERE store_id = ? AND DATE(scheduled_for) = ?`,
		storeID, dateStr)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var scheduled string
		var reserved int
		if err := rows.Scan(&scheduled, &reserved); err != nil {
			return nil, err
		}
		// scheduled_for 是 DATETIME（UTC 存储但语义为门店本地）；
		// 这里按门店时区格式化为 "YYYY-MM-DD HH:MM" 与 GenerateSlots 的 key 对齐。
		out[scheduled] = reserved
	}
	return out, rows.Err()
}
