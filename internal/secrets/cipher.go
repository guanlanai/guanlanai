package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
)

// Cipher 是用户 API Key 的加解密接口。
type Cipher interface {
	Encrypt(plaintext string, aad []byte) (ciphertext, nonce []byte, err error)
	Decrypt(ciphertext, nonce, aad []byte) (string, error)
}

// AESGCM 使用 AES-256-GCM 加密用户 Key。
type AESGCM struct {
	aead    cipher.AEAD
	version int
}

// NewAESGCM 从 base64 编码的 32 字节主密钥创建加密器。
func NewAESGCM(masterKeyBase64 string, version int) (*AESGCM, error) {
	raw, err := base64.StdEncoding.DecodeString(masterKeyBase64)
	if err != nil || len(raw) != 32 {
		return nil, errors.New("APP_MASTER_KEY 必须是 32 字节随机值的 base64")
	}

	block, err := aes.NewCipher(raw)
	if err != nil {
		return nil, fmt.Errorf("初始化 AES: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("初始化 GCM: %w", err)
	}
	return &AESGCM{aead: aead, version: version}, nil
}

// Version 返回密钥版本，便于以后轮换主密钥。
func (c *AESGCM) Version() int {
	return c.version
}

func (c *AESGCM) Encrypt(plaintext string, aad []byte) ([]byte, []byte, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, fmt.Errorf("生成 nonce: %w", err)
	}
	ciphertext := c.aead.Seal(nil, nonce, []byte(plaintext), aad)
	return ciphertext, nonce, nil
}

func (c *AESGCM) Decrypt(ciphertext, nonce, aad []byte) (string, error) {
	plaintext, err := c.aead.Open(nil, nonce, ciphertext, aad)
	if err != nil {
		return "", errors.New("解密用户 Key 失败")
	}
	return string(plaintext), nil
}

// Mask 只保留少量字符用于展示，绝不返回完整 Key。
func Mask(key string) string {
	if len(key) <= 8 {
		return "****"
	}
	if len(key) <= 12 {
		return key[:2] + "..." + key[len(key)-2:]
	}
	return key[:3] + "..." + key[len(key)-4:]
}
