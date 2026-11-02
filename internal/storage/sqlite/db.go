package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// Open 打开 SQLite 数据库并设置适合单机部署的 PRAGMA。
// MVP 使用单写连接，避免 SQLITE_BUSY 和并发写冲突。
func Open(path string) (*sql.DB, error) {
	if path == "" {
		return nil, fmt.Errorf("数据库路径不能为空")
	}

	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("解析数据库路径: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o700); err != nil {
		return nil, fmt.Errorf("创建数据库目录: %w", err)
	}

	u := url.URL{Scheme: "file", Path: abs}
	dsn := u.String() + "?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)"

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开 SQLite: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("连接 SQLite: %w", err)
	}

	// 兼容已有数据库：启动时收紧权限，避免历史文件保持 0644。
	for _, path := range []string{abs, abs + "-wal", abs + "-shm"} {
		if err := os.Chmod(path, 0o600); err != nil && !errors.Is(err, os.ErrNotExist) {
			_ = db.Close()
			return nil, fmt.Errorf("收紧数据库文件权限 %s: %w", path, err)
		}
	}
	return db, nil
}
