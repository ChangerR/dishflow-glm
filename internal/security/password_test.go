package security

import (
	"bytes"
	"strings"
	"testing"
)

func TestPasswordHashAndVerify(t *testing.T) {
	pw := "correct horse battery staple"
	hash, err := HashPassword(pw)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if !VerifyPassword(hash, pw) {
		t.Fatal("verify should succeed for correct password")
	}
	if VerifyPassword(hash, pw+"x") {
		t.Fatal("verify should fail for wrong password")
	}
}

func TestPasswordLenValidation(t *testing.T) {
	cases := []struct {
		pw   string
		ok   bool
	}{
		{"short", false},                     // < 12
		{strings.Repeat("a", 11), false},     // 11
		{strings.Repeat("a", 12), true},      // 12
		{strings.Repeat("a", 72), true},      // 72
		{strings.Repeat("a", 73), false},     // > 72
	}
	for _, c := range cases {
		err := ValidatePasswordLen(c.pw)
		if c.ok && err != nil {
			t.Errorf("len %d expected ok, got %v", len(c.pw), err)
		}
		if !c.ok && err == nil {
			t.Errorf("len %d expected error", len(c.pw))
		}
	}
}

func TestNewTokenUniqueness(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 1000; i++ {
		tok, err := NewToken(32)
		if err != nil {
			t.Fatal(err)
		}
		if seen[tok] {
			t.Fatalf("duplicate token generated at iteration %d", i)
		}
		seen[tok] = true
	}
}

func TestEncryptorRoundTrip(t *testing.T) {
	key := bytes.Repeat([]byte{0x42}, 32)
	enc, err := NewEncryptor(key)
	if err != nil {
		t.Fatalf("new encryptor: %v", err)
	}
	plain := []byte("apiclient_key_pem_secret_content")
	ct, nonce, err := enc.Seal(plain)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if bytes.Equal(ct, plain) {
		t.Fatal("ciphertext equals plaintext")
	}
	got, err := enc.Open(ct, nonce)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatalf("round trip mismatch: got %q", got)
	}
}

func TestEncryptorNilPlaintext(t *testing.T) {
	enc, _ := NewEncryptor(bytes.Repeat([]byte{1}, 32))
	ct, nonce, err := enc.Seal(nil)
	if err != nil {
		t.Fatal(err)
	}
	if ct != nil || nonce != nil {
		t.Fatal("nil plaintext should yield nil ciphertext/nonce")
	}
	got, err := enc.Open(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatal("expected nil plaintext back")
	}
}

func TestEncryptorTamperFails(t *testing.T) {
	enc, _ := NewEncryptor(bytes.Repeat([]byte{7}, 32))
	ct, nonce, _ := enc.Seal([]byte("secret"))
	ct[0] ^= 0xff // 篡改密文
	if _, err := enc.Open(ct, nonce); err == nil {
		t.Fatal("tampered ciphertext should fail to decrypt")
	}
}

func TestEncryptorBadKeyLen(t *testing.T) {
	if _, err := NewEncryptor([]byte("too-short")); err == nil {
		t.Fatal("expected error for short key")
	}
}
