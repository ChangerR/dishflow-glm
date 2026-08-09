// Package config loads and validates DishFlow runtime configuration from
// environment variables. All SHOP_* variables are documented in .env.example.
//
// 配置遵循 PRD §0.1：业务规则优先；过短/空的安全密钥在非开发模式下
// fail closed（生产必须提供足够长的签名/加密密钥）。
package config

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

// Config holds all runtime configuration.
type Config struct {
	ServeAddr          string
	DatabaseDSN        string
	RedisAddr          string
	SessionSigningKey  string
	QuoteSigningKey    string
	CredentialKey      string
	TrustedProxies     []string
	DevMode            bool
	LogLevel           string

	// 会话/报价派生参数（PRD §5.1 / §4.4）。
	SessionIdleTTL     time.Duration // 空闲过期 8h
	SessionAbsoluteTTL time.Duration // 绝对最长 7d
	SessionRenewSlack  time.Duration // 活跃续期最频繁每 30min
	QuoteTTL           time.Duration // 十分钟报价
}

// Load reads configuration from the environment and validates it.
// In non-dev mode, missing/short security keys cause a hard error (fail closed).
func Load() (Config, error) {
	cfg := Config{
		ServeAddr:         getenv("SHOP_SERVE_ADDR", "127.0.0.1:8080"),
		DatabaseDSN:       getenv("SHOP_DATABASE_DSN", ""),
		RedisAddr:         getenv("SHOP_REDIS_ADDR", "redis://127.0.0.1:6379/0"),
		SessionSigningKey: os.Getenv("SHOP_SESSION_SIGNING_KEY"),
		QuoteSigningKey:   os.Getenv("SHOP_QUOTE_SIGNING_KEY"),
		CredentialKey:     os.Getenv("SHOP_CREDENTIAL_KEY"),
		DevMode:           strings.EqualFold(os.Getenv("SHOP_DEV_MODE"), "true"),
		LogLevel:          strings.ToLower(getenv("SHOP_LOG_LEVEL", "info")),
		TrustedProxies:    splitCSV(os.Getenv("SHOP_TRUSTED_PROXIES")),

		SessionIdleTTL:     8 * time.Hour,
		SessionAbsoluteTTL: 7 * 24 * time.Hour,
		SessionRenewSlack:  30 * time.Minute,
		QuoteTTL:           10 * time.Minute,
	}

	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) validate() error {
	var errs []error
	if c.DatabaseDSN == "" {
		errs = append(errs, errors.New("SHOP_DATABASE_DSN is required"))
	}
	// 生产模式（非 dev）下，签名/加密密钥必须满足最小强度（PRD §18）。
	if !c.DevMode {
		if len(c.SessionSigningKey) < 32 {
			errs = append(errs, errors.New("SHOP_SESSION_SIGNING_KEY must be at least 32 chars (or set SHOP_DEV_MODE=true)"))
		}
		if len(c.QuoteSigningKey) < 32 {
			errs = append(errs, errors.New("SHOP_QUOTE_SIGNING_KEY must be at least 32 chars (or set SHOP_DEV_MODE=true)"))
		}
		if c.CredentialKey == "" {
			errs = append(errs, errors.New("SHOP_CREDENTIAL_KEY is required (or set SHOP_DEV_MODE=true)"))
		}
	}
	switch c.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		errs = append(errs, fmt.Errorf("invalid SHOP_LOG_LEVEL %q", c.LogLevel))
	}
	if len(errs) > 0 {
		return fmt.Errorf("invalid config: %w", joinErrors(errs))
	}
	return nil
}

// DevKey returns a deterministic weak key for dev mode so the process can boot
// without real secrets. NEVER used when DevMode is false.
func (c Config) devKey(seed string) []byte {
	// 仅用于开发签名，长度满足 32 字节。
	b := make([]byte, 32)
	for i := 0; i < 32; i++ {
		b[i] = byte('a' + ((i + int(seed[0])) % 26))
	}
	return b
}

// CredentialKey32 返回用于 AES-GCM 凭证加密的 32 字节 key。
// 生产必须提供 SHOP_CREDENTIAL_KEY（足够长）；dev 模式下用派生弱 key。
func (c Config) CredentialKey32() []byte {
	if c.CredentialKey != "" {
		h := sha256.Sum256([]byte(c.CredentialKey))
		return h[:]
	}
	if c.DevMode {
		return c.devKey("credentials")
	}
	return nil
}

// QuoteSigningKeyBytes 返回用于 HMAC 签名 quote_token 的 key（PRD §4.4.6）。
// 生产必须足够长；dev 模式用派生弱 key。
func (c Config) QuoteSigningKeyBytes() []byte {
	if c.QuoteSigningKey != "" {
		return []byte(c.QuoteSigningKey)
	}
	if c.DevMode {
		return c.devKey("quotes")
	}
	return nil
}

func getenv(key, def string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return def
}

func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func joinErrors(errs []error) error {
	msgs := make([]string, len(errs))
	for i, e := range errs {
		msgs[i] = e.Error()
	}
	return errors.New(strings.Join(msgs, "; "))
}
