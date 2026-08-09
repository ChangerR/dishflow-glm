package menu

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dishflow/zshop/internal/security"
)

// timeSinceDays 返回 t 距 UTC now 的天数（向上取整）。
func timeSinceDays(t time.Time) int {
	d := time.Since(t.UTC())
	if d < 0 {
		return 0
	}
	return int(d / (24 * time.Hour))
}

// ── 菜品 ──────────────────────────────────────────────────────────────

// Product 菜品。
type Product struct {
	ID                 int64
	StoreID            int64
	CategoryID         int64
	Code               string
	Name               string
	Description        string
	ImageURL           string
	Enabled            bool
	ManuallySoldOut    bool
	SortOrder          int
	PackagingFeeCents  int64
	DeletedAt          sql.NullTime
	DeleteBatchID      string
	CreatedAt          string
	UpdatedAt          string
}

// SKU 菜品规格。
type SKU struct {
	ID            int64
	StoreID       int64
	ProductID     int64
	Name          string
	PriceCents    int64
	InventoryMode string // UNLIMITED | DAILY
	DailyStock    int
	Enabled       bool
	IsDefault     bool
	SortOrder     int
	DeletedAt     sql.NullTime
	DeleteBatchID string
}

// OptionGroup 选项组。
type OptionGroup struct {
	ID            int64
	StoreID       int64
	ProductID     int64
	Name          string
	SelectionType string // SINGLE | MULTI
	IsRequired    bool
	MinSelect     int
	MaxSelect     int
	SortOrder     int
	DeletedAt     sql.NullTime
	DeleteBatchID string
}

// OptionItem 选项项。
type OptionItem struct {
	ID                 int64
	StoreID            int64
	OptionGroupID      int64
	Name               string
	PriceModifierCents int64
	Enabled            bool
	IsDefault          bool
	SortOrder          int
	DeletedAt          sql.NullTime
	DeleteBatchID      string
}

// CreateProductInput 创建菜品（含 SKU/选项组）。
type CreateProductInput struct {
	StoreID           int64
	CategoryID        int64
	Code              string
	Name              string
	Description       string
	ImageURL          string
	Enabled           bool
	ManuallySoldOut   bool
	SortOrder         int
	PackagingFeeCents int64
	SKUs              []SKUInput
	OptionGroups      []OptionGroupInput
}

// SKUInput SKU 输入。
type SKUInput struct {
	Name          string
	PriceCents    int64
	InventoryMode string
	DailyStock    int
	Enabled       bool
	IsDefault     bool
	SortOrder     int
}

// OptionGroupInput 选项组输入。
type OptionGroupInput struct {
	Name          string
	SelectionType string
	IsRequired    bool
	MinSelect     int
	MaxSelect     int
	SortOrder     int
	Items         []OptionItemInput
}

// OptionItemInput 选项项输入。
type OptionItemInput struct {
	Name               string
	PriceModifierCents int64
	Enabled            bool
	IsDefault          bool
	SortOrder          int
}

// CreateProduct 事务内创建菜品及其 SKU/选项组/选项项（PRD §7.2）。
func (s *Store) CreateProduct(ctx context.Context, in CreateProductInput) (int64, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return 0, errors.New("product name required")
	}
	if len(in.SKUs) == 0 {
		return 0, errors.New("at least one SKU required")
	}
	if err := validateOptionGroups(in.OptionGroups); err != nil {
		return 0, err
	}
	// 校验 SKU 默认项（DAILY 库存非负等）。
	for _, sk := range in.SKUs {
		if sk.InventoryMode == "" {
			sk.InventoryMode = "UNLIMITED"
		}
		if sk.InventoryMode != "UNLIMITED" && sk.InventoryMode != "DAILY" {
			return 0, fmt.Errorf("invalid inventory mode %q", sk.InventoryMode)
		}
		if sk.DailyStock < 0 {
			return 0, errors.New("daily stock must be >= 0")
		}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx,
		`INSERT INTO products (store_id, category_id, code, name, description, image_url, enabled, manually_sold_out, sort_order, packaging_fee_cents)
		 VALUES (?,?,?,?,?,?,?,?,?,?)`,
		in.StoreID, in.CategoryID, in.Code, name, in.Description, in.ImageURL,
		in.Enabled, in.ManuallySoldOut, in.SortOrder, in.PackagingFeeCents)
	if err != nil {
		return 0, err
	}
	productID, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}

	for _, sk := range in.SKUs {
		mode := sk.InventoryMode
		if mode == "" {
			mode = "UNLIMITED"
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO skus (store_id, product_id, name, price_cents, inventory_mode, daily_stock, enabled, is_default, sort_order)
			 VALUES (?,?,?,?,?,?,?,?,?)`,
			in.StoreID, productID, sk.Name, sk.PriceCents, mode, sk.DailyStock, sk.Enabled, sk.IsDefault, sk.SortOrder); err != nil {
			return 0, err
		}
	}

	for _, og := range in.OptionGroups {
		st := og.SelectionType
		if st == "" {
			st = "SINGLE"
		}
		ogr, err := tx.ExecContext(ctx,
			`INSERT INTO option_groups (store_id, product_id, name, selection_type, is_required, min_select, max_select, sort_order)
			 VALUES (?,?,?,?,?,?,?,?)`,
			in.StoreID, productID, og.Name, st, og.IsRequired, og.MinSelect, og.MaxSelect, og.SortOrder)
		if err != nil {
			return 0, err
		}
		ogID, _ := ogr.LastInsertId()
		for _, oi := range og.Items {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO option_items (store_id, option_group_id, name, price_modifier_cents, enabled, is_default, sort_order)
				 VALUES (?,?,?,?,?,?,?)`,
				in.StoreID, ogID, oi.Name, oi.PriceModifierCents, oi.Enabled, oi.IsDefault, oi.SortOrder); err != nil {
				return 0, err
			}
		}
	}

	return productID, tx.Commit()
}

// validateOptionGroups 校验选项组规则（PRD §4.3）。
func validateOptionGroups(groups []OptionGroupInput) error {
	for _, g := range groups {
		st := g.SelectionType
		if st == "" {
			st = "SINGLE"
		}
		if st != "SINGLE" && st != "MULTI" {
			return fmt.Errorf("invalid selection type %q", g.SelectionType)
		}
		if g.MinSelect < 0 || g.MaxSelect < 1 || g.MinSelect > g.MaxSelect {
			return fmt.Errorf("invalid select range min=%d max=%d", g.MinSelect, g.MaxSelect)
		}
		// 默认项数校验。
		defaults := 0
		enabledDefaults := 0
		for _, oi := range g.Items {
			if oi.IsDefault {
				defaults++
				if oi.Enabled {
					enabledDefaults++
				}
			}
		}
		if st == "SINGLE" && defaults > 1 {
			return errors.New("single-select group may have at most one default option")
		}
		if st == "MULTI" && defaults > g.MaxSelect {
			return errors.New("multi-select defaults exceed max select")
		}
		if defaults != enabledDefaults {
			return errors.New("default option must be enabled")
		}
	}
	return nil
}

// GetProductDetail 取菜品详情（含 SKU/选项组/选项项，过滤回收站）。
func (s *Store) GetProductDetail(ctx context.Context, storeID, id int64) (Product, []SKU, []OptionGroup, map[int64][]OptionItem, error) {
	var p Product
	err := s.db.QueryRowContext(ctx,
		`SELECT id, store_id, category_id, code, name, description, image_url, enabled, manually_sold_out, sort_order, packaging_fee_cents, deleted_at, delete_batch_id, created_at, updated_at
		 FROM products WHERE id = ? AND store_id = ? AND deleted_at IS NULL`, id, storeID).
		Scan(&p.ID, &p.StoreID, &p.CategoryID, &p.Code, &p.Name, &p.Description, &p.ImageURL,
			&p.Enabled, &p.ManuallySoldOut, &p.SortOrder, &p.PackagingFeeCents, &p.DeletedAt,
			&p.DeleteBatchID, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Product{}, nil, nil, nil, sql.ErrNoRows
		}
		return Product{}, nil, nil, nil, err
	}
	skus, err := s.listSKUs(ctx, storeID, id)
	if err != nil {
		return Product{}, nil, nil, nil, err
	}
	ogs, itemsByGroup, err := s.listOptionGroups(ctx, storeID, id)
	if err != nil {
		return Product{}, nil, nil, nil, err
	}
	return p, skus, ogs, itemsByGroup, nil
}

// listSKUs 列出菜品的有效 SKU。
func (s *Store) listSKUs(ctx context.Context, storeID, productID int64) ([]SKU, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, store_id, product_id, name, price_cents, inventory_mode, daily_stock, enabled, is_default, sort_order, deleted_at, delete_batch_id
		 FROM skus WHERE store_id = ? AND product_id = ? AND deleted_at IS NULL ORDER BY sort_order, id`, storeID, productID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SKU
	for rows.Next() {
		var sk SKU
		if err := rows.Scan(&sk.ID, &sk.StoreID, &sk.ProductID, &sk.Name, &sk.PriceCents, &sk.InventoryMode,
			&sk.DailyStock, &sk.Enabled, &sk.IsDefault, &sk.SortOrder, &sk.DeletedAt, &sk.DeleteBatchID); err != nil {
			return nil, err
		}
		out = append(out, sk)
	}
	return out, rows.Err()
}

// listOptionGroups 列出菜品的有效选项组与其选项项。
func (s *Store) listOptionGroups(ctx context.Context, storeID, productID int64) ([]OptionGroup, map[int64][]OptionItem, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, store_id, product_id, name, selection_type, is_required, min_select, max_select, sort_order, deleted_at, delete_batch_id
		 FROM option_groups WHERE store_id = ? AND product_id = ? AND deleted_at IS NULL ORDER BY sort_order, id`, storeID, productID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var groups []OptionGroup
	for rows.Next() {
		var g OptionGroup
		if err := rows.Scan(&g.ID, &g.StoreID, &g.ProductID, &g.Name, &g.SelectionType, &g.IsRequired,
			&g.MinSelect, &g.MaxSelect, &g.SortOrder, &g.DeletedAt, &g.DeleteBatchID); err != nil {
			return nil, nil, err
		}
		groups = append(groups, g)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}

	// 一次查该菜品所有选项组下的有效选项项，再按组分组。
	itemsByGroup := map[int64][]OptionItem{}
	if len(groups) > 0 {
		ids := make([]any, len(groups))
		for i, g := range groups {
			ids[i] = g.ID
		}
		q := `SELECT id, option_group_id, name, price_modifier_cents, enabled, is_default, sort_order
		      FROM option_items WHERE option_group_id IN (?` + strings.Repeat(",?", len(ids)-1) + `) AND deleted_at IS NULL ORDER BY sort_order, id`
		irows, err := s.db.QueryContext(ctx, q, ids...)
		if err != nil {
			return nil, nil, err
		}
		defer irows.Close()
		for irows.Next() {
			var oi OptionItem
			if err := irows.Scan(&oi.ID, &oi.OptionGroupID, &oi.Name, &oi.PriceModifierCents, &oi.Enabled, &oi.IsDefault, &oi.SortOrder); err != nil {
				return nil, nil, err
			}
			itemsByGroup[oi.OptionGroupID] = append(itemsByGroup[oi.OptionGroupID], oi)
		}
	}
	return groups, itemsByGroup, nil
}

// ListProducts 列出门店菜品（可按分类过滤），不含回收站。
func (s *Store) ListProducts(ctx context.Context, storeID, categoryID int64, includeDeleted bool) ([]Product, error) {
	q := `SELECT id, store_id, category_id, code, name, description, image_url, enabled, manually_sold_out, sort_order, packaging_fee_cents, deleted_at, delete_batch_id, created_at, updated_at
	      FROM products WHERE store_id = ?`
	args := []any{storeID}
	if categoryID > 0 {
		q += ` AND category_id = ?`
		args = append(args, categoryID)
	}
	if !includeDeleted {
		q += ` AND deleted_at IS NULL`
	}
	q += ` ORDER BY sort_order, id`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Product
	for rows.Next() {
		var p Product
		if err := rows.Scan(&p.ID, &p.StoreID, &p.CategoryID, &p.Code, &p.Name, &p.Description, &p.ImageURL,
			&p.Enabled, &p.ManuallySoldOut, &p.SortOrder, &p.PackagingFeeCents, &p.DeletedAt,
			&p.DeleteBatchID, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// DeleteProduct 软删除菜品（PRD §7.2）。
func (s *Store) DeleteProduct(ctx context.Context, storeID, id int64) (string, error) {
	batchID, err := security.NewHexID(16)
	if err != nil {
		return "", err
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE products SET deleted_at = UTC_TIMESTAMP(3), delete_batch_id = ?, enabled = 0
		 WHERE id = ? AND store_id = ? AND deleted_at IS NULL`, batchID, id, storeID)
	if err != nil {
		return "", err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return "", sql.ErrNoRows
	}
	return batchID, nil
}

// RestoreProduct 恢复菜品：所属分类必须已恢复（PRD §7.2）。
func (s *Store) RestoreProduct(ctx context.Context, storeID, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var deletedAt sql.NullTime
	var categoryID int64
	err = tx.QueryRowContext(ctx,
		`SELECT deleted_at, category_id FROM products WHERE id = ? AND store_id = ? FOR UPDATE`, id, storeID).
		Scan(&deletedAt, &categoryID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return sql.ErrNoRows
		}
		return err
	}
	if !deletedAt.Valid {
		return errors.New("product not deleted")
	}
	if timeSinceDays(deletedAt.Time) > RecycleWindowDays {
		return errors.New("recycle window expired")
	}
	// 父分类必须已恢复。
	var catDeleted sql.NullTime
	if err := tx.QueryRowContext(ctx,
		`SELECT deleted_at FROM categories WHERE id = ? AND store_id = ?`, categoryID, storeID).Scan(&catDeleted); err != nil {
		return err
	}
	if catDeleted.Valid {
		return errors.New("parent category is deleted; restore it first")
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE products SET deleted_at = NULL, delete_batch_id = '' WHERE id = ? AND store_id = ?`, id, storeID); err != nil {
		return err
	}
	return tx.Commit()
}
