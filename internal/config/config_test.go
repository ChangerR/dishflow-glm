package config

import (
	"testing"
)

func TestLoad_RequiresDSN(t *testing.T) {
	t.Setenv("SHOP_DATABASE_DSN", "")
	t.Setenv("SHOP_DEV_MODE", "false")
	t.Setenv("SHOP_SESSION_SIGNING_KEY", "0123456789abcdef0123456789abcdef")
	t.Setenv("SHOP_QUOTE_SIGNING_KEY", "0123456789abcdef0123456789abcdef")
	t.Setenv("SHOP_CREDENTIAL_KEY", "0123456789abcdef")
	if _, err := Load(); err == nil {
		t.Fatal("expected error when DSN missing")
	}
}

func TestLoad_DevModeRelaxesSecrets(t *testing.T) {
	t.Setenv("SHOP_DATABASE_DSN", "shop:shop@tcp(127.0.0.1:3306)/dishflow")
	t.Setenv("SHOP_DEV_MODE", "true")
	// 故意不设签名密钥；dev 模式应放行。
	cfg, err := Load()
	if err != nil {
		t.Fatalf("dev mode should boot without secrets: %v", err)
	}
	if cfg.DatabaseDSN == "" {
		t.Fatal("DSN should be populated")
	}
	if cfg.QuoteTTL.Minutes() != 10 {
		t.Fatalf("quote TTL should be 10m, got %v", cfg.QuoteTTL)
	}
}

func TestLoad_ProdRequiresLongKeys(t *testing.T) {
	t.Setenv("SHOP_DATABASE_DSN", "shop:shop@tcp(127.0.0.1:3306)/dishflow")
	t.Setenv("SHOP_DEV_MODE", "false")
	t.Setenv("SHOP_SESSION_SIGNING_KEY", "short") // too short
	t.Setenv("SHOP_QUOTE_SIGNING_KEY", "0123456789abcdef0123456789abcdef")
	t.Setenv("SHOP_CREDENTIAL_KEY", "0123456789abcdef")
	if _, err := Load(); err == nil {
		t.Fatal("expected error when session signing key too short in prod")
	}
}

func TestLoad_TrustedProxiesParsed(t *testing.T) {
	t.Setenv("SHOP_DATABASE_DSN", "shop:shop@tcp(127.0.0.1:3306)/dishflow")
	t.Setenv("SHOP_DEV_MODE", "true")
	t.Setenv("SHOP_TRUSTED_PROXIES", " 10.0.0.1 , 10.0.0.2 ,")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"10.0.0.1", "10.0.0.2"}
	if len(cfg.TrustedProxies) != len(want) {
		t.Fatalf("trusted proxies = %v, want %v", cfg.TrustedProxies, want)
	}
	for i := range want {
		if cfg.TrustedProxies[i] != want[i] {
			t.Fatalf("trusted proxies[%d] = %q, want %q", i, cfg.TrustedProxies[i], want[i])
		}
	}
}
