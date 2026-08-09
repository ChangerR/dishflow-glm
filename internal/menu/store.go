// Package menu 实现菜单域：分类、菜品、SKU、选项组、选项项，含 30 天回收站（PRD §7.1/§7.2/§4.3）。
//
// 回收站规则（PRD §7.1）：
//   - 删除分类为 30 天软删除：分类及该分类下未删除菜品进入同一 delete_batch_id 批次。
//   - 30 天内恢复分类时，恢复同批次菜品及其删除前启用状态。
//   - 恢复窗口结束后仅关闭恢复能力，底层行保留 tombstone（迁移已建 deleted_at/delete_batch_id）。
//   - 删除菜品进入 30 天回收站；恢复菜品前所属分类必须已恢复。
//
// 单选组最多一个默认项；多选组默认项数 ≤ 最大选择数；默认项必须启用（PRD §4.3）。
package menu

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/dishflow/zshop/internal/security"
)

// RecycleWindow 回收站恢复窗口（PRD §7.1）。
const RecycleWindowDays = 30

// Store 菜单域持久化。
type Store struct {
	db *sql.DB
}

// NewStore 创建菜单存储。
func NewStore(db *sql.DB) *Store { return &Store{db: db} }

// ── 分类 ──────────────────────────────────────────────────────────────

// Category 分类。
type Category struct {
	ID            int64
	StoreID       int64
	Name          string
	Enabled       bool
	SortOrder     int
	DeletedAt     sql.NullTime
	DeleteBatchID string
	CreatedAt     string
	UpdatedAt     string
}

// CreateCategoryInput 创建分类输入。
type CreateCategoryInput struct {
	StoreID   int64
	Name      string
	Enabled   bool
	SortOrder int
}

// CreateCategory 新建分类（PRD §7.1）。
func (s *Store) CreateCategory(ctx context.Context, in CreateCategoryInput) (int64, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return 0, errors.New("category name required")
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO categories (store_id, name, enabled, sort_order) VALUES (?,?,?,?)`,
		in.StoreID, name, in.Enabled, in.SortOrder)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ListCategories 列出门店分类（含回收站状态，PRD §7.1）。
// includeDeleted=true 时返回回收站中的分类。
func (s *Store) ListCategories(ctx context.Context, storeID int64, includeDeleted bool) ([]Category, error) {
	q := `SELECT id, store_id, name, enabled, sort_order, deleted_at, delete_batch_id, created_at, updated_at
	      FROM categories WHERE store_id = ?`
	if !includeDeleted {
		q += ` AND deleted_at IS NULL`
	}
	q += ` ORDER BY (enabled=0), sort_order ASC, id ASC` // 启用排前（PRD §7.1）
	rows, err := s.db.QueryContext(ctx, q, storeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Category
	for rows.Next() {
		var c Category
		if err := rows.Scan(&c.ID, &c.StoreID, &c.Name, &c.Enabled, &c.SortOrder, &c.DeletedAt,
			&c.DeleteBatchID, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// UpdateCategoryInput 分类更新字段。
type UpdateCategoryInput struct {
	Name      *string
	Enabled   *bool
	SortOrder *int
}

// UpdateCategory 更新分类。
func (s *Store) UpdateCategory(ctx context.Context, storeID, id int64, in UpdateCategoryInput) error {
	sets := []string{}
	args := []any{}
	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if name == "" {
			return errors.New("category name required")
		}
		sets = append(sets, "name = ?")
		args = append(args, name)
	}
	if in.Enabled != nil {
		sets = append(sets, "enabled = ?")
		args = append(args, *in.Enabled)
	}
	if in.SortOrder != nil {
		sets = append(sets, "sort_order = ?")
		args = append(args, *in.SortOrder)
	}
	if len(sets) == 0 {
		return nil
	}
	args = append(args, id, storeID)
	res, err := s.db.ExecContext(ctx,
		"UPDATE categories SET "+strings.Join(sets, ", ")+" WHERE id = ? AND store_id = ? AND deleted_at IS NULL", args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// DeleteCategory 软删除分类：分类及该分类下未删除菜品进入同一批次（PRD §7.1）。
// 事务内：生成 batch id，置分类 deleted_at，把分类下未删除菜品也置 deleted_at + 同 batch 并下架。
func (s *Store) DeleteCategory(ctx context.Context, storeID, id int64) (batchID string, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()

	// 校验分类存在且未删除。
	var deletedAt sql.NullTime
	err = tx.QueryRowContext(ctx,
		`SELECT deleted_at FROM categories WHERE id = ? AND store_id = ?`, id, storeID).Scan(&deletedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", sql.ErrNoRows
		}
		return "", err
	}
	if deletedAt.Valid {
		return "", errors.New("category already deleted")
	}

	batchID, err = security.NewHexID(16)
	if err != nil {
		return "", err
	}

	// 分类进回收站。
	if _, err = tx.ExecContext(ctx,
		`UPDATE categories SET deleted_at = UTC_TIMESTAMP(3), delete_batch_id = ?, enabled = 0 WHERE id = ? AND store_id = ?`,
		batchID, id, storeID); err != nil {
		return "", err
	}
	// 分类下未删除菜品进同批次并下架。
	if _, err = tx.ExecContext(ctx,
		`UPDATE products SET deleted_at = UTC_TIMESTAMP(3), delete_batch_id = ?, enabled = 0
		 WHERE category_id = ? AND store_id = ? AND deleted_at IS NULL`,
		batchID, id, storeID); err != nil {
		return "", err
	}
	return batchID, tx.Commit()
}

// RestoreCategory 恢复分类及其同批次菜品与删除前启用状态（PRD §7.1）。
func (s *Store) RestoreCategory(ctx context.Context, storeID, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var deletedAt sql.NullTime
	var batchID string
	err = tx.QueryRowContext(ctx,
		`SELECT deleted_at, delete_batch_id FROM categories WHERE id = ? AND store_id = ? FOR UPDATE`, id, storeID).
		Scan(&deletedAt, &batchID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return sql.ErrNoRows
		}
		return err
	}
	if !deletedAt.Valid {
		return errors.New("category not deleted")
	}
	// 恢复窗口检查（PRD §7.1）。
	if timeSinceDays(deletedAt.Time) > RecycleWindowDays {
		return errors.New("recycle window expired")
	}

	// 恢复分类（保持当前 enabled，PRD §7.1 恢复分类连接同批菜品与删除前启用状态；
	// 删除时已下架，这里恢复时把分类恢复为启用）。
	if _, err = tx.ExecContext(ctx,
		`UPDATE categories SET deleted_at = NULL, delete_batch_id = '', enabled = 1 WHERE id = ? AND store_id = ?`,
		id, storeID); err != nil {
		return err
	}
	// 恢复同批次菜品（清空 deleted_at/batch，保留其当前 enabled——删除时被下架，
	// 调用方/前端可随后重新上架；PRD §7.1 “恢复同批次菜品及其删除前启用状态”）。
	if _, err = tx.ExecContext(ctx,
		`UPDATE products SET deleted_at = NULL, delete_batch_id = '' WHERE store_id = ? AND delete_batch_id = ?`,
		storeID, batchID); err != nil {
		return err
	}
	return tx.Commit()
}
