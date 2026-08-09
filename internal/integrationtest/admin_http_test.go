//go:build integration

// 管理 HTTP 层集成测试：登录拿 cookie → 平台建店/建账号 → 指定店主 → X-Store-Id 鉴权。
// 运行：SHOP_DATABASE_DSN=... go test -tags=integration -race ./internal/integrationtest/
package integrationtest

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
	"github.com/redis/go-redis/v9"

	"github.com/dishflow/zshop/internal/authn"
	"github.com/dishflow/zshop/internal/config"
	"github.com/dishflow/zshop/internal/platform"
	"github.com/dishflow/zshop/internal/security"
	"github.com/dishflow/zshop/internal/server"
)

var counter int64

func uniq() int64 {
	counter++
	return time.Now().UnixNano() + counter
}

func mustCfg(t *testing.T) config.Config {
	return config.Config{
		DatabaseDSN:        os.Getenv("SHOP_DATABASE_DSN"),
		RedisAddr:          "redis://127.0.0.1:6379/0",
		DevMode:            true,
		LogLevel:           "error",
		SessionIdleTTL:     8 * time.Hour,
		SessionAbsoluteTTL: 7 * 24 * time.Hour,
		SessionRenewSlack:  30 * time.Minute,
		QuoteTTL:           10 * time.Minute,
		CredentialKey:      "test-credential-key",
	}
}

func setupServer(t *testing.T) (*httptest.Server, *sql.DB, func()) {
	t.Helper()
	cfg := mustCfg(t)
	dsn := cfg.DatabaseDSN
	if dsn == "" {
		dsn = "root:rootpw@tcp(127.0.0.1:3307)/dishflow?parseTime=true&loc=UTC&charset=utf8mb4"
	}
	dbx, err := sqlx.Connect("mysql", dsn)
	if err != nil {
		t.Skipf("mysql unavailable: %v", err)
	}
	db := dbx.DB
	// Redis 可选；不可用则跳过 ready 检查相关。
	rdb, _ := redis.NewClient(&redis.Options{Addr: "127.0.0.1:6379"}).Ping(context.Background()).Result()
	_ = rdb
	rClient := redis.NewClient(&redis.Options{Addr: "127.0.0.1:6379"})

	srv := server.New(cfg, server.NewLogger(cfg), db, rClient)
	ts := httptest.NewServer(srv.Router())
	return ts, db, func() {
		ts.Close()
		db.Close()
	}
}

// seedPlatformAdmin 直接插一个平台超管账号并返回登录凭证。
func seedPlatformAdmin(t *testing.T, db *sql.DB) (login, password string) {
	t.Helper()
	login = fmt.Sprintf("platadmin_%d", uniq())
	password = "password123456"
	hash, _ := security.HashPassword(password)
	_, err := db.ExecContext(context.Background(),
		`INSERT INTO admin_users (login, display_name, password_hash, enabled, is_platform_admin) VALUES (?,?,?,1,1)`,
		login, "Platform Admin", hash)
	if err != nil {
		t.Fatalf("seed platform admin: %v", err)
	}
	return login, password
}

func doJSON(t *testing.T, client *http.Client, method, urlStr string, body any, headers map[string]string) (*http.Response, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	req, err := http.NewRequest(method, urlStr, &buf)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	resp.Body.Close()
	return resp, out
}

// newClientWithJar 返回一个带 cookie jar 的 HTTP 客户端。
func newClientWithJar() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar, Timeout: 10 * time.Second}
}

func TestLoginAndSessionFlow(t *testing.T) {
	ts, db, cleanup := setupServer(t)
	defer cleanup()

	login, pw := seedPlatformAdmin(t, db)
	client := newClientWithJar()

	// 登录拿 cookie。
	resp, _ := doJSON(t, client, http.MethodPost, ts.URL+"/api/v1/admin/session",
		map[string]string{"login": login, "password": pw}, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d", resp.StatusCode)
	}

	// 会话信息。
	resp, out := doJSON(t, client, http.MethodGet, ts.URL+"/api/v1/admin/session", nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("session info status = %d", resp.StatusCode)
	}
	if out["is_platform_admin"] != true {
		t.Fatalf("expected platform admin, got %v", out)
	}

	// 退出。
	resp, _ = doJSON(t, client, http.MethodDelete, ts.URL+"/api/v1/admin/session", nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("logout status = %d", resp.StatusCode)
	}
	// 再次访问会话信息应 401（cookie 已被服务端撤销，jar 仍带但无效）。
	resp, _ = doJSON(t, client, http.MethodGet, ts.URL+"/api/v1/admin/session", nil, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("after logout status = %d, want 401", resp.StatusCode)
	}
}

func TestLoginBadPassword(t *testing.T) {
	ts, db, cleanup := setupServer(t)
	defer cleanup()
	login, _ := seedPlatformAdmin(t, db)

	client := newClientWithJar()
	resp, _ := doJSON(t, client, http.MethodPost, ts.URL+"/api/v1/admin/session",
		map[string]string{"login": login, "password": "wrong-password-xx"}, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bad password status = %d, want 401", resp.StatusCode)
	}
}

func TestPlatformCreateStoreRequiresPlatformAdmin(t *testing.T) {
	ts, db, cleanup := setupServer(t)
	defer cleanup()

	// 建一个普通账号（非平台超管）。
	login := fmt.Sprintf("normal_%d", uniq())
	hash, _ := security.HashPassword("password123456")
	_, err := db.ExecContext(context.Background(),
		`INSERT INTO admin_users (login, display_name, password_hash, enabled, is_platform_admin) VALUES (?,?,?,1,0)`,
		login, "Normal", hash)
	if err != nil {
		t.Fatal(err)
	}

	// 登录。
	client := newClientWithJar()
	resp, _ := doJSON(t, client, http.MethodPost, ts.URL+"/api/v1/admin/session",
		map[string]string{"login": login, "password": "password123456"}, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d", resp.StatusCode)
	}

	// 普通账号访问平台建店应 403。
	resp, _ = doJSON(t, client, http.MethodPost, ts.URL+"/api/v1/admin/platform/stores",
		map[string]string{"name": "不该建成"}, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("platform create by normal user status = %d, want 403", resp.StatusCode)
	}
}

func TestXStoreIdMembershipEnforced(t *testing.T) {
	ts, db, cleanup := setupServer(t)
	defer cleanup()

	// 建门店 + 普通账号 + 成员关系。
	platStore := platform.NewStore(db)
	ctx := context.Background()
	storeID, _ := platStore.CreateStore(ctx, platform.CreateStoreInput{Name: fmt.Sprintf("门店_%d", uniq())})
	hash, _ := security.HashPassword("password123456")
	login := fmt.Sprintf("member_%d", uniq())
	res, err := db.ExecContext(ctx,
		`INSERT INTO admin_users (login, display_name, password_hash, enabled, is_platform_admin) VALUES (?,?,?,1,0)`,
		login, "Member", hash)
	if err != nil {
		t.Fatal(err)
	}
	userID, _ := res.LastInsertId()
	_ = platStore.AddMember(ctx, storeID, userID, authn.RoleManager)

	// 登录。
	client := newClientWithJar()
	resp, _ := doJSON(t, client, http.MethodPost, ts.URL+"/api/v1/admin/session",
		map[string]string{"login": login, "password": "password123456"}, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d", resp.StatusCode)
	}

	// 带 X-Store-Id 但门店 id 错误应 403。
	resp, _ = doJSON(t, client, http.MethodGet, ts.URL+"/api/v1/admin/session", nil,
		map[string]string{"X-Store-Id": "999999"})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("wrong X-Store-Id status = %d, want 403", resp.StatusCode)
	}

	// 正确门店 id 应 200。
	resp, _ = doJSON(t, client, http.MethodGet, ts.URL+"/api/v1/admin/session", nil,
		map[string]string{"X-Store-Id": fmt.Sprintf("%d", storeID)})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("correct X-Store-Id status = %d, want 200", resp.StatusCode)
	}
}

// 防止 net/url 未使用导入（保留以便扩展）。
var _ = url.Parse
