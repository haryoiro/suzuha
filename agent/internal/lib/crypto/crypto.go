package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
)

// AESGCMCipher は AES-256-GCM で API キーを暗号化/復号する。
type AESGCMCipher struct {
	aead cipher.AEAD
}

// NewAESGCMCipher は hex エンコードされた 32 バイトのキーから cipher を生成する。
func NewAESGCMCipher(hexKey string) (*AESGCMCipher, error) {
	key, err := hex.DecodeString(hexKey)
	if err != nil {
		return nil, fmt.Errorf("crypto: 暗号鍵の hex デコードに失敗: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("crypto: 暗号鍵は 32 バイト必要 (got %d)", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("crypto: AES の初期化に失敗: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("crypto: GCM の初期化に失敗: %w", err)
	}
	return &AESGCMCipher{aead: aead}, nil
}

// Encrypt は平文を暗号化し、base64 エンコードされた文字列を返す。
// 空文字列はそのまま返す。
func (c *AESGCMCipher) Encrypt(plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("crypto: nonce 生成に失敗: %w", err)
	}
	sealed := c.aead.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(sealed), nil
}

// Decrypt は base64 エンコードされた暗号文を復号する。
// 空文字列はそのまま返す。
func (c *AESGCMCipher) Decrypt(ciphertext string) (string, error) {
	if ciphertext == "" {
		return "", nil
	}
	data, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		return "", fmt.Errorf("crypto: base64 デコードに失敗: %w", err)
	}
	nonceSize := c.aead.NonceSize()
	if len(data) < nonceSize {
		return "", fmt.Errorf("crypto: 暗号文が短すぎる")
	}
	nonce, sealed := data[:nonceSize], data[nonceSize:]
	plaintext, err := c.aead.Open(nil, nonce, sealed, nil)
	if err != nil {
		return "", fmt.Errorf("crypto: 復号に失敗: %w", err)
	}
	return string(plaintext), nil
}
