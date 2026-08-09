//go:build integration

// 平台域集成测试：在真实 MySQL 上验证唯一店主、开店审批事务、加入审批。
// 运行：SHOP_DATABASE_DSN=... go test -tags=integration -race ./internal/platform/
package platform

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"

	"github.com/dishflow/zshop/internal/authn"
)

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

func TestCreateAdminAccount_DuplicateLogin(t *testing.T) {
	db := mustDB(t)
	defer db.Close()
	ctx := context.Background()
	s := NewStore(db)

	login := fmt.Sprintf("dupe_%d", uniqueCounter())
	if _, err := s.CreateAdminAccount(ctx, CreateAdminAccountInput{Login: login, DisplayName: "D", Password: "password123456"}); err != nil {
		t.Fatalf("first create: %v", err)
	}
	if _, err := s.CreateAdminAccount(ctx, CreateAdminAccountInput{Login: login, DisplayName: "D2", Password: "password123456"}); err == nil {
		t.Fatal("expected duplicate login error")
	}
}

func TestAssignStoreOwner_UniqueOwner(t *testing.T) {
	db := mustDB(t)
	defer db.Close()
	ctx := context.Background()
	s := NewStore(db)

	storeID, err := s.CreateStore(ctx, CreateStoreInput{Name: fmt.Sprintf("唯一店主店_%d", uniqueCounter())})
	if err != nil {
		t.Fatal(err)
	}
	owner1, _ := s.CreateAdminAccount(ctx, CreateAdminAccountInput{Login: fmt.Sprintf("owner1_%d", uniqueCounter()), Password: "password123456"})
	owner2, _ := s.CreateAdminAccount(ctx, CreateAdminAccountInput{Login: fmt.Sprintf("owner2_%d", uniqueCounter()), Password: "password123456"})

	// 设 owner1 为店主。
	if err := s.AssignStoreOwner(ctx, storeID, owner1); err != nil {
		t.Fatalf("assign owner1: %v", err)
	}
	// 直接 AddMember 把 owner2 设为 OWNER 应自动降 owner1。
	if err := s.AddMember(ctx, storeID, owner2, authn.RoleOwner); err != nil {
		t.Fatalf("assign owner2: %v", err)
	}

	members, err := s.ListMembers(ctx, storeID)
	if err != nil {
		t.Fatal(err)
	}
	owners := 0
	for _, m := range members {
		if m.Role == "OWNER" {
			owners++
		}
	}
	if owners != 1 {
		t.Fatalf("expected exactly 1 OWNER, got %d", owners)
	}
}

func TestAssignStoreOwner_ConflictAcrossStores(t *testing.T) {
	db := mustDB(t)
	defer db.Close()
	ctx := context.Background()
	s := NewStore(db)

	storeA, _ := s.CreateStore(ctx, CreateStoreInput{Name: fmt.Sprintf("店A_%d", uniqueCounter())})
	storeB, _ := s.CreateStore(ctx, CreateStoreInput{Name: fmt.Sprintf("店B_%d", uniqueCounter())})
	user, _ := s.CreateAdminAccount(ctx, CreateAdminAccountInput{Login: fmt.Sprintf("cross_%d", uniqueCounter()), Password: "password123456"})

	if err := s.AssignStoreOwner(ctx, storeA, user); err != nil {
		t.Fatalf("assign storeA: %v", err)
	}
	// 已归属店A，再指定为店B 店主应冲突。
	if err := s.AssignStoreOwner(ctx, storeB, user); err == nil {
		t.Fatal("expected conflict when binding to second store")
	}
}

func TestApproveShopApplication_CreatesStoreAndOwner(t *testing.T) {
	db := mustDB(t)
	defer db.Close()
	ctx := context.Background()
	s := NewStore(db)

	applicant, _ := s.CreateAdminAccount(ctx, CreateAdminAccountInput{Login: fmt.Sprintf("apply_%d", uniqueCounter()), Password: "password123456"})
	reviewer, _ := s.CreateAdminAccount(ctx, CreateAdminAccountInput{Login: fmt.Sprintf("rev_%d", uniqueCounter()), Password: "password123456"})

	name := fmt.Sprintf("开店测试_%d", uniqueCounter())
	appID, err := s.CreateShopApplication(ctx, applicant, name, "contact")
	if err != nil {
		t.Fatal(err)
	}

	storeID, err := s.ApproveShopApplication(ctx, appID, reviewer, "ok")
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if storeID <= 0 {
		t.Fatal("expected store id")
	}

	// 验证申请人成为店主。
	authStore := authn.NewStore(db)
	m, err := authStore.GetMembership(ctx, applicant, storeID)
	if err != nil {
		t.Fatalf("applicant should be member: %v", err)
	}
	if m.Role != authn.RoleOwner {
		t.Fatalf("applicant role = %s, want OWNER", m.Role)
	}

	// 重复审批应失败。
	if _, err := s.ApproveShopApplication(ctx, appID, reviewer, "ok"); err == nil {
		t.Fatal("re-approve should fail")
	}
}

func TestRejectShopApplication(t *testing.T) {
	db := mustDB(t)
	defer db.Close()
	ctx := context.Background()
	s := NewStore(db)

	applicant, _ := s.CreateAdminAccount(ctx, CreateAdminAccountInput{Login: fmt.Sprintf("rej_%d", uniqueCounter()), Password: "password123456"})
	reviewer, _ := s.CreateAdminAccount(ctx, CreateAdminAccountInput{Login: fmt.Sprintf("revr_%d", uniqueCounter()), Password: "password123456"})

	appID, _ := s.CreateShopApplication(ctx, applicant, fmt.Sprintf("驳回店_%d", uniqueCounter()), "")
	if err := s.RejectShopApplication(ctx, appID, reviewer, "no"); err != nil {
		t.Fatalf("reject: %v", err)
	}
}

func TestApproveShopJoinRequest(t *testing.T) {
	db := mustDB(t)
	defer db.Close()
	ctx := context.Background()
	s := NewStore(db)

	storeID, _ := s.CreateStore(ctx, CreateStoreInput{Name: fmt.Sprintf("加入店_%d", uniqueCounter())})
	owner, _ := s.CreateAdminAccount(ctx, CreateAdminAccountInput{Login: fmt.Sprintf("joinowner_%d", uniqueCounter()), Password: "password123456"})
	staff, _ := s.CreateAdminAccount(ctx, CreateAdminAccountInput{Login: fmt.Sprintf("joinstaff_%d", uniqueCounter()), Password: "password123456"})
	s.AssignStoreOwner(ctx, storeID, owner)

	reqID, err := s.CreateShopJoinRequest(ctx, storeID, staff, authn.RoleStaff)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ApproveShopJoinRequest(ctx, reqID, owner); err != nil {
		t.Fatalf("approve join: %v", err)
	}
	authStore := authn.NewStore(db)
	m, err := authStore.GetMembership(ctx, staff, storeID)
	if err != nil {
		t.Fatalf("staff should be member: %v", err)
	}
	if m.Role != authn.RoleStaff {
		t.Fatalf("role = %s, want STAFF", m.Role)
	}
}

func TestStorePickupDefaults(t *testing.T) {
	db := mustDB(t)
	defer db.Close()
	ctx := context.Background()
	s := NewStore(db)
	id, _ := s.CreateStore(ctx, CreateStoreInput{Name: fmt.Sprintf("默认配置_%d", uniqueCounter())})
	st, err := s.GetShop(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if st.PickupMinutes != 15 || st.PickupAdvanceDays != 7 || st.PickupSlotMinutes != 15 ||
		st.PickupSlotCapacity != 5 || st.PickupMinLeadMinutes != 30 {
		t.Fatalf("pickup defaults wrong: %+v", st)
	}
	if !st.ScheduledPickupEnabled {
		t.Fatal("scheduled pickup should default enabled")
	}
}

// uniqueCounter 返回单调递增整数，用于保证登录名/门店名唯一。
var counter int64

func uniqueCounter() int64 {
	counter++
	return time.Now().UnixNano() + counter
}
