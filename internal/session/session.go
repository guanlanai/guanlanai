package session

import (
	"context"
	"errors"
	"time"
)

// Scope 区分私聊会话和群组会话。
type Scope string

const (
	// ScopePrivate 表示私聊会话；同一个 user/chat 可以有多条，只有一条 active。
	ScopePrivate Scope = "private"
	// ScopeGroup 表示群组会话；同一个用户在同一个群固定只有一条。
	ScopeGroup Scope = "group"
)

// MaxPrivateSessions 是一个用户在一个私聊 chat 中允许保留的最大会话数。
const MaxPrivateSessions = 20

// MaxTitleLength 是会话标题允许的最大字符数。
const MaxTitleLength = 40

// 会话操作的可预期错误，供上层转换为用户提示。
var (
	ErrNotFound      = errors.New("会话不存在")
	ErrLimitReached  = errors.New("会话数量已达上限")
	ErrLastSession   = errors.New("不能删除最后一个私聊会话")
	ErrTitleRequired = errors.New("会话标题不能为空")
)

// Session 是单个 Telegram chat 中的分析上下文。
// 私聊 chat 中可以存在多条；群组中每个成员一条。
type Session struct {
	ID               int64
	UserID           int64
	ChatID           int64
	Scope            Scope
	Title            string
	CurrentSymbol    string
	CurrentMarket    string
	CurrentTimeframe string
	Summary          string
	SummaryTokens    int
	LastAnalysisJSON string
	Generation       int64
	CreatedAt        time.Time
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
	// 私聊
	GetOrCreateActivePrivate(ctx context.Context, userID, chatID int64) (*Session, error)
	CreatePrivate(ctx context.Context, userID, chatID int64, title string) (*Session, error)
	ListPrivate(ctx context.Context, userID, chatID int64) ([]Session, error)
	GetPrivate(ctx context.Context, userID, chatID, sessionID int64) (*Session, error)
	SetActivePrivate(ctx context.Context, userID, chatID, sessionID int64) error
	RenamePrivate(ctx context.Context, userID, chatID, sessionID int64, title string) error
	DeletePrivate(ctx context.Context, userID, chatID, sessionID int64) error

	// 群组
	GetOrCreateGroup(ctx context.Context, userID, chatID int64) (*Session, error)

	// 通用
	GetByID(ctx context.Context, sessionID int64) (*Session, error)
	AppendMessage(ctx context.Context, sessionID int64, role, content string, tokens int) error
	RecentMessages(ctx context.Context, sessionID int64, limit int) ([]Message, error)
	SaveAnalysis(ctx context.Context, sessionID int64, symbol, market, timeframe, analysisJSON string) (*Session, error)
	UpdateSummary(ctx context.Context, sessionID int64, summary string, tokens int) error
	TrimMessages(ctx context.Context, sessionID int64, keep int) error
	Reset(ctx context.Context, sessionID int64) (*Session, error)
	CleanupMessages(ctx context.Context, before time.Time) (int64, error)
}
