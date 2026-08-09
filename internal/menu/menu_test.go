//go:build integration

// 菜单与库存集成测试：回收站批次恢复、SKU/选项校验、库存不变量与幂等。
// 运行：SHOP_DATABASE_DSN=... go test -tags=integration -race ./internal/menu/ ./internal/inventory/
package menu

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"

	"github.com/dishflow/zshop/internal/inventory"
)

var counter int64

func uniq() int64 {
	counter++
	return time.Now().UnixNano() + counter
}

func mustDB(t *testing.T) *sql.DB {
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

func seedStore(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	res, err := db.ExecContext(context.Background(), `INSERT INTO stores (name, timezone) VALUES (?, 'Asia/Shanghai')`, fmt.Sprintf("菜单测试店_%d", uniq()))
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id
}

func TestCategoryRecycleBatchRestore(t *testing.T) {
	db := mustDB(t)
	defer db.Close()
	ctx := context.Background()
	storeID := seedStore(t, db)
	s := NewStore(db)

	catID, err := s.CreateCategory(ctx, CreateCategoryInput{StoreID: storeID, Name: "汤品", Enabled: true, SortOrder: 1})
	if err != nil {
		t.Fatal(err)
	}
	// 建两个菜品。
	p1, err := s.CreateProduct(ctx, CreateProductInput{
		StoreID: storeID, CategoryID: catID, Name: "番茄汤", Enabled: true,
		SKUs: []SKUInput{{Name: "常规", PriceCents: 1800, InventoryMode: "UNLIMITED", Enabled: true, IsDefault: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	p2, err := s.CreateProduct(ctx, CreateProductInput{
		StoreID: storeID, CategoryID: catID, Name: "蘑菇汤", Enabled: true,
		SKUs: []SKUInput{{Name: "常规", PriceCents: 2000, InventoryMode: "UNLIMITED", Enabled: true, IsDefault: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = p2

	// 删除分类：分类和两个菜品应同批次进回收站。
	batch, err := s.DeleteCategory(ctx, storeID, catID)
	if err != nil {
		t.Fatalf("delete category: %v", err)
	}
	if batch == "" {
		t.Fatal("expected batch id")
	}

	// 未删除列表不应包含被删分类/菜品。
	cats, _ := s.ListCategories(ctx, storeID, false)
	for _, c := range cats {
		if c.ID == catID {
			t.Fatal("deleted category should not appear in active list")
		}
	}
	prods, _ := s.ListProducts(ctx, storeID, catID, false)
	if len(prods) != 0 {
		t.Fatalf("expected 0 active products, got %d", len(prods))
	}

	// 恢复分类应连带恢复同批次菜品。
	if err := s.RestoreCategory(ctx, storeID, catID); err != nil {
		t.Fatalf("restore: %v", err)
	}
	prods, _ = s.ListProducts(ctx, storeID, catID, false)
	if len(prods) != 2 {
		t.Fatalf("expected 2 restored products, got %d", len(prods))
	}
	// p1 应可见。
	found := false
	for _, p := range prods {
		if p.ID == p1 {
			found = true
		}
	}
	if !found {
		t.Fatal("restored product p1 not found")
	}
}

func TestRestoreProductBlockedWhenCategoryDeleted(t *testing.T) {
	db := mustDB(t)
	defer db.Close()
	ctx := context.Background()
	storeID := seedStore(t, db)
	s := NewStore(db)

	catID, _ := s.CreateCategory(ctx, CreateCategoryInput{StoreID: storeID, Name: "饮料", Enabled: true})
	pid, _ := s.CreateProduct(ctx, CreateProductInput{
		StoreID: storeID, CategoryID: catID, Name: "可乐", Enabled: true,
		SKUs: []SKUInput{{Name: "罐", PriceCents: 500, InventoryMode: "UNLIMITED", Enabled: true, IsDefault: true}},
	})
	// 删除分类（菜品同批进回收站）。
	s.DeleteCategory(ctx, storeID, catID)
	// 单独恢复菜品应被阻止（父分类已删除，PRD §7.2）。
	if err := s.RestoreProduct(ctx, storeID, pid); err == nil {
		t.Fatal("restoring product while category deleted should fail")
	}
}

func TestOptionGroupValidation_SingleDefault(t *testing.T) {
	db := mustDB(t)
	defer db.Close()
	ctx := context.Background()
	storeID := seedStore(t, db)
	s := NewStore(db)
	catID, _ := s.CreateCategory(ctx, CreateCategoryInput{StoreID: storeID, Name: "主食", Enabled: true})

	// 单选组两个默认项应失败。
	_, err := s.CreateProduct(ctx, CreateProductInput{
		StoreID: storeID, CategoryID: catID, Name: "套餐", Enabled: true,
		SKUs: []SKUInput{{Name: "S", PriceCents: 1000, InventoryMode: "UNLIMITED", Enabled: true, IsDefault: true}},
		OptionGroups: []OptionGroupInput{{
			Name: "辣度", SelectionType: "SINGLE", MinSelect: 1, MaxSelect: 1,
			Items: []OptionItemInput{
				{Name: "微辣", PriceModifierCents: 0, Enabled: true, IsDefault: true},
				{Name: "中辣", PriceModifierCents: 0, Enabled: true, IsDefault: true}, // 第二个默认 → 失败
			},
		}},
	})
	if err == nil {
		t.Fatal("expected error: single-select two defaults")
	}

	// 多选组默认项数 > max 应失败。
	_, err = s.CreateProduct(ctx, CreateProductInput{
		StoreID: storeID, CategoryID: catID, Name: "套餐2", Enabled: true,
		SKUs: []SKUInput{{Name: "S", PriceCents: 1000, InventoryMode: "UNLIMITED", Enabled: true, IsDefault: true}},
		OptionGroups: []OptionGroupInput{{
			Name: "加料", SelectionType: "MULTI", MinSelect: 0, MaxSelect: 2,
			Items: []OptionItemInput{
				{Name: "蛋", PriceModifierCents: 100, Enabled: true, IsDefault: true},
				{Name: "肉", PriceModifierCents: 200, Enabled: true, IsDefault: true},
				{Name: "菜", PriceModifierCents: 100, Enabled: true, IsDefault: true}, // 3 > 2
			},
		}},
	})
	if err == nil {
		t.Fatal("expected error: multi-select defaults exceed max")
	}

	// 禁用默认项应失败。
	_, err = s.CreateProduct(ctx, CreateProductInput{
		StoreID: storeID, CategoryID: catID, Name: "套餐3", Enabled: true,
		SKUs: []SKUInput{{Name: "S", PriceCents: 1000, InventoryMode: "UNLIMITED", Enabled: true, IsDefault: true}},
		OptionGroups: []OptionGroupInput{{
			Name: "辣度", SelectionType: "SINGLE", MinSelect: 1, MaxSelect: 1,
			Items: []OptionItemInput{
				{Name: "微辣", PriceModifierCents: 0, Enabled: false, IsDefault: true}, // 默认但禁用
			},
		}},
	})
	if err == nil {
		t.Fatal("expected error: default must be enabled")
	}

	// 合法配置应成功。
	pid, err := s.CreateProduct(ctx, CreateProductInput{
		StoreID: storeID, CategoryID: catID, Name: "套餐4", Enabled: true,
		SKUs: []SKUInput{{Name: "S", PriceCents: 1000, InventoryMode: "UNLIMITED", Enabled: true, IsDefault: true}},
		OptionGroups: []OptionGroupInput{{
			Name: "辣度", SelectionType: "SINGLE", MinSelect: 1, MaxSelect: 1,
			Items: []OptionItemInput{
				{Name: "微辣", PriceModifierCents: 0, Enabled: true, IsDefault: true},
				{Name: "中辣", PriceModifierCents: 0, Enabled: true, IsDefault: false},
			},
		}},
	})
	if err != nil {
		t.Fatalf("valid config failed: %v", err)
	}
	// 验证详情可读出选项项。
	_, _, ogs, items, err := s.GetProductDetail(ctx, storeID, pid)
	if err != nil {
		t.Fatal(err)
	}
	if len(ogs) != 1 || len(items[ogs[0].ID]) != 2 {
		t.Fatalf("option groups/items wrong: %+v %v", ogs, items)
	}
}

func TestInventoryAdjustInvariantAndIdempotency(t *testing.T) {
	db := mustDB(t)
	defer db.Close()
	ctx := context.Background()
	storeID := seedStore(t, db)
	ms := NewStore(db)
	catID, _ := ms.CreateCategory(ctx, CreateCategoryInput{StoreID: storeID, Name: "限购", Enabled: true})
	pid, _ := ms.CreateProduct(ctx, CreateProductInput{
		StoreID: storeID, CategoryID: catID, Name: "限量菜", Enabled: true,
		SKUs: []SKUInput{{Name: "份", PriceCents: 1000, InventoryMode: "DAILY", DailyStock: 5, Enabled: true, IsDefault: true}},
	})
	// 找到 sku id。
	_, skus, _, _, _ := ms.GetProductDetail(ctx, storeID, pid)
	if len(skus) != 1 {
		t.Fatal("expected 1 sku")
	}
	skuID := skus[0].ID

	inv := inventory.NewStore(db)
	date := "2026-08-01"
	// 初始化 daily_inventory（EnsureDaily）。
	if err := inv.EnsureDaily(ctx, storeID, skuID, date, 5); err != nil {
		t.Fatal(err)
	}

	// 正向调整 +3。
	if err := inv.Adjust(ctx, inventory.AdjustInput{
		StoreID: storeID, SKUID: skuID, BusinessDate: date, Delta: 3, Reason: "进货", IdempotencyKey: "k1",
	}); err != nil {
		t.Fatalf("adjust +3: %v", err)
	}
	di, _ := inv.GetDaily(ctx, storeID, skuID, date)
	if di.TargetQty != 8 {
		t.Fatalf("target = %d, want 8", di.TargetQty)
	}

	// 重复幂等键应失败。
	err := inv.Adjust(ctx, inventory.AdjustInput{
		StoreID: storeID, SKUID: skuID, BusinessDate: date, Delta: 1, Reason: "进货", IdempotencyKey: "k1",
	})
	if err == nil {
		t.Fatal("duplicate idempotency key should fail")
	}

	// 模拟已售约束：直接 update sold，然后尝试把 target 调到低于 reserved+sold。
	db.ExecContext(ctx, `UPDATE daily_inventory SET sold_qty = 8 WHERE store_id=? AND sku_id=? AND business_date=?`, storeID, skuID, date)
	err = inv.Adjust(ctx, inventory.AdjustInput{
		StoreID: storeID, SKUID: skuID, BusinessDate: date, Delta: -5, Reason: "减库存", IdempotencyKey: "k2",
	})
	if err == nil {
		t.Fatal("adjustment violating invariant should fail")
	}

	// delta=0 / reason 空 / 缺幂等键应失败。
	if err := inv.Adjust(ctx, inventory.AdjustInput{StoreID: storeID, SKUID: skuID, BusinessDate: date, Delta: 0, Reason: "x", IdempotencyKey: "k3"}); err == nil {
		t.Fatal("delta=0 should fail")
	}
	if err := inv.Adjust(ctx, inventory.AdjustInput{StoreID: storeID, SKUID: skuID, BusinessDate: date, Delta: 1, Reason: "", IdempotencyKey: "k4"}); err == nil {
		t.Fatal("empty reason should fail")
	}
	if err := inv.Adjust(ctx, inventory.AdjustInput{StoreID: storeID, SKUID: skuID, BusinessDate: date, Delta: 1, Reason: "x", IdempotencyKey: ""}); err == nil {
		t.Fatal("missing idempotency key should fail")
	}

	// 流水应记录调整。
	movs, err := inv.ListMovements(ctx, storeID, skuID, date, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	hasAdjust := false
	for _, m := range movs {
		if m["movement_type"] == "ADJUST" {
			hasAdjust = true
		}
	}
	if !hasAdjust {
		t.Fatal("expected ADJUST movement in ledger")
	}
}
