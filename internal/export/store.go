// Package export 实现门店数据导出与导入（PRD §11）。
//
// 规则：
//   - 格式 dishflow.store-export，版本 1，JSON。
//   - 不含支付/打印密钥、订单、顾客、会员、库存流水或审计。
//   - 菜单策略 replace：导入后旧菜单进回收站，新分类/菜品/SKU/选项以新 ID 创建。
//   - 导入属于不可撤销高风险操作，事务内应用。
package export

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

const FormatID = "dishflow.store-export"
const FormatVersion = 1

// File 导出/导入文件结构。
type File struct {
	Format    string    `json:"format"`
	Version   int       `json:"version"`
	ExportedAt time.Time `json:"exported_at"`
	SourceStoreID int64 `json:"source_store_id"`
	Store     *StoreSection   `json:"store,omitempty"`
	Menu      *MenuSection    `json:"menu,omitempty"`
	MiniProgram *MiniSection  `json:"miniprogram,omitempty"`
}

// StoreSection 基础信息分区。
type StoreSection struct {
	Name          string `json:"name"`
	Phone         string `json:"phone"`
	Address       string `json:"address"`
	BusinessHours string `json:"business_hours"`
	Announcement  string `json:"announcement"`
	PickupMinutes int    `json:"pickup_minutes"`
	Timezone      string `json:"timezone"`
}

// MiniSection 小程序配置分区。
type MiniSection struct {
	BrandName  string `json:"brand_name"`
	ThemeColor string `json:"theme_color"`
	LogoURL    string `json:"logo_url"`
}

// MenuSection 菜单分区。
type MenuSection struct {
	Categories []ExportCategory `json:"categories"`
}

// ExportCategory 导出分类。
type ExportCategory struct {
	Name      string          `json:"name"`
	SortOrder int             `json:"sort_order"`
	Products  []ExportProduct `json:"products"`
}

// ExportProduct 导出菜品。
type ExportProduct struct {
	Name              string           `json:"name"`
	Description       string           `json:"description"`
	ImageURL          string           `json:"image_url"`
	SortOrder         int              `json:"sort_order"`
	PackagingFeeCents int64            `json:"packaging_fee_cents"`
	SKUs              []ExportSKU      `json:"skus"`
	OptionGroups      []ExportOptGroup `json:"option_groups"`
}

// ExportSKU 导出 SKU。
type ExportSKU struct {
	Name          string `json:"name"`
	PriceCents    int64  `json:"price_cents"`
	InventoryMode string `json:"inventory_mode"`
	DailyStock    int    `json:"daily_stock"`
	SortOrder     int    `json:"sort_order"`
}

// ExportOptGroup 导出选项组。
type ExportOptGroup struct {
	Name          string           `json:"name"`
	SelectionType string           `json:"selection_type"`
	IsRequired    bool             `json:"is_required"`
	MinSelect     int              `json:"min_select"`
	MaxSelect     int              `json:"max_select"`
	Items         []ExportOptItem  `json:"items"`
}

// ExportOptItem 导出选项项。
type ExportOptItem struct {
	Name               string `json:"name"`
	PriceModifierCents int64  `json:"price_modifier_cents"`
	SortOrder          int    `json:"sort_order"`
}

// Store 导入导出。
type Store struct {
	db *sql.DB
}

// NewStore 创建导出存储。
func NewStore(db *sql.DB) *Store { return &Store{db: db} }

// Export 导出门店分区（基础信息/菜单/小程序配置，PRD §11）。
func (s *Store) Export(ctx context.Context, storeID int64) (File, error) {
	f := File{Format: FormatID, Version: FormatVersion, ExportedAt: time.Now().UTC(), SourceStoreID: storeID}

	// 基础信息。
	var name, phone, addr, hours, ann, tz string
	var pickup int
	if err := s.db.QueryRowContext(ctx,
		`SELECT name, phone, address, business_hours, announcement, pickup_minutes, timezone FROM stores WHERE id=?`, storeID).
		Scan(&name, &phone, &addr, &hours, &ann, &pickup, &tz); err != nil {
		return File{}, err
	}
	f.Store = &StoreSection{Name: name, Phone: phone, Address: addr, BusinessHours: hours, Announcement: ann, PickupMinutes: pickup, Timezone: tz}

	// 小程序配置。
	var brand, theme, logo string
	_ = s.db.QueryRowContext(ctx, `SELECT brand_name, theme_color, logo_url FROM miniprogram_config WHERE store_id=?`, storeID).Scan(&brand, &theme, &logo)
	f.MiniProgram = &MiniSection{BrandName: brand, ThemeColor: theme, LogoURL: logo}

	// 菜单（分类→菜品→SKU/选项）。
	cats, err := s.db.QueryContext(ctx,
		`SELECT id, name, sort_order FROM categories WHERE store_id=? AND deleted_at IS NULL ORDER BY sort_order`, storeID)
	if err != nil {
		return File{}, err
	}
	defer cats.Close()
	menu := MenuSection{}
	for cats.Next() {
		var catID int64
		var cname string
		var csort int
		if err := cats.Scan(&catID, &cname, &csort); err != nil {
			return File{}, err
		}
		ec := ExportCategory{Name: cname, SortOrder: csort}
		prods, err := s.exportProducts(ctx, storeID, catID)
		if err != nil {
			return File{}, err
		}
		ec.Products = prods
		menu.Categories = append(menu.Categories, ec)
	}
	f.Menu = &menu
	return f, nil
}

func (s *Store) exportProducts(ctx context.Context, storeID, catID int64) ([]ExportProduct, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, description, image_url, sort_order, packaging_fee_cents FROM products WHERE store_id=? AND category_id=? AND deleted_at IS NULL ORDER BY sort_order`,
		storeID, catID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ExportProduct
	for rows.Next() {
		var pid int64
		var p ExportProduct
		if err := rows.Scan(&pid, &p.Name, &p.Description, &p.ImageURL, &p.SortOrder, &p.PackagingFeeCents); err != nil {
			return nil, err
		}
		// SKU。
		srows, _ := s.db.QueryContext(ctx,
			`SELECT name, price_cents, inventory_mode, daily_stock, sort_order FROM skus WHERE product_id=? AND deleted_at IS NULL ORDER BY sort_order`, pid)
		for srows.Next() {
			var sk ExportSKU
			if err := srows.Scan(&sk.Name, &sk.PriceCents, &sk.InventoryMode, &sk.DailyStock, &sk.SortOrder); err != nil {
				srows.Close()
				return nil, err
			}
			p.SKUs = append(p.SKUs, sk)
		}
		srows.Close()
		// 选项组。
		p.OptionGroups, err = s.exportOptionGroups(ctx, pid)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

func (s *Store) exportOptionGroups(ctx context.Context, productID int64) ([]ExportOptGroup, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, selection_type, is_required, min_select, max_select FROM option_groups WHERE product_id=? AND deleted_at IS NULL ORDER BY id`, productID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ExportOptGroup
	for rows.Next() {
		var gid int64
		var req int
		var g ExportOptGroup
		if err := rows.Scan(&gid, &g.Name, &g.SelectionType, &req, &g.MinSelect, &g.MaxSelect); err != nil {
			return nil, err
		}
		g.IsRequired = req == 1
		irows, _ := s.db.QueryContext(ctx,
			`SELECT name, price_modifier_cents, sort_order FROM option_items WHERE option_group_id=? AND deleted_at IS NULL ORDER BY sort_order`, gid)
		for irows.Next() {
			var oi ExportOptItem
			if err := irows.Scan(&oi.Name, &oi.PriceModifierCents, &oi.SortOrder); err != nil {
				irows.Close()
				return nil, err
			}
			g.Items = append(g.Items, oi)
		}
		irows.Close()
		out = append(out, g)
	}
	return out, nil
}

// Import 导入：菜单 replace（旧菜单进回收站，新建），事务内（PRD §11）。
// overwriteAppid=true 时覆盖小程序 AppID（需校验全局唯一冲突）。
func (s *Store) Import(ctx context.Context, storeID int64, f File, overwriteAppid bool, appid string) error {
	if f.Format != FormatID || f.Version != FormatVersion {
		return errors.New("unsupported export format/version")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// AppID 覆盖冲突校验。
	if overwriteAppid && appid != "" {
		var cnt int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM miniprogram_config WHERE wechat_appid=? AND store_id<>?`, appid, storeID).Scan(&cnt); err != nil {
			return err
		}
		if cnt > 0 {
			return errors.New("WECHAT_APPID_CONFLICT: appid already used by another store")
		}
	}

	// 菜单 replace：旧菜单进回收站（同一批次）。
	batch, _ := newBatchID()
	if _, err := tx.ExecContext(ctx,
		`UPDATE categories SET deleted_at=UTC_TIMESTAMP(3), delete_batch_id=?, enabled=0 WHERE store_id=? AND deleted_at IS NULL`, batch, storeID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE products SET deleted_at=UTC_TIMESTAMP(3), delete_batch_id=?, enabled=0 WHERE store_id=? AND deleted_at IS NULL`, batch, storeID); err != nil {
		return err
	}

	// 新建分类/菜品/SKU/选项（新 ID）。
	if f.Menu != nil {
		for _, c := range f.Menu.Categories {
			res, err := tx.ExecContext(ctx,
				`INSERT INTO categories (store_id, name, enabled, sort_order) VALUES (?, ?, 1, ?)`, storeID, c.Name, c.SortOrder)
			if err != nil {
				return fmt.Errorf("insert category: %w", err)
			}
			catID, _ := res.LastInsertId()
			for _, p := range c.Products {
				res, err := tx.ExecContext(ctx,
					`INSERT INTO products (store_id, category_id, name, description, image_url, enabled, sort_order, packaging_fee_cents) VALUES (?, ?, ?, ?, ?, 1, ?, ?)`,
					storeID, catID, p.Name, p.Description, p.ImageURL, p.SortOrder, p.PackagingFeeCents)
				if err != nil {
					return fmt.Errorf("insert product: %w", err)
				}
				pid, _ := res.LastInsertId()
				for _, sk := range p.SKUs {
					mode := sk.InventoryMode
					if mode == "" {
						mode = "UNLIMITED"
					}
					if _, err := tx.ExecContext(ctx,
						`INSERT INTO skus (store_id, product_id, name, price_cents, inventory_mode, daily_stock, enabled, is_default, sort_order) VALUES (?, ?, ?, ?, ?, ?, 1, 0, ?)`,
						storeID, pid, sk.Name, sk.PriceCents, mode, sk.DailyStock, sk.SortOrder); err != nil {
						return err
					}
				}
				for _, og := range p.OptionGroups {
					st := og.SelectionType
					if st == "" {
						st = "SINGLE"
					}
					req := 0
					if og.IsRequired {
						req = 1
					}
					ogr, err := tx.ExecContext(ctx,
						`INSERT INTO option_groups (store_id, product_id, name, selection_type, is_required, min_select, max_select, sort_order) VALUES (?, ?, ?, ?, ?, ?, ?, 0)`,
						storeID, pid, og.Name, st, req, og.MinSelect, og.MaxSelect)
					if err != nil {
						return err
					}
					gid, _ := ogr.LastInsertId()
					for _, oi := range og.Items {
						if _, err := tx.ExecContext(ctx,
							`INSERT INTO option_items (store_id, option_group_id, name, price_modifier_cents, enabled, is_default, sort_order) VALUES (?, ?, ?, ?, 1, 0, ?)`,
							storeID, gid, oi.Name, oi.PriceModifierCents, oi.SortOrder); err != nil {
							return err
						}
					}
				}
			}
		}
	}

	// AppID 覆盖。
	if overwriteAppid && appid != "" {
		if _, err := tx.ExecContext(ctx, `UPDATE miniprogram_config SET wechat_appid=? WHERE store_id=?`, appid, storeID); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// ParseFile 解析导入 JSON（含大小校验 1.5MB，PRD §11）。
func ParseFile(body []byte) (File, error) {
	if len(body) == 0 || len(body) > 1.5*1024*1024 {
		return File{}, errors.New("import file must be 1..1536000 bytes JSON")
	}
	var f File
	if err := json.Unmarshal(body, &f); err != nil {
		return File{}, err
	}
	return f, nil
}

func newBatchID() (string, error) {
	// 简单 batch id（复用时间）。
	return fmt.Sprintf("imp_%d", time.Now().UnixNano()), nil
}
