// Package tables 实现桌台管理与换码（PRD §10.1）。
//
// 规则：
//   - 同门店桌号唯一；高熵不透明 token；scene 不含敏感数据。
//   - 换码生成新 token，原子替换，旧码立即失效。
//   - 小程序码存 COS 对象 key（数据库不存图片）。
package tables

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/dishflow/zshop/internal/security"
)

// Store 桌台持久化。
type Store struct {
	db *sql.DB
}

// NewStore 创建桌台存储。
func NewStore(db *sql.DB) *Store { return &Store{db: db} }

// Table 桌台。
type Table struct {
	ID            int64
	StoreID       int64
	TableNo       string
	Area          string
	Enabled       bool
	TableToken    string
	MinicodeKey   string
	Scene         string
}

// CreateTable 新建桌台（生成高熵 token，PRD §10.1）。
func (s *Store) CreateTable(ctx context.Context, storeID int64, tableNo, area string) (Table, error) {
	tableNo = strings.TrimSpace(tableNo)
	if tableNo == "" {
		return Table{}, errors.New("table_no required")
	}
	token, err := security.NewHexID(16)
	if err != nil {
		return Table{}, err
	}
	scene := "t" + token[:8] // 不含敏感数据（PRD §10.1）
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO dining_tables (store_id, table_no, area, enabled, table_token, scene) VALUES (?, ?, ?, 1, ?, ?)`,
		storeID, tableNo, area, token, scene)
	if err != nil {
		return Table{}, err
	}
	id, _ := res.LastInsertId()
	return Table{ID: id, StoreID: storeID, TableNo: tableNo, Area: area, Enabled: true, TableToken: token, Scene: scene}, nil
}

// List 列出门店桌台。
func (s *Store) List(ctx context.Context, storeID int64) ([]Table, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, store_id, table_no, area, enabled, table_token, minicode_object_key, scene FROM dining_tables WHERE store_id=? ORDER BY id`, storeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Table
	for rows.Next() {
		var t Table
		var enabled int
		if err := rows.Scan(&t.ID, &t.StoreID, &t.TableNo, &t.Area, &enabled, &t.TableToken, &t.MinicodeKey, &t.Scene); err != nil {
			return nil, err
		}
		t.Enabled = enabled == 1
		out = append(out, t)
	}
	return out, rows.Err()
}

// RotateToken 换码：生成新 token，原子替换，旧码失效（PRD §10.1）。
func (s *Store) RotateToken(ctx context.Context, storeID, id int64) (Table, error) {
	token, err := security.NewHexID(16)
	if err != nil {
		return Table{}, err
	}
	scene := "t" + token[:8]
	// 清空旧小程序码（换码后旧码失效，需重新生成）。
	res, err := s.db.ExecContext(ctx,
		`UPDATE dining_tables SET table_token=?, scene=?, minicode_object_key='' WHERE id=? AND store_id=?`,
		token, scene, id, storeID)
	if err != nil {
		return Table{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Table{}, sql.ErrNoRows
	}
	var t Table
	var enabled int
	err = s.db.QueryRowContext(ctx,
		`SELECT id, store_id, table_no, area, enabled, table_token, minicode_object_key, scene FROM dining_tables WHERE id=?`, id).
		Scan(&t.ID, &t.StoreID, &t.TableNo, &t.Area, &enabled, &t.TableToken, &t.MinicodeKey, &t.Scene)
	t.Enabled = enabled == 1
	return t, err
}

// Update 更新桌台启用/区域。
func (s *Store) Update(ctx context.Context, storeID, id int64, area *string, enabled *bool) error {
	sets := []string{}
	args := []any{}
	if area != nil {
		sets = append(sets, "area=?")
		args = append(args, *area)
	}
	if enabled != nil {
		sets = append(sets, "enabled=?")
		args = append(args, *enabled)
	}
	if len(sets) == 0 {
		return nil
	}
	args = append(args, id, storeID)
	res, err := s.db.ExecContext(ctx, "UPDATE dining_tables SET "+strings.Join(sets, ", ")+" WHERE id=? AND store_id=?", args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// SetMinicodeKey 设置生成的小程序码 COS 对象 key。
func (s *Store) SetMinicodeKey(ctx context.Context, storeID, id int64, key string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE dining_tables SET minicode_object_key=? WHERE id=? AND store_id=?`, key, id, storeID)
	return err
}
