// Package security 提供跨域安全原语：密码哈希、随机 ID/令牌、AES-GCM 凭证加密。
package security

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/bcrypt"
)

// 密码长度约束（PRD §5.1：新密码 12..72）。
const (
	MinPasswordLen = 12
	MaxPasswordLen = 72 // bcrypt 硬上限
)

// ValidatePasswordLen 校验明文密码长度（PRD §5.1）。
func ValidatePasswordLen(pw string) error {
	n := len(pw)
	if n < MinPasswordLen {
		return fmt.Errorf("password too short: need at least %d", MinPasswordLen)
	}
	if n > MaxPasswordLen {
		return fmt.Errorf("password too long: max %d", MaxPasswordLen)
	}
	return nil
}

// HashPassword 返回 bcrypt 哈希（cost 12）。
func HashPassword(pw string) (string, error) {
	if err := ValidatePasswordLen(pw); err != nil {
		return "", err
	}
	b, err := bcrypt.GenerateFromPassword([]byte(pw), 12)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// VerifyPassword 校验明文与哈希是否匹配。
func VerifyPassword(hash, pw string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)) == nil
}

// NewToken 生成 n 字节的 base64url token（去除填充）。
func NewToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// NewHexID 生成 n 字节的 hex id。
func NewHexID(n int) (string, error) {
	b := make([]byte, n)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", b), nil
}

// HashToken 返回 token 的 SHA-256（用于查找唯一约束，不存原值）。
func HashToken(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}

// HashTokenStr 返回 token 的 SHA-256 hex 字符串（seed/演示用固定 token）。
func HashTokenStr(token string) string {
	h := sha256.Sum256([]byte(token))
	return fmt.Sprintf("%x", h[:])
}

// ── AES-GCM 凭证加密（PRD §18/§14.1/§13.1：密钥只写不读）──────────────

// Encryptor 使用 AES-256-GCM 加密凭证密文（AppSecret/商户私钥/APIv3/打印 KEY）。
// key 应为 32 字节；dev 模式可由 config 派生。
type Encryptor struct {
	aead cipher.AEAD
}

// NewEncryptor 用 32 字节 key 构造；key 长度非 32 返回错误。
func NewEncryptor(key []byte) (*Encryptor, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("credential key must be 32 bytes, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Encryptor{aead: aead}, nil
}

// Seal 返回 (ciphertext, nonce)，均含 nonce 前缀以便后续解密。
func (e *Encryptor) Seal(plaintext []byte) (ciphertext, nonce []byte, err error) {
	if plaintext == nil {
		return nil, nil, nil
	}
	nonce = make([]byte, e.aead.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, err
	}
	ciphertext = e.aead.Seal(nil, nonce, plaintext, nil)
	return ciphertext, nonce, nil
}

// Open 解密 (ciphertext, nonce)。
func (e *Encryptor) Open(ciphertext, nonce []byte) ([]byte, error) {
	if ciphertext == nil {
		return nil, nil
	}
	if len(nonce) != e.aead.NonceSize() {
		return nil, errors.New("invalid nonce size")
	}
	return e.aead.Open(nil, nonce, ciphertext, nil)
}
