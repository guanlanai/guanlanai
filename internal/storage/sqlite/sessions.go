package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/guanlanai/guanlanai/internal/session"
)

const sessionColumns = `id, user_id, chat_id, scope,
       COALESCE(title, ''), COALESCE(current_symbol, ''), COALESCE(current_market, ''),
       COALESCE(summary, ''), summary_tokens, COALESCE(last_analysis_json, ''),
       generation, created_at, last_active_at`

// GetOrCreateActivePrivate 返回私聊当前 active 会话；没有时回退到最近一条或创建默认会话。
func (s *Store) GetOrCreateActivePrivate(ctx context.Context, userID, chatID int64) (*session.Session, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("开始读取私聊会话: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var sessionID int64
	err = tx.QueryRowContext(ctx, `
SELECT s.id
FROM chat_state c
JOIN sessions s ON s.id = c.active_session_id
WHERE c.chat_id = ? AND s.user_id = ? AND s.chat_id = ? AND s.scope = 'private'`,
		chatID, userID, chatID).Scan(&sessionID)
	if errors.Is(err, sql.ErrNoRows) {
		err = tx.QueryRowContext(ctx, `
SELECT id FROM sessions
WHERE user_id = ? AND chat_id = ? AND scope = 'private'
ORDER BY last_active_at DESC, id DESC
LIMIT 1`, userID, chatID).Scan(&sessionID)
		if errors.Is(err, sql.ErrNoRows) {
			result, insertErr := tx.ExecContext(ctx, `
INSERT INTO sessions (user_id, chat_id, scope, title)
VALUES (?, ?, 'private', ?)`, userID, chatID, defaultTitle(1))
			if insertErr != nil {
				return nil, fmt.Errorf("创建默认私聊会话: %w", insertErr)
			}
			sessionID, err = result.LastInsertId()
			if err != nil {
				return nil, fmt.Errorf("读取新会话 ID: %w", err)
			}
		} else if err != nil {
			return nil, fmt.Errorf("读取最近私聊会话: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO chat_state (chat_id, active_session_id)
VALUES (?, ?)
ON CONFLICT(chat_id) DO UPDATE SET
    active_session_id = excluded.active_session_id,
    updated_at = CURRENT_TIMESTAMP`, chatID, sessionID); err != nil {
			return nil, fmt.Errorf("更新私聊 active 会话: %w", err)
		}
	} else if err != nil {
		return nil, fmt.Errorf("读取私聊 active 会话: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("提交私聊会话读取: %w", err)
	}
	return s.GetByID(ctx, sessionID)
}

// CreatePrivate 新建私聊会话并设为 active。
func (s *Store) CreatePrivate(ctx context.Context, userID, chatID int64, title string) (*session.Session, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("开始创建私聊会话: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var count int
	if err := tx.QueryRowContext(ctx, `
SELECT COUNT(*) FROM sessions WHERE user_id = ? AND chat_id = ? AND scope = 'private'`,
		userID, chatID).Scan(&count); err != nil {
		return nil, fmt.Errorf("统计私聊会话: %w", err)
	}
	if count >= s.maxPrivateSessions {
		return nil, session.ErrLimitReached
	}

	title = normalizeTitle(title)
	if title == "" {
		title = defaultTitle(count + 1)
	}
	result, err := tx.ExecContext(ctx, `
INSERT INTO sessions (user_id, chat_id, scope, title)
VALUES (?, ?, 'private', ?)`, userID, chatID, title)
	if err != nil {
		return nil, fmt.Errorf("创建私聊会话: %w", err)
	}
	sessionID, err := result.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("读取新会话 ID: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO chat_state (chat_id, active_session_id)
VALUES (?, ?)
ON CONFLICT(chat_id) DO UPDATE SET
    active_session_id = excluded.active_session_id,
    updated_at = CURRENT_TIMESTAMP`, chatID, sessionID); err != nil {
		return nil, fmt.Errorf("设置私聊 active 会话: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("提交私聊会话创建: %w", err)
	}
	return s.GetByID(ctx, sessionID)
}

// ListPrivate 按最近活跃时间倒序列出私聊会话。
func (s *Store) ListPrivate(ctx context.Context, userID, chatID int64) ([]session.Session, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT `+sessionColumns+`
FROM sessions
WHERE user_id = ? AND chat_id = ? AND scope = 'private'
ORDER BY last_active_at DESC, id DESC`, userID, chatID)
	if err != nil {
		return nil, fmt.Errorf("列出私聊会话: %w", err)
	}
	defer rows.Close()

	var sessions []session.Session
	for rows.Next() {
		sess, scanErr := scanSession(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		sessions = append(sessions, *sess)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历私聊会话: %w", err)
	}
	return sessions, nil
}

// GetPrivate 按 user + chat + private 范围读取会话，防止越权访问。
func (s *Store) GetPrivate(ctx context.Context, userID, chatID, sessionID int64) (*session.Session, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT `+sessionColumns+`
FROM sessions
WHERE id = ? AND user_id = ? AND chat_id = ? AND scope = 'private'`,
		sessionID, userID, chatID)
	return scanSessionOrNotFound(row, "读取私聊会话")
}

// SetActivePrivate 切换私聊 active 会话。
func (s *Store) SetActivePrivate(ctx context.Context, userID, chatID, sessionID int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开始切换私聊会话: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var found int64
	err = tx.QueryRowContext(ctx, `
SELECT id FROM sessions
WHERE id = ? AND user_id = ? AND chat_id = ? AND scope = 'private'`,
		sessionID, userID, chatID).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return session.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("校验私聊会话归属: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE sessions SET last_active_at = CURRENT_TIMESTAMP WHERE id = ?`, sessionID); err != nil {
		return fmt.Errorf("更新私聊会话活跃时间: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO chat_state (chat_id, active_session_id)
VALUES (?, ?)
ON CONFLICT(chat_id) DO UPDATE SET
    active_session_id = excluded.active_session_id,
    updated_at = CURRENT_TIMESTAMP`, chatID, sessionID); err != nil {
		return fmt.Errorf("保存私聊 active 会话: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交私聊会话切换: %w", err)
	}
	return nil
}

// RenamePrivate 重命名私聊会话。
func (s *Store) RenamePrivate(ctx context.Context, userID, chatID, sessionID int64, title string) error {
	title = normalizeTitle(title)
	if title == "" {
		return session.ErrTitleRequired
	}
	result, err := s.db.ExecContext(ctx, `
UPDATE sessions SET title = ?
WHERE id = ? AND user_id = ? AND chat_id = ? AND scope = 'private'`,
		title, sessionID, userID, chatID)
	if err != nil {
		return fmt.Errorf("重命名私聊会话: %w", err)
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return session.ErrNotFound
	}
	return nil
}

// DeletePrivate 删除私聊会话；至少保留一条，删除 active 时自动切到最近一条。
func (s *Store) DeletePrivate(ctx context.Context, userID, chatID, sessionID int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开始删除私聊会话: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var owned int64
	err = tx.QueryRowContext(ctx, `
SELECT id FROM sessions
WHERE id = ? AND user_id = ? AND chat_id = ? AND scope = 'private'`,
		sessionID, userID, chatID).Scan(&owned)
	if errors.Is(err, sql.ErrNoRows) {
		return session.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("校验私聊会话归属: %w", err)
	}

	var count int
	if err := tx.QueryRowContext(ctx, `
SELECT COUNT(*) FROM sessions WHERE user_id = ? AND chat_id = ? AND scope = 'private'`,
		userID, chatID).Scan(&count); err != nil {
		return fmt.Errorf("统计私聊会话: %w", err)
	}
	if count <= 1 {
		return session.ErrLastSession
	}

	var activeID int64
	err = tx.QueryRowContext(ctx, `SELECT active_session_id FROM chat_state WHERE chat_id = ?`, chatID).Scan(&activeID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("读取私聊 active 会话: %w", err)
	}

	result, err := tx.ExecContext(ctx, `
DELETE FROM sessions
WHERE id = ? AND user_id = ? AND chat_id = ? AND scope = 'private'`,
		sessionID, userID, chatID)
	if err != nil {
		return fmt.Errorf("删除私聊会话: %w", err)
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return session.ErrNotFound
	}

	if activeID == sessionID || activeID == 0 {
		var nextID int64
		if err := tx.QueryRowContext(ctx, `
SELECT id FROM sessions
WHERE user_id = ? AND chat_id = ? AND scope = 'private'
ORDER BY last_active_at DESC, id DESC
LIMIT 1`, userID, chatID).Scan(&nextID); err != nil {
			return fmt.Errorf("选择新的私聊 active 会话: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO chat_state (chat_id, active_session_id)
VALUES (?, ?)
ON CONFLICT(chat_id) DO UPDATE SET
    active_session_id = excluded.active_session_id,
    updated_at = CURRENT_TIMESTAMP`, chatID, nextID); err != nil {
			return fmt.Errorf("更新私聊 active 会话: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交私聊会话删除: %w", err)
	}
	return nil
}

// GetOrCreateGroup 返回成员在群里的唯一会话。
func (s *Store) GetOrCreateGroup(ctx context.Context, userID, chatID int64) (*session.Session, error) {
	if _, err := s.db.ExecContext(ctx, `
INSERT OR IGNORE INTO sessions (user_id, chat_id, scope, title)
VALUES (?, ?, 'group', '群会话')`, userID, chatID); err != nil {
		return nil, fmt.Errorf("创建群会话: %w", err)
	}
	row := s.db.QueryRowContext(ctx, `
SELECT `+sessionColumns+`
FROM sessions
WHERE user_id = ? AND chat_id = ? AND scope = 'group'`, userID, chatID)
	return scanSessionOrNotFound(row, "读取群会话")
}

// GetByID 按 ID 读取会话，用于在途分析的写回。
func (s *Store) GetByID(ctx context.Context, sessionID int64) (*session.Session, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT `+sessionColumns+` FROM sessions WHERE id = ?`, sessionID)
	return scanSessionOrNotFound(row, "读取会话")
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
		return nil, session.ErrNotFound
	}
	return s.GetByID(ctx, sessionID)
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

// Reset 清空指定会话上下文并递增 generation；不影响在途分析。
func (s *Store) Reset(ctx context.Context, sessionID int64) (*session.Session, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("开始重置会话: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	result, err := tx.ExecContext(ctx, `
UPDATE sessions
SET current_symbol = NULL, current_market = NULL, summary = NULL,
    summary_tokens = 0, last_analysis_json = NULL,
    generation = generation + 1, last_active_at = CURRENT_TIMESTAMP
WHERE id = ?`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("重置会话: %w", err)
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return nil, session.ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM messages WHERE session_id = ?`, sessionID); err != nil {
		return nil, fmt.Errorf("清空会话消息: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("提交重置会话: %w", err)
	}
	return s.GetByID(ctx, sessionID)
}

// CleanupMessages 删除超过保留期的消息。
func (s *Store) CleanupMessages(ctx context.Context, before time.Time) (int64, error) {
	result, err := s.db.ExecContext(ctx, `DELETE FROM messages WHERE created_at < ?`, before)
	if err != nil {
		return 0, fmt.Errorf("清理过期消息: %w", err)
	}
	return result.RowsAffected()
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanSession(row rowScanner) (*session.Session, error) {
	var (
		result session.Session
		scope  string
	)
	if err := row.Scan(
		&result.ID,
		&result.UserID,
		&result.ChatID,
		&scope,
		&result.Title,
		&result.CurrentSymbol,
		&result.CurrentMarket,
		&result.Summary,
		&result.SummaryTokens,
		&result.LastAnalysisJSON,
		&result.Generation,
		&result.CreatedAt,
		&result.LastActiveAt,
	); err != nil {
		return nil, err
	}
	result.Scope = session.Scope(scope)
	return &result, nil
}

func scanSessionOrNotFound(row rowScanner, action string) (*session.Session, error) {
	result, err := scanSession(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, session.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", action, err)
	}
	return result, nil
}

func defaultTitle(index int) string {
	return "会话 " + strconv.Itoa(index)
}

func normalizeTitle(title string) string {
	title = strings.Join(strings.Fields(title), " ")
	runes := []rune(title)
	if len(runes) > session.MaxTitleLength {
		title = string(runes[:session.MaxTitleLength])
	}
	return title
}

var _ session.Store = (*Store)(nil)
