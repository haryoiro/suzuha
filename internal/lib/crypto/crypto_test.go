package crypto

import (
	"encoding/hex"
	"testing"
)

func testCipher(t *testing.T) *AESGCMCipher {
	t.Helper()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	c, err := NewAESGCMCipher(hex.EncodeToString(key))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestEncryptDecrypt_RoundTrip(t *testing.T) {
	c := testCipher(t)
	original := "sk-secret-api-key-12345"

	encrypted, err := c.Encrypt(original)
	if err != nil {
		t.Fatal(err)
	}
	if encrypted == original {
		t.Error("暗号化された文字列が平文と同じ")
	}

	decrypted, err := c.Decrypt(encrypted)
	if err != nil {
		t.Fatal(err)
	}
	if decrypted != original {
		t.Errorf("復号結果が一致しない: got %q, want %q", decrypted, original)
	}
}

func TestEncryptDecrypt_EmptyString(t *testing.T) {
	c := testCipher(t)

	encrypted, err := c.Encrypt("")
	if err != nil {
		t.Fatal(err)
	}
	if encrypted != "" {
		t.Error("空文字列は空文字列のまま返すべき")
	}

	decrypted, err := c.Decrypt("")
	if err != nil {
		t.Fatal(err)
	}
	if decrypted != "" {
		t.Error("空文字列は空文字列のまま返すべき")
	}
}

func TestEncrypt_DifferentCiphertexts(t *testing.T) {
	c := testCipher(t)
	plain := "test-key"

	enc1, _ := c.Encrypt(plain)
	enc2, _ := c.Encrypt(plain)
	if enc1 == enc2 {
		t.Error("同じ平文でも毎回異なる暗号文であるべき (nonce が違う)")
	}

	// 両方復号できる
	dec1, _ := c.Decrypt(enc1)
	dec2, _ := c.Decrypt(enc2)
	if dec1 != plain || dec2 != plain {
		t.Error("どちらも正しく復号できるべき")
	}
}

func TestDecrypt_WrongKey(t *testing.T) {
	c1 := testCipher(t)

	key2 := make([]byte, 32)
	for i := range key2 {
		key2[i] = byte(i + 100)
	}
	c2, _ := NewAESGCMCipher(hex.EncodeToString(key2))

	encrypted, _ := c1.Encrypt("secret")
	_, err := c2.Decrypt(encrypted)
	if err == nil {
		t.Error("異なる鍵での復号はエラーになるべき")
	}
}

func TestNewAESGCMCipher_InvalidKey(t *testing.T) {
	// 短すぎる
	_, err := NewAESGCMCipher("aabbcc")
	if err == nil {
		t.Error("短い鍵でエラーを期待")
	}

	// 不正な hex
	_, err = NewAESGCMCipher("not-hex-at-all")
	if err == nil {
		t.Error("不正な hex でエラーを期待")
	}
}
