//go:build integration

// P6 集成测试：券领取幂等、入会手机号加密与重复绑定、积分流水幂等。
// 运行：SHOP_DATABASE_DSN=... go test -tags=integration -race ./internal/integrationtest/
package integrationtest

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/dishflow/zshop/internal/marketing"
	"github.com/dishflow/zshop/internal/members"
	"github.com/dishflow/zshop/internal/security"
)

func TestCouponClaim_Idempotent(t *testing.T) {
	db := dbOnly(t)
	defer db.Close()
	ctx := context.Background()
	c := uniq()

	res, _ := db.ExecContext(ctx, `INSERT INTO stores (name, timezone) VALUES (?, 'Asia/Shanghai')`, fmt.Sprintf("券店_%d", c))
	storeID, _ := res.LastInsertId()
	res, _ = db.ExecContext(ctx, `INSERT INTO customers (store_id, wechat_openid) VALUES (?, ?)`, storeID, fmt.Sprintf("oid_%d", c))
	customerID, _ := res.LastInsertId()

	s := marketing.NewStore(db)
	ends := time.Now().Add(24 * time.Hour)
	tplID, err := s.CreateCouponTemplate(ctx, marketing.CouponTemplate{
		StoreID: storeID, Name: "新人券", MinSpendCents: 0, DiscountCents: 1000,
		StartsAt: time.Now().Add(-time.Hour), EndsAt: ends, PubliclyClaim: true, Audience: "ALL",
	})
	if err != nil {
		t.Fatal(err)
	}

	// 第一次领取成功。
	if _, err := s.Claim(ctx, storeID, customerID, tplID); err != nil {
		t.Fatalf("first claim: %v", err)
	}
	// 第二次领取（同顾客同模板）应被拒（幂等，PRD §4.11）。
	if _, err := s.Claim(ctx, storeID, customerID, tplID); err != marketing.ErrAlreadyClaimed {
		t.Fatalf("expected ErrAlreadyClaimed, got %v", err)
	}
}

func TestMembership_PhoneEncryptionAndDuplicateBound(t *testing.T) {
	db := dbOnly(t)
	defer db.Close()
	ctx := context.Background()
	c := uniq()

	res, _ := db.ExecContext(ctx, `INSERT INTO stores (name, timezone) VALUES (?, 'Asia/Shanghai')`, fmt.Sprintf("会员店_%d", c))
	storeID, _ := res.LastInsertId()
	res, _ = db.ExecContext(ctx, `INSERT INTO customers (store_id, wechat_openid) VALUES (?, ?)`, storeID, fmt.Sprintf("oidA_%d", c))
	custA, _ := res.LastInsertId()
	res, _ = db.ExecContext(ctx, `INSERT INTO customers (store_id, wechat_openid) VALUES (?, ?)`, storeID, fmt.Sprintf("oidB_%d", c))
	custB, _ := res.LastInsertId()

	enc, err := security.NewEncryptor([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	s := members.NewStore(db, enc)

	// 顾客 A 入会（手机号加密保存）。
	_, isNew, err := s.Join(ctx, storeID, custA, "13800138000", "86")
	if err != nil {
		t.Fatalf("A join: %v", err)
	}
	if !isNew {
		t.Fatal("A should be new member")
	}
	// 验证手机号加密存储（hash 非空、last4、加密密文存在）。
	var hash, last4, phoneEnc []byte
	db.QueryRowContext(ctx, `SELECT phone_hash, phone_last4, phone_encrypted FROM customers WHERE id=?`, custA).Scan(&hash, &last4, &phoneEnc)
	if len(hash) == 0 || string(last4) != "8000" || len(phoneEnc) == 0 {
		t.Fatalf("phone not stored encrypted: hash=%v last4=%s encLen=%d", hash, string(last4), len(phoneEnc))
	}
	// 解密回原文验证。
	plain, err := enc.Open(phoneEnc, nil)
	if err != nil {
		// nonce 单独存：取 phone_nonce。
		var nonce []byte
		db.QueryRowContext(ctx, `SELECT phone_nonce FROM customers WHERE id=?`, custA).Scan(&nonce)
		plain, err = enc.Open(phoneEnc, nonce)
		if err != nil {
			t.Fatalf("decrypt phone: %v", err)
		}
	}
	if string(plain) != "13800138000" {
		t.Fatalf("decrypted phone = %q", string(plain))
	}

	// 顾客 B 用同手机号入会应被拒（同门店同手机号唯一，PRD §4.12）。
	_, _, err = s.Join(ctx, storeID, custB, "13800138000", "86")
	if err != members.ErrPhoneBound {
		t.Fatalf("expected ErrPhoneBound, got %v", err)
	}

	// A 重复入会返回现有会员，不报错、isNew=false。
	m2, isNew2, err := s.Join(ctx, storeID, custA, "13800138000", "86")
	if err != nil || isNew2 {
		t.Fatalf("re-join should return existing member: err=%v isNew=%v", err, isNew2)
	}
	if m2.MemberNo == "" {
		t.Fatal("member_no should be stable")
	}
}

func TestPoints_AwardAndAdjustIdempotent(t *testing.T) {
	db := dbOnly(t)
	defer db.Close()
	ctx := context.Background()
	c := uniq()

	res, _ := db.ExecContext(ctx, `INSERT INTO stores (name, timezone, points_per_yuan) VALUES (?, 'Asia/Shanghai', 1)`, fmt.Sprintf("积分店_%d", c))
	storeID, _ := res.LastInsertId()
	res, _ = db.ExecContext(ctx, `INSERT INTO customers (store_id, wechat_openid) VALUES (?, ?)`, storeID, fmt.Sprintf("oid_%d", c))
	customerID, _ := res.LastInsertId()

	enc, _ := security.NewEncryptor([]byte("0123456789abcdef0123456789abcdef"))
	memStore := members.NewStore(db, enc)
	// 入会。
	memStore.Join(ctx, storeID, customerID, "13900139000", "86")

	// 支付 250 分（2.5 元，向下取整到 2 元，×1 = 2 分）。
	bal, err := memStore.AwardPoints(ctx, storeID, customerID, 1, 250, 1, "award-1")
	if err != nil {
		t.Fatal(err)
	}
	if bal != 2 {
		t.Fatalf("balance = %d, want 2", bal)
	}
	// 重复同幂等键不应重复入账（idempotency_key 唯一约束拒绝）。
	bal2, err := memStore.AwardPoints(ctx, storeID, customerID, 1, 250, 1, "award-1")
	if err == nil && bal2 != 2 {
		// 幂等命中既有可能返回 (0,nil)（已入账）或重复键错误，关键是余额不变。
		t.Fatalf("idempotent award balance = %d, want unchanged", bal2)
	}
	// 验证余额仍为 2。
	m, _ := memStore.GetByCustomer(ctx, storeID, customerID)
	if m.PointsBalance != 2 {
		t.Fatalf("balance = %d, want 2 (no double award)", m.PointsBalance)
	}

	// 人工调整 +5。
	mAfterAward, _ := memStore.GetByCustomer(ctx, storeID, customerID)
	bal3, err := memStore.AdjustPoints(ctx, mAfterAward.ID, storeID, customerID, 99, 5, "补偿", "adj-1")
	if err != nil {
		t.Fatal(err)
	}
	if bal3 != 7 {
		t.Fatalf("after adjust balance = %d, want 7", bal3)
	}
	// 调整 delta=0 应失败。
	if _, err := memStore.AdjustPoints(ctx, mAfterAward.ID, storeID, customerID, 99, 0, "x", "adj-2"); err == nil {
		t.Fatal("delta=0 should fail")
	}
	// 调整到负应失败。
	if _, err := memStore.AdjustPoints(ctx, mAfterAward.ID, storeID, customerID, 99, -1000, "x", "adj-3"); err != members.ErrInsufficientPoints {
		t.Fatalf("expected ErrInsufficientPoints, got %v", err)
	}
}
