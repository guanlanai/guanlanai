package secrets

import (
	"bytes"
	"encoding/base64"
	"testing"
)

func testMasterKey() string {
	return base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
}

func TestEncryptDecrypt(t *testing.T) {
	cipher, err := NewAESGCM(testMasterKey(), 3)
	if err != nil {
		t.Fatalf("NewAESGCM() error = %v", err)
	}
	if cipher.Version() != 3 {
		t.Fatalf("Version() = %d, want 3", cipher.Version())
	}

	ciphertext, nonce, err := cipher.Encrypt("sk-test-key", []byte("user:1"))
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}
	if bytes.Contains(ciphertext, []byte("sk-test-key")) {
		t.Fatal("密文不应包含明文")
	}

	plaintext, err := cipher.Decrypt(ciphertext, nonce, []byte("user:1"))
	if err != nil {
		t.Fatalf("Decrypt() error = %v", err)
	}
	if plaintext != "sk-test-key" {
		t.Fatalf("Decrypt() = %q", plaintext)
	}
}

func TestDecryptRejectsWrongAAD(t *testing.T) {
	cipher, _ := NewAESGCM(testMasterKey(), 1)
	ciphertext, nonce, _ := cipher.Encrypt("sk-test-key", []byte("user:1"))

	if _, err := cipher.Decrypt(ciphertext, nonce, []byte("user:2")); err == nil {
		t.Fatal("错误 AAD 应当解密失败")
	}
}

func TestDecryptRejectsTampering(t *testing.T) {
	cipher, _ := NewAESGCM(testMasterKey(), 1)
	ciphertext, nonce, _ := cipher.Encrypt("sk-test-key", []byte("user:1"))
	ciphertext[0] ^= 0xff

	if _, err := cipher.Decrypt(ciphertext, nonce, []byte("user:1")); err == nil {
		t.Fatal("篡改密文应当解密失败")
	}
}

func TestNewAESGCMRejectsBadKey(t *testing.T) {
	if _, err := NewAESGCM("bad", 1); err == nil {
		t.Fatal("无效主密钥应当报错")
	}
}

func TestMask(t *testing.T) {
	tests := map[string]string{
		"short":       "****",
		"12345678":    "****",
		"123456789":   "12...89",
		"sk-test-key": "sk...ey",
	}
	for input, want := range tests {
		if got := Mask(input); got != want {
			t.Fatalf("Mask(%q) = %q, want %q", input, got, want)
		}
	}
}
