package sqlite

import "database/sql"

// Store 提供 SQLite 上的用户配置读写。
type Store struct {
	db *sql.DB
}

// NewStore 创建用户存储。
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}
