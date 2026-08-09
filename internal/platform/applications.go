package platform

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dishflow/zshop/internal/authn"
)

// ShopApplication 表示开店申请（PRD §10.4）。
type ShopApplication struct {
	ID                 int64
	ApplicantUserID    int64
	StoreName          string
	Contact            string
	Status             string // PENDING/APPROVED/REJECTED
	SubmittedAt        string
	ReviewedAt         sql.NullTime
	ReviewerUserID     sql.NullInt64
	CreatedStoreID     sql.NullInt64
	Note               string
}

// ShopJoinRequest 表示加入申请（PRD §10.4）。
type ShopJoinRequest struct {
	ID              int64
	StoreID         int64
	ApplicantUserID int64
	RequestedRole   string
	Status          string
	SubmittedAt     string
	ReviewedAt      sql.NullTime
	ReviewerUserID  sql.NullInt64
	Note            string
}

// CreateShopApplication 提交开店申请。
func (s *Store) CreateShopApplication(ctx context.Context, applicantUserID int64, storeName, contact string) (int64, error) {
	storeName = strings.TrimSpace(storeName)
	if storeName == "" {
		return 0, errors.New("store name required")
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO shop_applications (applicant_admin_user_id, store_name, contact, status)
		 VALUES (?,?,?, 'PENDING')`, applicantUserID, storeName, contact)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ListMyShopApplications 申请人查看自己的开店申请。
func (s *Store) ListMyShopApplications(ctx context.Context, applicantUserID int64) ([]ShopApplication, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, applicant_admin_user_id, store_name, contact, status, submitted_at, reviewed_at,
		        reviewer_admin_user_id, created_store_id, note
		 FROM shop_applications WHERE applicant_admin_user_id = ? ORDER BY id DESC`, applicantUserID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanShopApplications(rows)
}

// ListPlatformShopApplications 平台查看开店申请（可选状态过滤）。
func (s *Store) ListPlatformShopApplications(ctx context.Context, status string) ([]ShopApplication, error) {
	q := `SELECT id, applicant_admin_user_id, store_name, contact, status, submitted_at, reviewed_at,
	             reviewer_admin_user_id, created_store_id, note
	      FROM shop_applications`
	args := []any{}
	if status != "" {
		q += " WHERE status = ?"
		args = append(args, status)
	}
	q += " ORDER BY id DESC"
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanShopApplications(rows)
}

func scanShopApplications(rows *sql.Rows) ([]ShopApplication, error) {
	var out []ShopApplication
	for rows.Next() {
		var a ShopApplication
		if err := rows.Scan(&a.ID, &a.ApplicantUserID, &a.StoreName, &a.Contact, &a.Status, &a.SubmittedAt,
			&a.ReviewedAt, &a.ReviewerUserID, &a.CreatedStoreID, &a.Note); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ApproveShopApplication 平台审批开店（PRD §10.4）。
// 事务内：建店 + 把申请人设为 OWNER + 标记申请 APPROVED，不可重复创建。
func (s *Store) ApproveShopApplication(ctx context.Context, appID, reviewerUserID int64, note string) (storeID int64, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	// 锁定申请行并校验状态。
	var app ShopApplication
	if err = tx.QueryRowContext(ctx,
		`SELECT id, applicant_admin_user_id, store_name, status, created_store_id
		 FROM shop_applications WHERE id = ? FOR UPDATE`, appID).
		Scan(&app.ID, &app.ApplicantUserID, &app.StoreName, &app.Status, &app.CreatedStoreID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, sql.ErrNoRows
		}
		return 0, err
	}
	if app.Status != "PENDING" {
		return 0, fmt.Errorf("application already %s", app.Status)
	}

	// 建店。
	res, err := tx.ExecContext(ctx, `INSERT INTO stores (name, timezone) VALUES (?, 'Asia/Shanghai')`, app.StoreName)
	if err != nil {
		return 0, fmt.Errorf("create store: %w", err)
	}
	storeID, err = res.LastInsertId()
	if err != nil {
		return 0, err
	}

	// 唯一店主：降已有 OWNER（新店无），再设申请人 OWNER。
	if _, err = tx.ExecContext(ctx,
		`UPDATE shop_members SET role='MANAGER' WHERE store_id=? AND role='OWNER'`, storeID); err != nil {
		return 0, err
	}
	if _, err = tx.ExecContext(ctx,
		`INSERT INTO shop_members (store_id, admin_user_id, role) VALUES (?,?, 'OWNER')`, storeID, app.ApplicantUserID); err != nil {
		return 0, fmt.Errorf("set owner: %w", err)
	}

	// 标记申请已通过。
	if _, err = tx.ExecContext(ctx,
		`UPDATE shop_applications SET status='APPROVED', reviewer_admin_user_id=?, reviewed_at=?, created_store_id=?, note=? WHERE id=?`,
		reviewerUserID, time.Now().UTC(), storeID, note, appID); err != nil {
		return 0, err
	}

	// 平台审计。
	if _, err = tx.ExecContext(ctx,
		`INSERT INTO audit_logs (platform_scope, actor_type, actor_admin_user_id, action, resource_type, resource_id, summary)
		 VALUES (1,'ADMIN',?, 'shop_application.approve','shop_application',?, ?)`,
		reviewerUserID, fmt.Sprintf("%d", appID), fmt.Sprintf("approved; created store id=%d, name=%q", storeID, app.StoreName)); err != nil {
		return 0, err
	}

	if err = tx.Commit(); err != nil {
		return 0, err
	}
	return storeID, nil
}

// RejectShopApplication 驳回开店申请。
func (s *Store) RejectShopApplication(ctx context.Context, appID, reviewerUserID int64, note string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE shop_applications SET status='REJECTED', reviewer_admin_user_id=?, reviewed_at=?, note=? WHERE id=? AND status='PENDING'`,
		reviewerUserID, time.Now().UTC(), note, appID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errors.New("application not pending or not found")
	}
	_, _ = s.db.ExecContext(ctx,
		`INSERT INTO audit_logs (platform_scope, actor_type, actor_admin_user_id, action, resource_type, resource_id, summary)
		 VALUES (1,'ADMIN',?, 'shop_application.reject','shop_application',?, ?)`,
		reviewerUserID, fmt.Sprintf("%d", appID), "rejected")
	return nil
}

// ── 加入申请 ──────────────────────────────────────────────────────────

// CreateShopJoinRequest 提交加入申请（PRD §10.4）。
func (s *Store) CreateShopJoinRequest(ctx context.Context, storeID, applicantUserID int64, role authn.Role) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO shop_join_requests (store_id, applicant_admin_user_id, requested_role, status)
		 VALUES (?,?,?, 'PENDING')`, storeID, applicantUserID, string(role))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ListMyShopJoinRequests 申请人查看自己的加入申请。
func (s *Store) ListMyShopJoinRequests(ctx context.Context, applicantUserID int64) ([]ShopJoinRequest, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, store_id, applicant_admin_user_id, requested_role, status, submitted_at, reviewed_at,
		        reviewer_admin_user_id, note
		 FROM shop_join_requests WHERE applicant_admin_user_id = ? ORDER BY id DESC`, applicantUserID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanShopJoinRequests(rows)
}

// ListStoreShopJoinRequests 店主查看本店加入申请。
func (s *Store) ListStoreShopJoinRequests(ctx context.Context, storeID int64) ([]ShopJoinRequest, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, store_id, applicant_admin_user_id, requested_role, status, submitted_at, reviewed_at,
		        reviewer_admin_user_id, note
		 FROM shop_join_requests WHERE store_id = ? ORDER BY id DESC`, storeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanShopJoinRequests(rows)
}

func scanShopJoinRequests(rows *sql.Rows) ([]ShopJoinRequest, error) {
	var out []ShopJoinRequest
	for rows.Next() {
		var r ShopJoinRequest
		if err := rows.Scan(&r.ID, &r.StoreID, &r.ApplicantUserID, &r.RequestedRole, &r.Status, &r.SubmittedAt,
			&r.ReviewedAt, &r.ReviewerUserID, &r.Note); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ApproveShopJoinRequest 店主批准加入申请（PRD §10.4）。通过建立唯一成员关系。
func (s *Store) ApproveShopJoinRequest(ctx context.Context, reqID, reviewerUserID int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var r ShopJoinRequest
	if err = tx.QueryRowContext(ctx,
		`SELECT id, store_id, applicant_admin_user_id, requested_role, status
		 FROM shop_join_requests WHERE id = ? FOR UPDATE`, reqID).
		Scan(&r.ID, &r.StoreID, &r.ApplicantUserID, &r.RequestedRole, &r.Status); err != nil {
		return err
	}
	if r.Status != "PENDING" {
		return fmt.Errorf("join request already %s", r.Status)
	}
	role := authn.Role(r.RequestedRole)
	if role == authn.RoleOwner {
		if _, err = tx.ExecContext(ctx,
			`UPDATE shop_members SET role='MANAGER' WHERE store_id=? AND role='OWNER'`, r.StoreID); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx,
		`INSERT INTO shop_members (store_id, admin_user_id, role) VALUES (?,?,?)`,
		r.StoreID, r.ApplicantUserID, r.RequestedRole); err != nil {
		return fmt.Errorf("create membership: %w", err)
	}
	if _, err = tx.ExecContext(ctx,
		`UPDATE shop_join_requests SET status='APPROVED', reviewer_admin_user_id=?, reviewed_at=? WHERE id=?`,
		reviewerUserID, time.Now().UTC(), reqID); err != nil {
		return err
	}
	return tx.Commit()
}

// RejectShopJoinRequest 驳回加入申请（不赋权）。
func (s *Store) RejectShopJoinRequest(ctx context.Context, reqID, reviewerUserID int64, note string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE shop_join_requests SET status='REJECTED', reviewer_admin_user_id=?, reviewed_at=?, note=? WHERE id=? AND status='PENDING'`,
		reviewerUserID, time.Now().UTC(), note, reqID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errors.New("join request not pending or not found")
	}
	return nil
}
