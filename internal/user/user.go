package user

import "context"

// User 是 Telegram 用户及其分析配置。
type User struct {
	ID             int64
	TelegramUserID int64
	Username       string
	KeyCiphertext  []byte
	KeyNonce       []byte
	KeyVersion     int
	KeyStatus      string
	APIBaseURL     string
	Model          string
	Mode           string
	Status         string
}

// HasKey 表示用户是否已保存 API Key。
func (u *User) HasKey() bool {
	return len(u.KeyCiphertext) > 0 && len(u.KeyNonce) > 0
}

// Store 是用户配置的持久化接口。
type Store interface {
	GetOrCreate(ctx context.Context, telegramUserID int64, username, defaultBaseURL, defaultMode string) (*User, error)
	UpdateKey(ctx context.Context, telegramUserID int64, ciphertext, nonce []byte, keyVersion int) error
	DeleteKey(ctx context.Context, telegramUserID int64) error
	UpdateBaseURL(ctx context.Context, telegramUserID int64, baseURL string) error
	UpdateModel(ctx context.Context, telegramUserID int64, model string) error
	UpdateMode(ctx context.Context, telegramUserID int64, mode string) error
}
