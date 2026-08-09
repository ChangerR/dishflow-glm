package payments

import (
	"crypto"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"testing"
	"time"
)

// 生成测试用 RSA 密钥对（验签测试）。
func testRSAKey(t *testing.T) (*rsa.PrivateKey, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	pubDER, _ := x509.MarshalPKIXPublicKey(&key.PublicKey)
	pubPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER})
	return key, string(pubPEM)
}

func TestAESGCMDecrypt_RoundTrip(t *testing.T) {
	apiV3Key := []byte("0123456789abcdef0123456789abcdef") // 32 bytes
	// 模拟微信加密。
	block, _ := aes.NewCipher(apiV3Key)
	gcm, _ := cipher.NewGCM(block)
	nonce := make([]byte, gcm.NonceSize())
	rand.Read(nonce)
	plaintext := []byte(`{"out_trade_no":"ON123","trade_state":"SUCCESS","amount":{"total":100,"currency":"CNY"}}`)
	ct := gcm.Seal(nil, nonce, plaintext, []byte("test_ad"))
	// 模拟微信格式。
	res := WechatResource{
		Ciphertext:     base64.StdEncoding.EncodeToString(ct),
		Nonce:          string(nonce),
		AssociatedData: "test_ad",
	}
	notify := WechatNotify{Resource: res}
	body, _ := json.Marshal(notify)

	result, err := aesGCMDecrypt(notify.Resource.Ciphertext, notify.Resource.Nonce, notify.Resource.AssociatedData, apiV3Key)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	var pay WechatPayResult
	json.Unmarshal(result, &pay)
	if pay.OutTradeNo != "ON123" || pay.Amount.Total != 100 {
		t.Fatalf("decrypted = %+v", pay)
	}
	_ = body
}

func TestVerifyAndDecrypt_TimeWindowExpired(t *testing.T) {
	_, pubPEM := testRSAKey(t)
	apiV3Key := []byte("0123456789abcdef0123456789abcdef")

	// timestamp 10 分钟前（超出 5 分钟窗口）。
	oldTS := fmt.Sprintf("%d", time.Now().Add(-10*time.Minute).Unix())
	h := WechatHeaders{Timestamp: oldTS, Nonce: "n", Serial: "s", Signature: "sig"}
	_, err := VerifyAndDecrypt(h, []byte(`{}`), pubPEM, apiV3Key)
	if err == nil {
		t.Fatal("expected time window error")
	}
}

func TestVerifyRSASignature_Valid(t *testing.T) {
	privKey, pubPEM := testRSAKey(t)
	body := []byte(`{"id":"evt_1"}`)
	ts := fmt.Sprintf("%d", time.Now().Unix())
	nonce := "abc123"
	// 构造签名串。
	signStr := fmt.Sprintf("%s\n%s\n", ts, nonce) + string(body) + "\n"
	hash := sha256.Sum256([]byte(signStr))
	sig, err := rsa.SignPKCS1v15(rand.Reader, privKey, crypto.SHA256, hash[:])
	if err != nil {
		t.Fatal(err)
	}
	h := WechatHeaders{Timestamp: ts, Nonce: nonce, Signature: base64.StdEncoding.EncodeToString(sig)}
	if err := verifyRSASignature(pubPEM, h, body); err != nil {
		t.Fatalf("valid signature should pass: %v", err)
	}
}

func TestVerifyRSASignature_Tampered(t *testing.T) {
	privKey, pubPEM := testRSAKey(t)
	body := []byte(`{"id":"evt_1"}`)
	ts := fmt.Sprintf("%d", time.Now().Unix())
	nonce := "abc123"
	signStr := fmt.Sprintf("%s\n%s\n", ts, nonce) + string(body) + "\n"
	hash := sha256.Sum256([]byte(signStr))
	sig, _ := rsa.SignPKCS1v15(rand.Reader, privKey, crypto.SHA256, hash[:])
	// 篡改 body。
	tamperedBody := []byte(`{"id":"evt_TAMPERED"}`)
	h := WechatHeaders{Timestamp: ts, Nonce: nonce, Signature: base64.StdEncoding.EncodeToString(sig)}
	if err := verifyRSASignature(pubPEM, h, tamperedBody); err == nil {
		t.Fatal("tampered body should fail signature verification")
	}
}

func TestAESGCMDecrypt_BadKey(t *testing.T) {
	apiV3Key := []byte("0123456789abcdef0123456789abcdef")
	block, _ := aes.NewCipher(apiV3Key)
	gcm, _ := cipher.NewGCM(block)
	nonce := make([]byte, gcm.NonceSize())
	rand.Read(nonce)
	ct := gcm.Seal(nil, nonce, []byte("test"), nil)
	// 用错误的 key 解密。
	badKey := []byte("99999999999999999999999999999999")
	_, err := aesGCMDecrypt(base64.StdEncoding.EncodeToString(ct), string(nonce), "", badKey)
	if err == nil {
		t.Fatal("decrypt with wrong key should fail")
	}
}
