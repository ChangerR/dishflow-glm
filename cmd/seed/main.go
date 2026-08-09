// Package main 是一次性 seed 脚本：创建演示用的平台超管账号、门店、成员、菜单与测试订单。
// 用法：SHOP_DATABASE_DSN=... SHOP_DEV_MODE=true go run ./cmd/seed
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/dishflow/zshop/internal/config"
	"github.com/dishflow/zshop/internal/security"
	"github.com/jmoiron/sqlx"
	_ "github.com/go-sql-driver/mysql"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	dbx, err := sqlx.Connect("mysql", cfg.DatabaseDSN)
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer dbx.Close()
	ctx := context.Background()

	// 1. 平台超管账号 demo / demo123456
	hash, _ := security.HashPassword("demo123456")
	_, err = dbx.ExecContext(ctx,
		`INSERT INTO admin_users (id, login, display_name, password_hash, enabled, is_platform_admin)
		 VALUES (1,'demo','演示超管',?,1,1) ON DUPLICATE KEY UPDATE password_hash=VALUES(password_hash)`, hash)
	if err != nil {
		log.Fatalf("seed admin: %v", err)
	}

	// 2. 一个店长账号 manager / manager123456
	hash2, _ := security.HashPassword("manager123456")
	_, err = dbx.ExecContext(ctx,
		`INSERT INTO admin_users (id, login, display_name, password_hash, enabled, is_platform_admin)
		 VALUES (2,'manager','演示店长',?,1,0) ON DUPLICATE KEY UPDATE password_hash=VALUES(password_hash)`, hash2)
	if err != nil {
		log.Fatalf("seed manager: %v", err)
	}

	// 3. 一个店员账号 staff / staff123456
	hash3, _ := security.HashPassword("staff123456")
	_, err = dbx.ExecContext(ctx,
		`INSERT INTO admin_users (id, login, display_name, password_hash, enabled, is_platform_admin)
		 VALUES (3,'staff','演示店员',?,1,0) ON DUPLICATE KEY UPDATE password_hash=VALUES(password_hash)`, hash3)
	if err != nil {
		log.Fatalf("seed staff: %v", err)
	}

	// 4. 门店（含小程序配置，X-Wechat-Appid 解析用）。
	_, err = dbx.ExecContext(ctx,
		`INSERT INTO stores (id, name, enabled, business_open, phone, business_hours, announcement, timezone, business_hours_correct)
		 VALUES (1,'DishFlow 演示店',1,1,'13800138000','09:00-22:00','欢迎光临 DishFlow 演示店！','Asia/Shanghai', NULL)
		 ON DUPLICATE KEY UPDATE name=VALUES(name)`)
	if err != nil {
		// stores 表可能没有 business_hours_correct 列，重试不带它。
		_, err = dbx.ExecContext(ctx,
			`INSERT INTO stores (id, name, enabled, business_open, phone, business_hours, announcement, timezone)
			 VALUES (1,'DishFlow 演示店',1,1,'13800138000','09:00-22:00','欢迎光临 DishFlow 演示店！','Asia/Shanghai')
			 ON DUPLICATE KEY UPDATE name=VALUES(name)`)
		if err != nil {
			log.Fatalf("seed store: %v", err)
		}
	}

	// 小程序配置（门店定位用 AppID）。
	_, err = dbx.ExecContext(ctx,
		`INSERT INTO miniprogram_config (store_id, wechat_appid, brand_name, theme_color, logo_url)
		 VALUES (1,'wxdemoappid0001','DishFlow','#1677ff','')
		 ON DUPLICATE KEY UPDATE brand_name=VALUES(brand_name)`)
	if err != nil {
		log.Fatalf("seed miniprogram: %v", err)
	}

	// 5. 店长/店员成员关系。
	_, _ = dbx.ExecContext(ctx, `INSERT IGNORE INTO shop_members (store_id, admin_user_id, role) VALUES (1,2,'OWNER')`)
	_, _ = dbx.ExecContext(ctx, `INSERT IGNORE INTO shop_members (store_id, admin_user_id, role) VALUES (1,3,'STAFF')`)

	// 6. 菜单：2 个分类、4 个菜品、SKU、选项组。
	_, err = dbx.ExecContext(ctx, `
INSERT IGNORE INTO categories (id, store_id, name, enabled, sort_order) VALUES
  (1,1,'招牌主食',1,1),
  (2,1,'饮品',1,2)`)
	if err != nil {
		log.Fatalf("seed categories: %v", err)
	}

	_, err = dbx.ExecContext(ctx, `
INSERT IGNORE INTO products (id, store_id, category_id, name, description, enabled, sort_order, packaging_fee_cents) VALUES
  (1,1,1,'招牌牛肉饭','秘制酱汁慢炖牛肉配米饭',1,1,150),
  (2,1,1,'香辣鸡腿堡饭','香辣鸡腿排配米饭',1,2,150),
  (3,1,2,'冰美式','现磨咖啡豆萃取',1,1,100),
  (4,1,2,'柠檬茶','新鲜柠檬现泡',1,2,100)`)
	if err != nil {
		log.Fatalf("seed products: %v", err)
	}

	_, err = dbx.ExecContext(ctx, `
INSERT IGNORE INTO skus (id, store_id, product_id, name, price_cents, inventory_mode, daily_stock, enabled, is_default, sort_order) VALUES
  (1,1,1,'标准份',2800,'DAILY',20,1,1,1),
  (2,1,1,'加大份',3200,'DAILY',10,1,0,2),
  (3,1,2,'标准辣',2500,'DAILY',15,1,1,1),
  (4,1,3,'中杯',1500,'UNLIMITED',0,1,1,1),
  (5,1,4,'中杯',1200,'UNLIMITED',0,1,1,1)`)
	if err != nil {
		log.Fatalf("seed skus: %v", err)
	}

	// 牛肉饭的加料选项组。
	_, err = dbx.ExecContext(ctx, `
INSERT IGNORE INTO option_groups (id, store_id, product_id, name, selection_type, is_required, min_select, max_select, sort_order) VALUES
  (1,1,1,'加料','MULTI',0,0,3,1),
  (2,1,1,'辣度','SINGLE',1,1,1,2)`)
	if err != nil {
		log.Fatalf("seed option_groups: %v", err)
	}

	_, err = dbx.ExecContext(ctx, `
INSERT IGNORE INTO option_items (id, store_id, option_group_id, name, price_modifier_cents, enabled, is_default, sort_order) VALUES
  (1,1,1,'加蛋',300,1,1,1),
  (2,1,1,'加牛肉',800,1,0,2),
  (3,1,1,'加青菜',200,1,0,3),
  (4,1,2,'微辣',0,1,1,1),
  (5,1,2,'中辣',0,1,0,2),
  (6,1,2,'特辣',0,1,0,3)`)
	if err != nil {
		log.Fatalf("seed option_items: %v", err)
	}

	// 7. 桌台（堂食扫码演示）。
	tok := security.HashTokenStr("demo-table-A01")
	_, _ = dbx.ExecContext(ctx,
		`INSERT IGNORE INTO dining_tables (id, store_id, table_no, area, enabled, table_token, scene) VALUES (1,1,'A01','大厅',1,?,'A01')`, tok)

	// 8. 演示订单（工作台直接可见）。
	seedDemoOrders(ctx, dbx)

	fmt.Println("✅ 种子数据已创建")
	fmt.Println("   平台超管: demo / demo123456")
	fmt.Println("   店长:     manager / manager123456")
	fmt.Println("   店员:     staff / staff123456")
}

func seedDemoOrders(ctx context.Context, dbx *sqlx.DB) {
	// 顾客（匿名 openid）。
	_, _ = dbx.ExecContext(ctx, `INSERT IGNORE INTO customers (id, store_id, wechat_openid) VALUES (1,1,'demo_customer_1'),(2,1,'demo_customer_2')`)
	now := time.Now().UTC()
	exp := now.Add(10 * time.Minute)
	biz := "2026-08-02"

	// 订单 1：PAID（待接单）。
	_, _ = dbx.ExecContext(ctx,
		`INSERT IGNORE INTO orders (id, store_id, customer_id, order_no, pickup_business_date, scenario, pickup_type,
		   item_amount_cents, packaging_fee_cents, discount_cents, payable_cents, paid_cents,
		   fulfillment_state, version, quote_token, quote_expires_at, expires_at, paid_at, created_at)
		 VALUES (1,1,1,'ON0001',?,'PICKUP','IMMEDIATE',2800,150,0,2950,2950,'PAID',1,'qt_demo1',?,?,?,?)`,
		biz, exp, exp, now)

	// 订单 2：ACCEPTED。
	_, _ = dbx.ExecContext(ctx,
		`INSERT IGNORE INTO orders (id, store_id, customer_id, order_no, pickup_business_date, scenario, pickup_type,
		   item_amount_cents, packaging_fee_cents, discount_cents, payable_cents, paid_cents,
		   fulfillment_state, version, quote_token, quote_expires_at, expires_at, paid_at, created_at)
		 VALUES (2,1,2,'ON0002',?,'DINE_IN','IMMEDIATE',5300,0,0,5300,5300,'ACCEPTED',1,'qt_demo2',?,?,?,?)`,
		biz, exp, exp, now)

	// 订单项快照。
	_, _ = dbx.ExecContext(ctx,
		`INSERT IGNORE INTO order_items (id, order_id, store_id, sku_id, product_id, sku_name, product_name, unit_price_cents, quantity, options_snapshot, packaging_fee_cents, line_amount_cents) VALUES
		  (1,1,1,1,1,'标准份','招牌牛肉饭',3100,1,'[]',150,3250),
		  (2,2,1,3,2,'标准辣','香辣鸡腿堡饭',2500,2,'[]',0,5000),
		  (3,2,1,4,3,'中杯','冰美式',1500,2,'[]',0,3000)`)

	// 事件。
	_, _ = dbx.ExecContext(ctx,
		`INSERT IGNORE INTO order_events (id, order_id, store_id, event_type, to_state, actor_type, summary) VALUES
		  (1,1,1,'order.created','PENDING_PAYMENT','SYSTEM','订单创建'),
		  (2,1,1,'order.paid','PAID','SYSTEM','支付成功'),
		  (3,2,1,'order.created','PENDING_PAYMENT','SYSTEM','订单创建'),
		  (4,2,1,'order.paid','PAID','SYSTEM','支付成功'),
		  (5,2,1,'state.ACCEPTED','ACCEPTED','STAFF','PAID → ACCEPTED')`)

	// 支付记录。
	_, _ = dbx.ExecContext(ctx,
		`INSERT IGNORE INTO payments (id, store_id, order_id, amount_cents, status, mock_payment, transaction_id) VALUES
		  (1,1,1,2950,'SUCCESS',1,'mock_tx_1'),
		  (2,1,2,5300,'SUCCESS',1,'mock_tx_2')`)

	// 每日库存（业务日 2026-08-02）。
	_, _ = dbx.ExecContext(ctx,
		`INSERT IGNORE INTO daily_inventory (store_id, sku_id, business_date, target_qty, reserved_qty, sold_qty) VALUES
		  (1,1,'2026-08-02',20,0,1),
		  (1,3,'2026-08-02',15,0,2),
		  (1,4,'2026-08-02',0,0,2)`)
}
