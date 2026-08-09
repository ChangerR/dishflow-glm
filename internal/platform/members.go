package platform

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/dishflow/zshop/internal/authn"
)

// CountMembershipsForUser 统计账号已归属的门店数（普通账号最多 1，PRD §2.2）。
func (s *Store) CountMembershipsForUser(ctx context.Context, adminUserID int64) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM shop_members WHERE admin_user_id = ?`, adminUserID).Scan(&n)
	return n, err
}

// AddMember 把账号加入门店为某角色（PRD §10.4）。
// 若角色为 OWNER：同门店其它 OWNER 自动降为 MANAGER，保持唯一店主（PRD §3.4/§10.4）。
func (s *Store) AddMember(ctx context.Context, storeID, adminUserID int64, role authn.Role) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if role == authn.RoleOwner {
		// 唯一店主：其它 OWNER 降为 MANAGER。
		if _, err := tx.ExecContext(ctx,
			`UPDATE shop_members SET role = 'MANAGER' WHERE store_id = ? AND role = 'OWNER'`, storeID); err != nil {
			return fmt.Errorf("demote existing owners: %w", err)
		}
	}
	res, err := tx.ExecContext(ctx,
		`INSERT INTO shop_members (store_id, admin_user_id, role) VALUES (?,?,?)`,
		storeID, adminUserID, string(role))
	if err != nil {
		return fmt.Errorf("insert member: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errors.New("member not added")
	}
	return tx.Commit()
}

// ChangeMemberRole 变更成员角色（PRD §10.4）。
// 升为 OWNER 时降其它 OWNER；移除当前店主前应先转移所有权（调用方校验）。
func (s *Store) ChangeMemberRole(ctx context.Context, storeID, adminUserID int64, role authn.Role) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if role == authn.RoleOwner {
		if _, err := tx.ExecContext(ctx,
			`UPDATE shop_members SET role = 'MANAGER' WHERE store_id = ? AND role = 'OWNER'`, storeID); err != nil {
			return err
		}
	}
	res, err := tx.ExecContext(ctx,
		`UPDATE shop_members SET role = ? WHERE store_id = ? AND admin_user_id = ?`,
		string(role), storeID, adminUserID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return tx.Commit()
}

// RemoveMember 移除成员（PRD §10.4）。移除当前店主前应由调用方先完成所有权转移。
func (s *Store) RemoveMember(ctx context.Context, storeID, adminUserID int64) error {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM shop_members WHERE store_id = ? AND admin_user_id = ?`, storeID, adminUserID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// ListMembers 列出门店成员。
func (s *Store) ListMembers(ctx context.Context, storeID int64) ([]Member, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT m.id, m.store_id, m.admin_user_id, m.role, u.login, u.display_name, m.created_at
		 FROM shop_members m JOIN admin_users u ON u.id = m.admin_user_id
		 WHERE m.store_id = ? ORDER BY FIELD(m.role,'OWNER','MANAGER','STAFF'), m.created_at`, storeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Member
	for rows.Next() {
		var m Member
		if err := rows.Scan(&m.ID, &m.StoreID, &m.AdminUserID, &m.Role, &m.Login, &m.DisplayName, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// AssignStoreOwner 指定唯一店主（平台操作，PRD §10.5）。
// 普通账号只能绑定一个门店：冲突时返回错误，调用方需先解除旧关系。
// 事务内：若账号已在别店有成员关系则拒绝；否则升级为该店 OWNER 并降其它 OWNER。
func (s *Store) AssignStoreOwner(ctx context.Context, storeID, adminUserID int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// 账号已有任何成员关系？
	var cnt int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM shop_members WHERE admin_user_id = ?`, adminUserID).Scan(&cnt); err != nil {
		return err
	}
	if cnt > 0 {
		// 若已是该店成员则允许（升级）；否则冲突。
		var existingStore int64
		err := tx.QueryRowContext(ctx,
			`SELECT store_id FROM shop_members WHERE admin_user_id = ? LIMIT 1`, adminUserID).Scan(&existingStore)
		if err != nil {
			return err
		}
		if existingStore != storeID {
			return errors.New("account already bound to another store; remove old membership first")
		}
	}
	// 唯一店主：降其它 OWNER。
	if _, err := tx.ExecContext(ctx,
		`UPDATE shop_members SET role = 'MANAGER' WHERE store_id = ? AND role = 'OWNER'`, storeID); err != nil {
		return err
	}
	if cnt == 0 {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO shop_members (store_id, admin_user_id, role) VALUES (?,?, 'OWNER')`, storeID, adminUserID); err != nil {
			return err
		}
	} else {
		if _, err := tx.ExecContext(ctx,
			`UPDATE shop_members SET role = 'OWNER' WHERE store_id = ? AND admin_user_id = ?`, storeID, adminUserID); err != nil {
			return err
		}
	}
	return tx.Commit()
}
