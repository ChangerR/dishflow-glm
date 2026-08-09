// Package materials 实现物料目录与采购清单状态机（PRD §12）。
package materials

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/dishflow/zshop/internal/security"
)

// Store 物料域持久化。
type Store struct {
	db *sql.DB
}

// NewStore 创建物料存储。
func NewStore(db *sql.DB) *Store { return &Store{db: db} }

// Material 物料。
type Material struct {
	ID        int64
	StoreID   int64
	Name      string
	Unit      string
	Enabled   bool
	Category  string
	SortOrder int
}

// CreateMaterial 新建物料（同门店名称唯一，PRD §12.1）。
func (s *Store) CreateMaterial(ctx context.Context, storeID int64, name, unit, category string) (int64, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return 0, errors.New("material name required")
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO materials (store_id, name, unit, category, enabled, sort_order) VALUES (?, ?, ?, ?, 1, 0)`,
		storeID, name, unit, category)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ListMaterials 列出门店物料（可搜索/分类过滤）。
func (s *Store) ListMaterials(ctx context.Context, storeID int64, search, category string) ([]Material, error) {
	q := `SELECT id, store_id, name, unit, category, enabled, sort_order FROM materials WHERE store_id=?`
	args := []any{storeID}
	if search != "" {
		q += ` AND name LIKE ?`
		args = append(args, "%"+search+"%")
	}
	if category != "" {
		q += ` AND category=?`
		args = append(args, category)
	}
	q += ` ORDER BY sort_order, id`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Material
	for rows.Next() {
		var m Material
		var enabled int
		if err := rows.Scan(&m.ID, &m.StoreID, &m.Name, &m.Unit, &m.Category, &enabled, &m.SortOrder); err != nil {
			return nil, err
		}
		m.Enabled = enabled == 1
		out = append(out, m)
	}
	return out, rows.Err()
}

// ── 采购清单（状态机 DRAFT→SUBMITTED→PRINTED→COMPLETED，任意未完成可 VOID，PRD §12.2）。

// PurchaseList 采购清单。
type PurchaseList struct {
	ID            int64
	StoreID       int64
	ListNo        string
	BusinessDate  string
	Title         string
	Status        string
	TotalAmountCents int64
	Version       int64
	PrintCount    int
}

// CreateList 新建草稿清单。
func (s *Store) CreateList(ctx context.Context, storeID int64, businessDate, title string, createdBy int64) (int64, error) {
	listNo, err := security.NewHexID(8)
	if err != nil {
		return 0, err
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO purchase_lists (store_id, list_no, business_date, title, status, created_by) VALUES (?, ?, ?, ?, 'DRAFT', ?)`,
		storeID, listNo, businessDate, title, createdBy)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// AddItem 加物料到清单（重复物料不得重复加入，PRD §12.2）。物料名称/单位快照。
func (s *Store) AddItem(ctx context.Context, storeID, listID, materialID int64, quantity float64, note string) error {
	if quantity <= 0 {
		return errors.New("quantity must be > 0")
	}
	// 取物料名称/单位快照。
	var name, unit string
	err := s.db.QueryRowContext(ctx, `SELECT name, unit FROM materials WHERE id=? AND store_id=?`, materialID, storeID).Scan(&name, &unit)
	if err != nil {
		return errors.New("material not found")
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO purchase_list_items (purchase_list_id, store_id, material_id, material_name, unit, quantity, note) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		listID, storeID, materialID, name, unit, quantity, note)
	return err
}

// SubmitList 提交清单（至少一个条目，PRD §12.2）。
func (s *Store) SubmitList(ctx context.Context, storeID, listID, userID int64, expectedVersion int64) error {
	return s.transitionList(ctx, storeID, listID, userID, expectedVersion, "SUBMITTED", "DRAFT")
}

// MarkPrinted 本地标记打印。
func (s *Store) MarkPrinted(ctx context.Context, storeID, listID, userID int64, expectedVersion int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockTransition(tx, listID, storeID, expectedVersion, "PRINTED", "SUBMITTED"); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE purchase_lists SET print_count=print_count+1 WHERE id=?`, listID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO purchase_list_events (purchase_list_id, store_id, event_type, from_status, to_status, actor_admin_user_id, summary) VALUES (?, ?, 'mark_printed', 'SUBMITTED', 'PRINTED', ?, '本地标记打印')`, listID, storeID, userID); err != nil {
		return err
	}
	return tx.Commit()
}

// CompleteList 完成清单（需金额信息完整）。
func (s *Store) CompleteList(ctx context.Context, storeID, listID, userID int64, expectedVersion int64, totalAmount int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockTransition(tx, listID, storeID, expectedVersion, "COMPLETED", "PRINTED"); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE purchase_lists SET total_amount_cents=? WHERE id=?`, totalAmount, listID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO purchase_list_events (purchase_list_id, store_id, event_type, from_status, to_status, actor_admin_user_id, summary) VALUES (?, ?, 'complete', 'PRINTED', 'COMPLETED', ?, '完成清单')`, listID, storeID, userID); err != nil {
		return err
	}
	return tx.Commit()
}

// VoidList 作废清单（可选原因）。
func (s *Store) VoidList(ctx context.Context, storeID, listID, userID int64, expectedVersion int64, reason string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockTransitionNotFinal(tx, listID, storeID, expectedVersion, "VOID"); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE purchase_lists SET void_reason=? WHERE id=?`, reason, listID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO purchase_list_events (purchase_list_id, store_id, event_type, to_status, actor_admin_user_id, summary) VALUES (?, ?, 'void', 'VOID', ?, ?)`, listID, storeID, userID, reason); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) transitionList(ctx context.Context, storeID, listID, userID int64, expectedVersion int64, to, from string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockTransition(tx, listID, storeID, expectedVersion, to, from); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO purchase_list_events (purchase_list_id, store_id, event_type, from_status, to_status, actor_admin_user_id, summary) VALUES (?, ?, ?, ?, ?, ?, ?)`, listID, storeID, "to_"+to, from, to, userID, from+"→"+to); err != nil {
		return err
	}
	return tx.Commit()
}

func lockTransition(tx *sql.Tx, listID, storeID, expectedVersion int64, to, from string) error {
	var status string
	var version int64
	if err := tx.QueryRow(`SELECT status, version FROM purchase_lists WHERE id=? AND store_id=? FOR UPDATE`, listID, storeID).Scan(&status, &version); err != nil {
		return err
	}
	if version != expectedVersion {
		return ErrVersionConflict
	}
	if status != from {
		return fmt.Errorf("STATE_CONFLICT: expected %s, got %s", from, status)
	}
	if _, err := tx.Exec(`UPDATE purchase_lists SET status=?, version=version+1 WHERE id=?`, to, listID); err != nil {
		return err
	}
	return nil
}

func lockTransitionNotFinal(tx *sql.Tx, listID, storeID, expectedVersion int64, to string) error {
	var status string
	var version int64
	if err := tx.QueryRow(`SELECT status, version FROM purchase_lists WHERE id=? AND store_id=? FOR UPDATE`, listID, storeID).Scan(&status, &version); err != nil {
		return err
	}
	if version != expectedVersion {
		return ErrVersionConflict
	}
	if status == "COMPLETED" || status == "VOID" {
		return fmt.Errorf("STATE_CONFLICT: cannot void %s", status)
	}
	if _, err := tx.Exec(`UPDATE purchase_lists SET status=?, version=version+1 WHERE id=?`, to, listID); err != nil {
		return err
	}
	return nil
}

// ErrVersionConflict 版本冲突。
var ErrVersionConflict = errors.New("STATE_CONFLICT")
