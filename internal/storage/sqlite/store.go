package sqlite

import (
	"database/sql"

	"github.com/guanlanai/guanlanai/internal/session"
)

// Store 提供 SQLite 上的用户配置和会话读写。
type Store struct {
	db                 *sql.DB
	maxPrivateSessions int
}

// Option 用于调整 Store 的可选行为。
type Option func(*Store)

// WithMaxPrivateSessions 覆盖每个私聊 chat 的会话上限。
func WithMaxPrivateSessions(limit int) Option {
	return func(s *Store) {
		if limit > 0 {
			s.maxPrivateSessions = limit
		}
	}
}

// NewStore 创建存储。
func NewStore(db *sql.DB, opts ...Option) *Store {
	s := &Store{
		db:                 db,
		maxPrivateSessions: session.MaxPrivateSessions,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}
