// Package platform 实现平台级管理：门店、后台账号、门店成员、开店/加入申请与审批（PRD §10/§3.4/§3.5）。
//
// 关键规则：
//   - 每店唯一店主（PRD §3.4/§10.4）：设某成员为 OWNER 时其它 OWNER 自动降为 MANAGER。
//   - 普通账号最多归属一家门店（PRD §2.2）：指定店主冲突需先解除旧关系。
//   - 平台审批开店为事务：建店 + 把申请人设为 OWNER，不可重复（PRD §10.4）。
//   - 平台账号操作写平台级审计日志（store_id NULL, platform_scope=1）。
package platform

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/dishflow/zshop/internal/security"
)

// Store 提供平台域持久化。
type Store struct {
	db *sql.DB
}

// NewStore 创建平台存储。
func NewStore(db *sql.DB) *Store { return &Store{db: db} }

// normalizeLogin 标准化登录账号（去空白 + 转小写）。
func normalizeLogin(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// Shop 表示一条门店。
type Shop struct {
	ID                       int64
	Name                     string
	Enabled                  bool
	BusinessOpen             bool
	Phone                    string
	Address                  string
	BusinessHours            string
	Announcement             string
	PickupMinutes            int
	ScheduledPickupEnabled   bool
	PickupAdvanceDays        int
	PickupSlotMinutes        int
	PickupSlotCapacity       int
	PickupMinLeadMinutes     int
	Timezone                 string
	PointsPerYuan            int
	NewMemberCouponTemplateID sql.NullInt64
	CreatedAt                string
	UpdatedAt                string
}

// AdminAccount 表示后台账号（不含密钥）。
type AdminAccount struct {
	ID              int64
	Login           string
	DisplayName     string
	Enabled         bool
	IsPlatformAdmin bool
	LastLoginAt     sql.NullTime
	CreatedAt       string
}

// Member 表示门店成员关系。
type Member struct {
	ID          int64
	StoreID     int64
	AdminUserID int64
	Role        string
	Login       string
	DisplayName string
	CreatedAt   string
}

// CreateStoreInput 创建门店输入。
type CreateStoreInput struct {
	Name      string
	Timezone  string
	Phone     string
	Address   string
}

// CreateStore 插入门店并返回 ID（默认预约配置由 DB DEFAULT 提供）。
func (s *Store) CreateStore(ctx context.Context, in CreateStoreInput) (int64, error) {
	if in.Timezone == "" {
		in.Timezone = "Asia/Shanghai"
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO stores (name, timezone, phone, address) VALUES (?,?,?,?)`,
		in.Name, in.Timezone, in.Phone, in.Address)
	if err != nil {
		// 检测唯一约束等
		return 0, fmt.Errorf("create store: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	return id, nil
}

// GetShop 按 ID 查门店。
func (s *Store) GetShop(ctx context.Context, id int64) (Shop, error) {
	var st Shop
	var role sql.NullInt64 // 不需要
	_ = role
	err := s.db.QueryRowContext(ctx,
		`SELECT id, name, enabled, business_open, phone, address, business_hours, announcement,
		        pickup_minutes, scheduled_pickup_enabled, pickup_advance_days, pickup_slot_minutes,
		        pickup_slot_capacity, pickup_min_lead_minutes, timezone, points_per_yuan,
		        new_member_coupon_template_id, created_at, updated_at
		 FROM stores WHERE id = ?`, id).
		Scan(&st.ID, &st.Name, &st.Enabled, &st.BusinessOpen, &st.Phone, &st.Address, &st.BusinessHours,
			&st.Announcement, &st.PickupMinutes, &st.ScheduledPickupEnabled, &st.PickupAdvanceDays,
			&st.PickupSlotMinutes, &st.PickupSlotCapacity, &st.PickupMinLeadMinutes, &st.Timezone,
			&st.PointsPerYuan, &st.NewMemberCouponTemplateID, &st.CreatedAt, &st.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Shop{}, sql.ErrNoRows
	}
	return st, err
}

// ListShops 分页列出门店。
func (s *Store) ListShops(ctx context.Context, limit int, cursor int64) ([]Shop, int64, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, enabled, business_open, phone, address, business_hours, announcement,
		        pickup_minutes, scheduled_pickup_enabled, pickup_advance_days, pickup_slot_minutes,
		        pickup_slot_capacity, pickup_min_lead_minutes, timezone, points_per_yuan,
		        new_member_coupon_template_id, created_at, updated_at
		 FROM stores WHERE id > ? ORDER BY id ASC LIMIT ?`, cursor, limit)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []Shop
	last := cursor
	for rows.Next() {
		var st Shop
		if err := rows.Scan(&st.ID, &st.Name, &st.Enabled, &st.BusinessOpen, &st.Phone, &st.Address, &st.BusinessHours,
			&st.Announcement, &st.PickupMinutes, &st.ScheduledPickupEnabled, &st.PickupAdvanceDays,
			&st.PickupSlotMinutes, &st.PickupSlotCapacity, &st.PickupMinLeadMinutes, &st.Timezone,
			&st.PointsPerYuan, &st.NewMemberCouponTemplateID, &st.CreatedAt, &st.UpdatedAt); err != nil {
			return nil, 0, err
		}
		last = st.ID
		out = append(out, st)
	}
	return out, last, rows.Err()
}

// UpdateStoreInput 门店更新字段。
type UpdateStoreInput struct {
	Name      *string
	Enabled   *bool
	Timezone  *string
}

// UpdateStore 部分更新门店（平台改名/启停，PRD §10.5）。
func (s *Store) UpdateStore(ctx context.Context, id int64, in UpdateStoreInput) error {
	sets := []string{}
	args := []any{}
	if in.Name != nil {
		sets = append(sets, "name = ?")
		args = append(args, *in.Name)
	}
	if in.Enabled != nil {
		sets = append(sets, "enabled = ?")
		args = append(args, *in.Enabled)
	}
	if in.Timezone != nil {
		sets = append(sets, "timezone = ?")
		args = append(args, *in.Timezone)
	}
	if len(sets) == 0 {
		return nil
	}
	args = append(args, id)
	res, err := s.db.ExecContext(ctx, "UPDATE stores SET "+strings.Join(sets, ", ")+" WHERE id = ?", args...)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// ── 后台账号 ─────────────────────────────────────────────────────────

// CreateAdminAccountInput 创建账号输入。
type CreateAdminAccountInput struct {
	Login           string
	DisplayName     string
	Password        string // 明文，内部 bcrypt
	IsPlatformAdmin bool
}

// CreateAdminAccount 创建后台账号。
func (s *Store) CreateAdminAccount(ctx context.Context, in CreateAdminAccountInput) (int64, error) {
	login := normalizeLogin(in.Login)
	if login == "" {
		return 0, errors.New("login required")
	}
	if err := security.ValidatePasswordLen(in.Password); err != nil {
		return 0, err
	}
	hash, err := security.HashPassword(in.Password)
	if err != nil {
		return 0, err
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO admin_users (login, display_name, password_hash, enabled, is_platform_admin) VALUES (?,?,?,1,?)`,
		login, in.DisplayName, hash, in.IsPlatformAdmin)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ListAdminAccounts 分页列出后台账号。
func (s *Store) ListAdminAccounts(ctx context.Context, limit int, cursor int64) ([]AdminAccount, int64, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, login, display_name, enabled, is_platform_admin, last_login_at, created_at
		 FROM admin_users WHERE id > ? ORDER BY id ASC LIMIT ?`, cursor, limit)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []AdminAccount
	last := cursor
	for rows.Next() {
		var a AdminAccount
		if err := rows.Scan(&a.ID, &a.Login, &a.DisplayName, &a.Enabled, &a.IsPlatformAdmin, &a.LastLoginAt, &a.CreatedAt); err != nil {
			return nil, 0, err
		}
		last = a.ID
		out = append(out, a)
	}
	return out, last, rows.Err()
}

// GetAdminAccount 按 ID 查账号。
func (s *Store) GetAdminAccount(ctx context.Context, id int64) (AdminAccount, error) {
	var a AdminAccount
	err := s.db.QueryRowContext(ctx,
		`SELECT id, login, display_name, enabled, is_platform_admin, last_login_at, created_at
		 FROM admin_users WHERE id = ?`, id).
		Scan(&a.ID, &a.Login, &a.DisplayName, &a.Enabled, &a.IsPlatformAdmin, &a.LastLoginAt, &a.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return AdminAccount{}, sql.ErrNoRows
	}
	return a, err
}

// UpdateAdminAccountInput 账号更新字段（登录账号不可改，PRD §10.5）。
type UpdateAdminAccountInput struct {
	DisplayName     *string
	Enabled         *bool
	IsPlatformAdmin *bool
	// 新密码（明文，非 nil 时重设）
	NewPassword *string
}

// UpdateAdminAccount 更新账号。
func (s *Store) UpdateAdminAccount(ctx context.Context, id int64, in UpdateAdminAccountInput) error {
	sets := []string{}
	args := []any{}
	if in.DisplayName != nil {
		sets = append(sets, "display_name = ?")
		args = append(args, *in.DisplayName)
	}
	if in.Enabled != nil {
		sets = append(sets, "enabled = ?")
		args = append(args, *in.Enabled)
	}
	if in.IsPlatformAdmin != nil {
		sets = append(sets, "is_platform_admin = ?")
		args = append(args, *in.IsPlatformAdmin)
	}
	if in.NewPassword != nil {
		if err := security.ValidatePasswordLen(*in.NewPassword); err != nil {
			return err
		}
		hash, err := security.HashPassword(*in.NewPassword)
		if err != nil {
			return err
		}
		sets = append(sets, "password_hash = ?")
		args = append(args, hash)
	}
	if len(sets) == 0 {
		return nil
	}
	args = append(args, id)
	res, err := s.db.ExecContext(ctx, "UPDATE admin_users SET "+strings.Join(sets, ", ")+" WHERE id = ?", args...)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}
