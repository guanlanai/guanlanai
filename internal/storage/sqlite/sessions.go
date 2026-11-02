package sqlite

import (
	"context"
	"fmt"
	"time"

	"github.com/guanlanai/guanlanai/internal/session"
)

// GetOrCreate 返回 chat 的会话；不存在时创建。
func (s *Store) GetOrCreateSession(ctx context.Context, userID, chatID int64) (*session.Session, error) {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO sessions (user_id, chat_id)
VALUES (?, ?)
ON CONFLICT(chat_id) DO UPDATE SET last_active_at = CURRENT_TIMESTAMP`,
		userID, chatID,
	)
	if err != nil {
		return nil, fmt.Errorf("创建或更新会话: %w", err)
	}
	return s.getSessionByChatID(ctx, chatID)
}

func (s *Store) getSessionByChatID(ctx context.Context, chatID int64) (*session.Session, error) {
	var result session.Session
	err := s.db.QueryRowContext(ctx, `
SELECT id, user_id, chat_id, COALESCE(current_symbol, ''), COALESCE(current_market, ''),
       COALESCE(summary, ''), summary_tokens, COALESCE(last_analysis_json, ''),
       generation, last_active_at
FROM sessions
WHERE chat_id = ?`, chatID).Scan(
		&result.ID,
		&result.UserID,
		&result.ChatID,
		&result.CurrentSymbol,
		&result.CurrentMarket,
		&result.Summary,
		&result.SummaryTokens,
		&result.LastAnalysisJSON,
		&result.Generation,
		&result.LastActiveAt,
	)
	if err != nil {
		return nil, fmt.Errorf("读取会话: %w", err)
	}
	return &result, nil
}

// AppendMessage 追加一条会话消息。
func (s *Store) AppendMessage(ctx context.Context, sessionID int64, role, content string, tokens int) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO messages (session_id, role, content, tokens)
VALUES (?, ?, ?, ?)`, sessionID, role, content, tokens)
	if err != nil {
		return fmt.Errorf("保存会话消息: %w", err)
	}
	_, _ = s.db.ExecContext(ctx, `UPDATE sessions SET last_active_at = CURRENT_TIMESTAMP WHERE id = ?`, sessionID)
	return nil
}

// RecentMessages 返回最近的消息，按时间正序。
func (s *Store) RecentMessages(ctx context.Context, sessionID int64, limit int) ([]session.Message, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id, role, content, tokens, created_at
FROM messages
WHERE session_id = ?
ORDER BY id DESC
LIMIT ?`, sessionID, limit)
	if err != nil {
		return nil, fmt.Errorf("读取会话消息: %w", err)
	}
	defer rows.Close()

	var messages []session.Message
	for rows.Next() {
		var message session.Message
		if err := rows.Scan(&message.ID, &message.Role, &message.Content, &message.Tokens, &message.CreatedAt); err != nil {
			return nil, fmt.Errorf("扫描会话消息: %w", err)
		}
		messages = append(messages, message)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历会话消息: %w", err)
	}
	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
	}
	return messages, nil
}

// SaveAnalysis 保存最近一次分析结果。
func (s *Store) SaveAnalysis(ctx context.Context, sessionID int64, symbol, market, analysisJSON string) (*session.Session, error) {
	result, err := s.db.ExecContext(ctx, `
UPDATE sessions
SET current_symbol = ?, current_market = ?, last_analysis_json = ?,
    last_active_at = CURRENT_TIMESTAMP
WHERE id = ?`, symbol, market, analysisJSON, sessionID)
	if err != nil {
		return nil, fmt.Errorf("保存分析上下文: %w", err)
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return nil, fmt.Errorf("会话不存在")
	}
	return s.getSessionByID(ctx, sessionID)
}

func (s *Store) getSessionByID(ctx context.Context, sessionID int64) (*session.Session, error) {
	var chatID int64
	if err := s.db.QueryRowContext(ctx, `SELECT chat_id FROM sessions WHERE id = ?`, sessionID).Scan(&chatID); err != nil {
		return nil, fmt.Errorf("读取会话 chat_id: %w", err)
	}
	return s.getSessionByChatID(ctx, chatID)
}

// UpdateSummary 保存压缩摘要。
func (s *Store) UpdateSummary(ctx context.Context, sessionID int64, summary string, tokens int) error {
	_, err := s.db.ExecContext(ctx, `
UPDATE sessions
SET summary = ?, summary_tokens = ?, last_active_at = CURRENT_TIMESTAMP
WHERE id = ?`, summary, tokens, sessionID)
	if err != nil {
		return fmt.Errorf("保存会话摘要: %w", err)
	}
	return nil
}

// TrimMessages 只保留最近 keep 条消息。
func (s *Store) TrimMessages(ctx context.Context, sessionID int64, keep int) error {
	if keep < 0 {
		keep = 0
	}
	_, err := s.db.ExecContext(ctx, `
DELETE FROM messages
WHERE session_id = ?
  AND id NOT IN (
      SELECT id FROM messages WHERE session_id = ? ORDER BY id DESC LIMIT ?
  )`, sessionID, sessionID, keep)
	if err != nil {
		return fmt.Errorf("裁剪会话消息: %w", err)
	}
	return nil
}

// Reset 清空会话上下文并递增 generation。
func (s *Store) Reset(ctx context.Context, userID, chatID int64) (*session.Session, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("开始重置会话: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE sessions
SET current_symbol = NULL, current_market = NULL, summary = NULL,
    summary_tokens = 0, last_analysis_json = NULL,
    generation = generation + 1, last_active_at = CURRENT_TIMESTAMP
WHERE user_id = ? AND chat_id = ?`, userID, chatID); err != nil {
		_ = tx.Rollback()
		return nil, fmt.Errorf("重置会话: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
DELETE FROM messages
WHERE session_id IN (SELECT id FROM sessions WHERE user_id = ? AND chat_id = ?)`,
		userID, chatID); err != nil {
		_ = tx.Rollback()
		return nil, fmt.Errorf("清空会话消息: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("提交重置会话: %w", err)
	}
	return s.getSessionByChatID(ctx, chatID)
}

// CleanupMessages 删除超过保留期的消息。
func (s *Store) CleanupMessages(ctx context.Context, before time.Time) (int64, error) {
	result, err := s.db.ExecContext(ctx, `DELETE FROM messages WHERE created_at < ?`, before)
	if err != nil {
		return 0, fmt.Errorf("清理过期消息: %w", err)
	}
	return result.RowsAffected()
}

var _ session.Store = (*Store)(nil)
