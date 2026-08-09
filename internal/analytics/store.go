// Package analytics 实现经营分析（PRD §9）。
//
// 经营口径：以支付成功金额和退款成功金额为准（PRD §9）。
package analytics

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// Store 分析查询。
type Store struct {
	db *sql.DB
}

// NewStore 创建分析存储。
func NewStore(db *sql.DB) *Store { return &Store{db: db} }

// Overview 概览指标（PRD §9）。
type Overview struct {
	PayAmountCents   int64 `json:"pay_amount_cents"`
	RefundAmountCents int64 `json:"refund_amount_cents"`
	NetAmountCents   int64 `json:"net_amount_cents"`
	PayOrderCount    int64 `json:"pay_order_count"`
	RefundOrderCount int64 `json:"refund_order_count"`
	AvgOrderCents    int64 `json:"avg_order_cents"`
	CustomerCount    int64 `json:"customer_count"`
	NewCustomerCount int64 `json:"new_customer_count"`
}

// Overview 取区间概览（门店时区解释，PRD §9）。
func (s *Store) Overview(ctx context.Context, storeID int64, start, end time.Time) (Overview, error) {
	var o Overview
	// 支付金额/订单数（PAID 之后的状态视为已支付）。
	err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(paid_cents),0), COALESCE(COUNT(*),0)
		 FROM orders WHERE store_id=? AND fulfillment_state NOT IN ('PENDING_PAYMENT','CANCELLED') AND paid_at IS NOT NULL AND paid_at BETWEEN ? AND ?`,
		storeID, start, end).Scan(&o.PayAmountCents, &o.PayOrderCount)
	if err != nil {
		return Overview{}, err
	}
	// 退款成功金额/订单数。
	err = s.db.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(refunds.amount_cents),0), COALESCE(COUNT(*),0)
		 FROM refunds JOIN orders ON orders.id=refunds.order_id
		 WHERE refunds.store_id=? AND refunds.status='SUCCESS' AND refunds.succeeded_at IS NOT NULL AND refunds.succeeded_at BETWEEN ? AND ?`,
		storeID, start, end).Scan(&o.RefundAmountCents, &o.RefundOrderCount)
	if err != nil {
		return Overview{}, err
	}
	o.NetAmountCents = o.PayAmountCents - o.RefundAmountCents
	if o.PayOrderCount > 0 {
		o.AvgOrderCents = o.PayAmountCents / o.PayOrderCount
	}
	// 客户数（去重下单顾客）。
	s.db.QueryRowContext(ctx,
		`SELECT COUNT(DISTINCT customer_id) FROM orders WHERE store_id=? AND paid_at IS NOT NULL AND paid_at BETWEEN ? AND ?`,
		storeID, start, end).Scan(&o.CustomerCount)
	// 新客：该区间首次下单的顾客。
	s.db.QueryRowContext(ctx,
		`SELECT COUNT(DISTINCT customer_id) FROM orders WHERE store_id=? AND paid_at IS NOT NULL
		 AND paid_at BETWEEN ? AND ?
		 AND customer_id IN (
		   SELECT customer_id FROM (SELECT customer_id, MIN(paid_at) first_paid FROM orders WHERE store_id=? AND paid_at IS NOT NULL GROUP BY customer_id) t WHERE first_paid BETWEEN ? AND ?
		 )`,
		storeID, start, end, storeID, start, end).Scan(&o.NewCustomerCount)
	return o, nil
}

// Breakdown 分布：堂食/自取场景、热销商品数量与金额（PRD §9）。
type Breakdown struct {
	Scenarios []map[string]any `json:"scenarios"`
	TopItems  []map[string]any `json:"top_items"`
}

func (s *Store) Breakdown(ctx context.Context, storeID int64, start, end time.Time) (Breakdown, error) {
	var b Breakdown
	// 场景分布。
	rows, err := s.db.QueryContext(ctx,
		`SELECT scenario, COUNT(*), COALESCE(SUM(paid_cents),0) FROM orders
		 WHERE store_id=? AND paid_at IS NOT NULL AND paid_at BETWEEN ? AND ?
		 GROUP BY scenario`, storeID, start, end)
	if err != nil {
		return Breakdown{}, err
	}
	for rows.Next() {
		var sc string
		var cnt, amt int64
		if err := rows.Scan(&sc, &cnt, &amt); err != nil {
			rows.Close()
			return Breakdown{}, err
		}
		b.Scenarios = append(b.Scenarios, map[string]any{"scenario": sc, "count": cnt, "amount_cents": amt})
	}
	rows.Close()

	// 热销商品（订单项快照聚合）。
	rows, err = s.db.QueryContext(ctx,
		`SELECT product_name, SUM(quantity) qty, SUM(line_amount_cents) amt FROM order_items oi
		 JOIN orders o ON o.id=oi.order_id
		 WHERE oi.store_id=? AND o.paid_at IS NOT NULL AND o.paid_at BETWEEN ? AND ?
		 GROUP BY product_name ORDER BY qty DESC LIMIT 10`, storeID, start, end)
	if err != nil {
		return Breakdown{}, err
	}
	for rows.Next() {
		var name string
		var qty, amt int64
		if err := rows.Scan(&name, &qty, &amt); err != nil {
			rows.Close()
			return Breakdown{}, err
		}
		b.TopItems = append(b.TopItems, map[string]any{"product_name": name, "quantity": qty, "amount_cents": amt})
	}
	rows.Close()
	return b, rowsErr(err)
}

func rowsErr(err error) error { return err }

// Trends 趋势（按日；PRD §9）。
type TrendPoint struct {
	Date         string `json:"date"`
	PayAmount    int64  `json:"pay_amount_cents"`
	PayOrderCount int64 `json:"pay_order_count"`
}

func (s *Store) Trends(ctx context.Context, storeID int64, start, end time.Time, hourly bool) ([]TrendPoint, error) {
	grp := "DATE(o.paid_at)"
	if hourly {
		grp = "DATE_FORMAT(o.paid_at,'%Y-%m-%d %H:00')"
	}
	q := fmt.Sprintf(
		`SELECT %s AS bucket, COALESCE(SUM(o.paid_cents),0), COUNT(*) FROM orders o
		 WHERE o.store_id=? AND o.paid_at IS NOT NULL AND o.paid_at BETWEEN ? AND ?
		 GROUP BY bucket ORDER BY bucket`, grp)
	rows, err := s.db.QueryContext(ctx, q, storeID, start, end)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TrendPoint
	for rows.Next() {
		var p TrendPoint
		if err := rows.Scan(&p.Date, &p.PayAmount, &p.PayOrderCount); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// suppress
var _ = strings.TrimSpace
