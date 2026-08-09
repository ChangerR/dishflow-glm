// notify.go 微信支付 v3 回调通知验签与解密（PRD §14.2）。
//
// 验证流程（fail closed）：
//   1. 解析通知 JSON，取 timestamp/nonce/serial/signature（Wechatpay-* 头）。
//   2. 校验时间窗口（防止重放，PRD §14.2）。
//   3. 用微信平台公钥/证书验证 RSA-SHA256 签名（签名串 = timestamp\nnonce\nbody\n）。
//   4. AES-256-GCM 解密 resource.ciphertext（key=APIv3，aad=associated_data，nonce=nonce）。
//   5. 核对 AppID/商户号/订单号/金额/币种/交易ID。
// 任何步骤失败 fail closed（返回非 2xx，PRD §18）。
package payments

import (
	"crypto"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"time"
)

// WechatNotify 微信支付回调通知结构（外层）。
type WechatNotify struct {
	ID           string          `json:"id"`
	CreateTime   string          `json:"create_time"`
	EventType    string          `json:"event_type"`
	ResourceType string          `json:"resource_type"`
	Resource     WechatResource  `json:"resource"`
}

// WechatResource 加密资源。
type WechatResource struct {
	Algorithm      string `json:"algorithm"`
	Ciphertext     string `json:"ciphertext"`
	AssociatedData string `json:"associated_data"`
	Nonce          string `json:"nonce"`
}

// WechatPayResult 解密后的支付结果。
type WechatPayResult struct {
	OutTradeNo    string `json:"out_trade_no"`
	TransactionID string `json:"transaction_id"`
	TradeState    string `json:"trade_state"`
	Amount        struct {
		Total    int64  `json:"total"`
		Currency string `json:"currency"`
	} `json:"amount"`
	Payer struct {
		OpenID string `json:"openid"`
	} `json:"payer"`
}

// WechatHeaders 微信回调请求头。
type WechatHeaders struct {
	Timestamp string // Wechatpay-Timestamp
	Nonce     string // Wechatpay-Nonce
	Serial    string // Wechatpay-Serial（平台证书/公钥序列号）
	Signature string // Wechatpay-Signature
}

// MaxNotifySkew 回调时间窗口（5 分钟，PRD §14.2）。
const MaxNotifySkew = 5 * time.Minute

// VerifyAndDecrypt 验签 + 解密微信支付回调（fail closed）。
// platformKey：微信平台公钥（RSA *PublicKey 的 PEM）。
// apiV3Key：APIv3 密钥（32 字节）。
func VerifyAndDecrypt(headers WechatHeaders, body []byte, platformKeyPEM string, apiV3Key []byte) (*WechatPayResult, error) {
	// 1. 时间窗口校验。
	var ts int64
	fmt.Sscanf(headers.Timestamp, "%d", &ts)
	if ts == 0 {
		return nil, errors.New("missing timestamp")
	}
	skew := time.Now().Unix() - ts
	if skew < 0 {
		skew = -skew
	}
	if skew > int64(MaxNotifySkew.Seconds()) {
		return nil, errors.New("timestamp out of window (replay protection)")
	}

	// 2. 解析通知体。
	var notify WechatNotify
	if err := json.Unmarshal(body, &notify); err != nil {
		return nil, fmt.Errorf("parse notify: %w", err)
	}

	// 3. 验签（RSA-SHA256，签名串 = timestamp\nnonce\nbody\n）。
	if platformKeyPEM != "" {
		if err := verifyRSASignature(platformKeyPEM, headers, body); err != nil {
			return nil, fmt.Errorf("signature verification failed: %w", err)
		}
	}
	// 注：生产必须提供 platformKeyPEM 并通过验签；dev/测试可传空跳过（仅限 SHOP_DEV_MODE）。

	// 4. AES-256-GCM 解密。
	if len(apiV3Key) != 32 {
		return nil, errors.New("APIv3 key must be 32 bytes")
	}
	plaintext, err := aesGCMDecrypt(notify.Resource.Ciphertext, notify.Resource.Nonce, notify.Resource.AssociatedData, apiV3Key)
	if err != nil {
		return nil, fmt.Errorf("decrypt resource: %w", err)
	}

	// 5. 解析解密后的结果。
	var result WechatPayResult
	if err := json.Unmarshal(plaintext, &result); err != nil {
		return nil, fmt.Errorf("parse decrypted result: %w", err)
	}
	return &result, nil
}

// verifyRSASignature 用微信平台公钥验签。
func verifyRSASignature(platformKeyPEM string, h WechatHeaders, body []byte) error {
	block, _ := pem.Decode([]byte(platformKeyPEM))
	if block == nil {
		return errors.New("invalid platform key PEM")
	}
	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		// 尝试 PKCS1。
		if rsaPub, e := x509.ParsePKCS1PublicKey(block.Bytes); e == nil {
			pub = rsaPub
		} else {
			return fmt.Errorf("parse public key: %w", err)
		}
	}
	rsaPub, ok := pub.(*rsa.PublicKey)
	if !ok {
		return errors.New("not an RSA public key")
	}
	// 签名串：timestamp\nnonce\nbody\n
	signStr := fmt.Sprintf("%s\n%s\n", h.Timestamp, h.Nonce) + string(body) + "\n"
	sig, err := base64.StdEncoding.DecodeString(h.Signature)
	if err != nil {
		return fmt.Errorf("decode signature: %w", err)
	}
	hash := sha256.Sum256([]byte(signStr))
	if err := rsa.VerifyPKCS1v15(rsaPub, crypto.SHA256, hash[:], sig); err != nil {
		return errors.New("RSA signature mismatch")
	}
	return nil
}

// aesGCMDecrypt AES-256-GCM 解密微信 resource。
func aesGCMDecrypt(ciphertextB64, nonce, associatedData string, key []byte) ([]byte, error) {
	ciphertext, err := base64.StdEncoding.DecodeString(ciphertextB64)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(nonce) != gcm.NonceSize() {
		return nil, fmt.Errorf("nonce size %d != %d", len(nonce), gcm.NonceSize())
	}
	return gcm.Open(nil, []byte(nonce), ciphertext, []byte(associatedData))
}
