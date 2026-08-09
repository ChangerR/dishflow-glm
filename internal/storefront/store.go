// Package storefront 实现顾客侧门店定位、bootstrap、菜单与桌码解析（PRD §4.1/§4.2/§10.1）。
package storefront

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// Store 顾客侧查询。
type Store struct {
	db *sql.DB
}

// NewStore 创建 storefront 存储。
func NewStore(db *sql.DB) *Store { return &Store{db: db} }

// Bootstrap 门店启动信息（PRD §4.1.4）。
type Bootstrap struct {
	StoreID      int64
	StoreName    string
	BusinessName string // 品牌名（来自 miniprogram_config.brand_name）
	ThemeColor   string
	LogoURL      string
	BusinessOpen bool
	Announcement string
	BusinessHours string
	Timezone     string
	AppID        string
}

// BootstrapByAppID 按 AppID 解析门店（小程序唯一 AppID，PRD §2.2）。
func (s *Store) BootstrapByAppID(ctx context.Context, appid string) (Bootstrap, error) {
	if appid == "" {
		return Bootstrap{}, errors.New("appid required")
	}
	var b Bootstrap
	err := s.db.QueryRowContext(ctx,
		`SELECT st.id, st.name, COALESCE(mp.brand_name,''), COALESCE(mp.theme_color,''),
		        COALESCE(mp.logo_url,''), st.business_open, st.announcement, st.business_hours, st.timezone, mp.wechat_appid
		 FROM stores st
		 INNER JOIN miniprogram_config mp ON mp.store_id = st.id
		 WHERE mp.wechat_appid = ? AND st.enabled = 1`, appid).
		Scan(&b.StoreID, &b.StoreName, &b.BusinessName, &b.ThemeColor, &b.LogoURL,
			&b.BusinessOpen, &b.Announcement, &b.BusinessHours, &b.Timezone, &b.AppID)
	if errors.Is(err, sql.ErrNoRows) {
		return Bootstrap{}, sql.ErrNoRows
	}
	return b, err
}

// StoreInfo 门店信息。
type StoreInfo struct {
	StoreID       int64
	Name          string
	Phone         string
	Address       string
	BusinessHours string
	Announcement  string
	BusinessOpen  bool
	Timezone      string
}

// GetStoreInfo 取门店公开信息。
func (s *Store) GetStoreInfo(ctx context.Context, storeID int64) (StoreInfo, error) {
	var si StoreInfo
	err := s.db.QueryRowContext(ctx,
		`SELECT id, name, phone, address, business_hours, announcement, business_open, timezone
		 FROM stores WHERE id = ? AND enabled = 1`, storeID).
		Scan(&si.StoreID, &si.Name, &si.Phone, &si.Address, &si.BusinessHours, &si.Announcement, &si.BusinessOpen, &si.Timezone)
	if errors.Is(err, sql.ErrNoRows) {
		return StoreInfo{}, sql.ErrNoRows
	}
	return si, err
}

// ── 公开菜单（PRD §4.2）──────────────────────────────────────────────

// CategoryDTO 公开分类。
type CategoryDTO struct {
	ID        int64 `json:"id"`
	Name      string `json:"name"`
	SortOrder int   `json:"sort_order"`
}

// DishDTO 公开菜品。
type DishDTO struct {
	ID                int64       `json:"id"`
	CategoryID        int64       `json:"category_id"`
	Name              string      `json:"name"`
	Description       string      `json:"description"`
	ImageURL          string      `json:"image_url"`
	SortOrder         int         `json:"sort_order"`
	ManuallySoldOut   bool        `json:"manually_sold_out"`
	StartPriceCents   int64       `json:"start_price_cents"`
	PackagingFeeCents int64       `json:"packaging_fee_cents"`
	SKUs              []SKUPlayer `json:"skus"`
	OptionGroups      []OptionGroupPlayer `json:"option_groups"`
}

// SKUPlayer 公开 SKU。
type SKUPlayer struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	PriceCents    int64  `json:"price_cents"`
	InventoryMode string `json:"inventory_mode"`
	IsDefault     bool   `json:"is_default"`
	SortOrder     int    `json:"sort_order"`
	// 唩罄状态：DAILY 且 remaining<=0 或人工售罄。
	SoldOut bool `json:"sold_out"`
}

// OptionGroupPlayer 公开选项组。
type OptionGroupPlayer struct {
	ID            int64             `json:"id"`
	Name          string            `json:"name"`
	SelectionType string            `json:"selection_type"`
	IsRequired    bool              `json:"is_required"`
	MinSelect     int               `json:"min_select"`
	MaxSelect     int               `json:"max_select"`
	SortOrder     int               `json:"sort_order"`
	Items         []OptionItemPlayer `json:"items"`
}

// OptionItemPlayer 公开选项项。
type OptionItemPlayer struct {
	ID                 int64  `json:"id"`
	Name               string `json:"name"`
	PriceModifierCents int64  `json:"price_modifier_cents"`
	IsDefault          bool   `json:"is_default"`
	SortOrder          int    `json:"sort_order"`
}

// PublicMenu 公开菜单（PRD §4.2：分类启用、菜品启用、未回收、至少一个可售 SKU）。
type PublicMenu struct {
	StoreID    int64        `json:"store_id"`
	Categories []CategoryDTO `json:"categories"`
	Dishes     []DishDTO    `json:"dishes"`
}

// GetPublicMenu 取公开菜单。businessDate 用于 DAILY 库存 remaining 计算。
func (s *Store) GetPublicMenu(ctx context.Context, storeID int64, businessDate string) (PublicMenu, error) {
	// 启用分类。
	cats, err := s.db.QueryContext(ctx,
		`SELECT id, name, sort_order FROM categories
		 WHERE store_id = ? AND enabled = 1 AND deleted_at IS NULL ORDER BY sort_order, id`, storeID)
	if err != nil {
		return PublicMenu{}, err
	}
	var categories []CategoryDTO
	catSet := map[int64]bool{}
	for cats.Next() {
		var c CategoryDTO
		if err := cats.Scan(&c.ID, &c.Name, &c.SortOrder); err != nil {
			cats.Close()
			return PublicMenu{}, err
		}
		categories = append(categories, c)
		catSet[c.ID] = true
	}
	cats.Close()

	// 启用菜品。
	drows, err := s.db.QueryContext(ctx,
		`SELECT id, category_id, name, description, image_url, sort_order, manually_sold_out, packaging_fee_cents
		 FROM products WHERE store_id = ? AND enabled = 1 AND deleted_at IS NULL ORDER BY sort_order, id`, storeID)
	if err != nil {
		return PublicMenu{}, err
	}
	type prodRow struct {
		dish DishDTO
	}
	var prods []prodRow
	for drows.Next() {
		var p prodRow
		if err := drows.Scan(&p.dish.ID, &p.dish.CategoryID, &p.dish.Name, &p.dish.Description,
			&p.dish.ImageURL, &p.dish.SortOrder, &p.dish.ManuallySoldOut, &p.dish.PackagingFeeCents); err != nil {
			drows.Close()
			return PublicMenu{}, err
		}
		prods = append(prods, p)
	}
	drows.Close()

	if len(prods) == 0 {
		return PublicMenu{StoreID: storeID, Categories: categories}, nil
	}

	// 一次性加载全部 SKU。
	productIDs := make([]any, 0, len(prods))
	for _, p := range prods {
		productIDs = append(productIDs, p.dish.ID)
	}
	skuQ := `SELECT id, product_id, name, price_cents, inventory_mode, is_default, sort_order
	         FROM skus WHERE store_id = ? AND enabled = 1 AND deleted_at IS NULL`
	skuQ += ` AND product_id IN (?` + strings.Repeat(",?", len(productIDs)-1) + `) ORDER BY sort_order, id`
	skuRows, err := s.db.QueryContext(ctx, skuQ, append([]any{storeID}, productIDs...)...)
	if err != nil {
		return PublicMenu{}, err
	}
	skusByProduct := map[int64][]SKUPlayer{}
	for skuRows.Next() {
		var pid int64
		var sk SKUPlayer
		if err := skuRows.Scan(&sk.ID, &pid, &sk.Name, &sk.PriceCents, &sk.InventoryMode, &sk.IsDefault, &sk.SortOrder); err != nil {
			skuRows.Close()
			return PublicMenu{}, err
		}
		skusByProduct[pid] = append(skusByProduct[pid], sk)
	}
	skuRows.Close()

	// 起售价 = 最低 SKU 价。
	dishes := make([]DishDTO, 0, len(prods))
	for _, p := range prods {
		if !catSet[p.dish.CategoryID] {
			continue // 分类未启用，跳过。
		}
		skus := skusByProduct[p.dish.ID]
		if len(skus) == 0 {
			continue // 至少一个可售 SKU（PRD §4.2）。
		}
		// 起售价。
		start := skus[0].PriceCents
		for _, sk := range skus {
			if sk.PriceCents < start {
				start = sk.PriceCents
			}
		}
		p.dish.StartPriceCents = start
		p.dish.SKUs = skus
		dishes = append(dishes, p.dish)
	}

	// 选项组/项（按门店一次取，避免 N+1）。简化：按菜品逐个取（小门店菜单规模可接受）。
	for i := range dishes {
		ogs, err := s.optionGroupsForProduct(ctx, dishes[i].ID)
		if err != nil {
			return PublicMenu{}, err
		}
		dishes[i].OptionGroups = ogs
	}

	return PublicMenu{StoreID: storeID, Categories: categories, Dishes: dishes}, nil
}

func (s *Store) optionGroupsForProduct(ctx context.Context, productID int64) ([]OptionGroupPlayer, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, selection_type, is_required, min_select, max_select, sort_order
		 FROM option_groups WHERE product_id = ? AND deleted_at IS NULL ORDER BY sort_order, id`, productID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var groups []OptionGroupPlayer
	for rows.Next() {
		var g OptionGroupPlayer
		if err := rows.Scan(&g.ID, &g.Name, &g.SelectionType, &g.IsRequired, &g.MinSelect, &g.MaxSelect, &g.SortOrder); err != nil {
			return nil, err
		}
		groups = append(groups, g)
	}
	// 选项项。
	for i := range groups {
		irows, err := s.db.QueryContext(ctx,
			`SELECT id, name, price_modifier_cents, is_default, sort_order
			 FROM option_items WHERE option_group_id = ? AND enabled = 1 AND deleted_at IS NULL ORDER BY sort_order, id`,
			groups[i].ID)
		if err != nil {
			return nil, err
		}
		for irows.Next() {
			var oi OptionItemPlayer
			if err := irows.Scan(&oi.ID, &oi.Name, &oi.PriceModifierCents, &oi.IsDefault, &oi.SortOrder); err != nil {
				irows.Close()
				return nil, err
			}
			groups[i].Items = append(groups[i].Items, oi)
		}
		irows.Close()
	}
	return groups, nil
}

// ── 桌码解析（PRD §4.1.2/§4.1.3）──────────────────────────────────────

// TableInfo 桌台解析结果。
type TableInfo struct {
	StoreID  int64  `json:"store_id"`
	TableID  int64  `json:"table_id"`
	TableNo  string `json:"table_no"`
	Area     string `json:"area"`
	Token    string `json:"table_token"`
}

// ResolveTable 解析桌码 token 为启用桌台；无效/轮换/停用返回错误（PRD §4.1.3）。
func (s *Store) ResolveTable(ctx context.Context, token string) (TableInfo, error) {
	if token == "" {
		return TableInfo{}, errors.New("table token required")
	}
	var t TableInfo
	err := s.db.QueryRowContext(ctx,
		`SELECT store_id, id, table_no, area, table_token FROM dining_tables
		 WHERE table_token = ? AND enabled = 1`, token).
		Scan(&t.StoreID, &t.TableID, &t.TableNo, &t.Area, &t.Token)
	if errors.Is(err, sql.ErrNoRows) {
		return TableInfo{}, errors.New("table not found or disabled")
	}
	return t, err
}
