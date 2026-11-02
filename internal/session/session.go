package session

import (
	"context"
	"time"
)

// Session 是单个 Telegram chat 的分析上下文。
type Session struct {
	ID               int64
	UserID           int64
	ChatID           int64
	CurrentSymbol    string
	CurrentMarket    string
	Summary          string
	SummaryTokens    int
	LastAnalysisJSON string
	Generation       int64
	LastActiveAt     time.Time
}

// Message 是会话中的一条消息。
type Message struct {
	ID        int64
	Role      string
	Content   string
	Tokens    int
	CreatedAt time.Time
}

// Store 是会话持久化接口。
type Store interface {
	GetOrCreateSession(ctx context.Context, userID, chatID int64) (*Session, error)
	AppendMessage(ctx context.Context, sessionID int64, role, content string, tokens int) error
	RecentMessages(ctx context.Context, sessionID int64, limit int) ([]Message, error)
	SaveAnalysis(ctx context.Context, sessionID int64, symbol, market, analysisJSON string) (*Session, error)
	UpdateSummary(ctx context.Context, sessionID int64, summary string, tokens int) error
	TrimMessages(ctx context.Context, sessionID int64, keep int) error
	Reset(ctx context.Context, userID, chatID int64) (*Session, error)
	CleanupMessages(ctx context.Context, before time.Time) (int64, error)
}
