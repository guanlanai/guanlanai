package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/guanlanai/guanlanai/internal/user"
)

// GetOrCreate 返回用户配置；不存在时创建。
func (s *Store) GetOrCreate(ctx context.Context, telegramUserID int64, username, defaultBaseURL, defaultMode string) (*user.User, error) {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO users (telegram_user_id, username, api_base_url, mode)
VALUES (?, ?, ?, ?)
ON CONFLICT(telegram_user_id) DO UPDATE SET
    username = excluded.username,
    updated_at = CURRENT_TIMESTAMP`,
		telegramUserID, nullableString(username), defaultBaseURL, defaultMode,
	)
	if err != nil {
		return nil, fmt.Errorf("创建或更新用户: %w", err)
	}
	return s.getByTelegramID(ctx, telegramUserID)
}

func (s *Store) getByTelegramID(ctx context.Context, telegramUserID int64) (*user.User, error) {
	var (
		result     user.User
		username   sql.NullString
		ciphertext []byte
		nonce      []byte
		keyVersion sql.NullInt64
		model      sql.NullString
	)
	err := s.db.QueryRowContext(ctx, `
SELECT id, telegram_user_id, username, key_ciphertext, key_nonce, key_version,
       key_status, api_base_url, model, mode, status
FROM users
WHERE telegram_user_id = ?`, telegramUserID).Scan(
		&result.ID,
		&result.TelegramUserID,
		&username,
		&ciphertext,
		&nonce,
		&keyVersion,
		&result.KeyStatus,
		&result.APIBaseURL,
		&model,
		&result.Mode,
		&result.Status,
	)
	if err != nil {
		return nil, fmt.Errorf("读取用户: %w", err)
	}

	result.Username = username.String
	result.KeyCiphertext = ciphertext
	result.KeyNonce = nonce
	if keyVersion.Valid {
		result.KeyVersion = int(keyVersion.Int64)
	}
	result.Model = model.String
	return &result, nil
}

// UpdateKey 保存加密后的 API Key。
func (s *Store) UpdateKey(ctx context.Context, telegramUserID int64, ciphertext, nonce []byte, keyVersion int) error {
	_, err := s.db.ExecContext(ctx, `
UPDATE users
SET key_ciphertext = ?, key_nonce = ?, key_version = ?,
    key_status = 'valid', key_updated_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP
WHERE telegram_user_id = ?`,
		ciphertext, nonce, keyVersion, telegramUserID,
	)
	if err != nil {
		return fmt.Errorf("保存用户 Key: %w", err)
	}
	return nil
}

// DeleteKey 删除用户 API Key。
func (s *Store) DeleteKey(ctx context.Context, telegramUserID int64) error {
	_, err := s.db.ExecContext(ctx, `
UPDATE users
SET key_ciphertext = NULL, key_nonce = NULL, key_version = NULL,
    key_status = 'missing', key_updated_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP
WHERE telegram_user_id = ?`, telegramUserID)
	if err != nil {
		return fmt.Errorf("删除用户 Key: %w", err)
	}
	return nil
}

// UpdateBaseURL 保存用户自定义 OpenAI 兼容地址。
func (s *Store) UpdateBaseURL(ctx context.Context, telegramUserID int64, baseURL string) error {
	_, err := s.db.ExecContext(ctx, `
UPDATE users
SET api_base_url = ?, api_base_url_updated_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP
WHERE telegram_user_id = ?`, baseURL, telegramUserID)
	if err != nil {
		return fmt.Errorf("保存 base_url: %w", err)
	}
	return nil
}

// UpdateModel 保存用户选择的模型。
func (s *Store) UpdateModel(ctx context.Context, telegramUserID int64, model string) error {
	_, err := s.db.ExecContext(ctx, `
UPDATE users
SET model = ?, updated_at = CURRENT_TIMESTAMP
WHERE telegram_user_id = ?`, nullableString(model), telegramUserID)
	if err != nil {
		return fmt.Errorf("保存模型: %w", err)
	}
	return nil
}

// UpdateMode 保存用户分析模式。
func (s *Store) UpdateMode(ctx context.Context, telegramUserID int64, mode string) error {
	_, err := s.db.ExecContext(ctx, `
UPDATE users
SET mode = ?, updated_at = CURRENT_TIMESTAMP
WHERE telegram_user_id = ?`, mode, telegramUserID)
	if err != nil {
		return fmt.Errorf("保存模式: %w", err)
	}
	return nil
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
