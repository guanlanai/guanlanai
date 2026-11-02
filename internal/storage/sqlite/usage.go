package sqlite

import (
	"context"
	"fmt"

	"github.com/guanlanai/guanlanai/internal/usage"
)

// Record 写入一次 LLM 用量。
func (s *Store) Record(ctx context.Context, event usage.Event) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO usage_events
    (user_id, request_id, kind, model, prompt_tokens, completion_tokens, latency_ms, status)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		event.UserID, event.RequestID, event.Kind, event.Model,
		event.PromptTokens, event.CompletionTokens, event.LatencyMS, event.Status,
	)
	if err != nil {
		return fmt.Errorf("保存用量: %w", err)
	}
	return nil
}

// Today 返回用户当天用量汇总。
func (s *Store) Today(ctx context.Context, userID int64) (usage.Summary, error) {
	var result usage.Summary
	err := s.db.QueryRowContext(ctx, `
SELECT COUNT(*),
       COALESCE(SUM(prompt_tokens), 0),
       COALESCE(SUM(completion_tokens), 0)
FROM usage_events
WHERE user_id = ? AND created_at >= datetime('now', 'start of day')`,
		userID,
	).Scan(&result.Requests, &result.PromptTokens, &result.CompletionTokens)
	if err != nil {
		return usage.Summary{}, fmt.Errorf("读取今日用量: %w", err)
	}
	return result, nil
}

var _ usage.Store = (*Store)(nil)
