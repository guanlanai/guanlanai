package backup

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/guanlanai/guanlanai/internal/storage/sqlite"
)

// Create 使用 SQLite VACUUM INTO 生成一致性备份。
func Create(ctx context.Context, dbPath, outDir string) (string, error) {
	if _, err := os.Stat(dbPath); err != nil {
		return "", fmt.Errorf("数据库不存在: %w", err)
	}
	if err := os.MkdirAll(outDir, 0o700); err != nil {
		return "", fmt.Errorf("创建备份目录: %w", err)
	}

	absOut, err := filepath.Abs(outDir)
	if err != nil {
		return "", fmt.Errorf("解析备份目录: %w", err)
	}
	name := "guanlanai-" + time.Now().UTC().Format("20060102T150405Z") + ".db"
	target := filepath.Join(absOut, name)

	db, err := sqlite.Open(dbPath)
	if err != nil {
		return "", fmt.Errorf("打开数据库: %w", err)
	}
	defer db.Close()

	quoted := strings.ReplaceAll(target, "'", "''")
	if _, err := db.ExecContext(ctx, "VACUUM INTO '"+quoted+"'"); err != nil {
		_ = os.Remove(target)
		return "", fmt.Errorf("生成备份: %w", err)
	}
	if err := os.Chmod(target, 0o600); err != nil {
		_ = os.Remove(target)
		return "", fmt.Errorf("设置备份权限: %w", err)
	}
	return target, nil
}
