package sqlite

import (
	"context"
	"io/fs"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/guanlanai/guanlanai/migrations"
)

func TestMigrate(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ctx := context.Background()
	if err := Migrate(ctx, db, migrations.FS); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	var tableCount int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name IN ('users', 'sessions', 'messages', 'usage_events', 'chat_state')`,
	).Scan(&tableCount); err != nil {
		t.Fatalf("查询表失败: %v", err)
	}
	if tableCount != 5 {
		t.Fatalf("业务表数量 = %d, want 5", tableCount)
	}

	var migrationCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&migrationCount); err != nil {
		t.Fatalf("查询迁移记录失败: %v", err)
	}
	if migrationCount != 2 {
		t.Fatalf("迁移记录数 = %d, want 2", migrationCount)
	}

	if err := Migrate(ctx, db, migrations.FS); err != nil {
		t.Fatalf("重复 Migrate() error = %v", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&migrationCount); err != nil {
		t.Fatalf("重复查询迁移记录失败: %v", err)
	}
	if migrationCount != 2 {
		t.Fatalf("重复迁移后记录数 = %d, want 2", migrationCount)
	}
}

func TestMigrate0002PreservesPrivateData(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()

	body, err := fs.ReadFile(migrations.FS, "0001_init.sql")
	if err != nil {
		t.Fatalf("读取 0001: %v", err)
	}
	firstOnly := fstest.MapFS{
		"0001_init.sql": &fstest.MapFile{Data: body},
	}
	if err := Migrate(ctx, db, firstOnly); err != nil {
		t.Fatalf("执行 0001: %v", err)
	}

	if _, err := db.ExecContext(ctx,
		`INSERT INTO users (telegram_user_id, username) VALUES (123, 'alice')`); err != nil {
		t.Fatalf("插入用户: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
INSERT INTO sessions (user_id, chat_id, current_symbol, current_market, summary, summary_tokens, last_analysis_json, generation)
VALUES (1, 456, 'BTC', 'crypto', 'old summary', 12, '{"opinion":"bullish"}', 3)`); err != nil {
		t.Fatalf("插入会话: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
INSERT INTO messages (session_id, role, content, tokens) VALUES (1, 'user', '分析 BTC', 5)`); err != nil {
		t.Fatalf("插入消息: %v", err)
	}

	if err := Migrate(ctx, db, migrations.FS); err != nil {
		t.Fatalf("执行 0002: %v", err)
	}

	var (
		scope      string
		title      string
		symbol     string
		summary    string
		generation int
	)
	if err := db.QueryRowContext(ctx, `
SELECT scope, title, current_symbol, summary, generation FROM sessions WHERE id = 1`,
	).Scan(&scope, &title, &symbol, &summary, &generation); err != nil {
		t.Fatalf("读取迁移后的会话: %v", err)
	}
	if scope != "private" || title != "BTC" || symbol != "BTC" || summary != "old summary" || generation != 3 {
		t.Fatalf("迁移后会话 = scope=%q title=%q symbol=%q summary=%q generation=%d",
			scope, title, symbol, summary, generation)
	}

	var (
		messageID int64
		content   string
	)
	if err := db.QueryRowContext(ctx, `SELECT id, content FROM messages WHERE session_id = 1`).Scan(&messageID, &content); err != nil {
		t.Fatalf("读取迁移后的消息: %v", err)
	}
	if messageID != 1 || content != "分析 BTC" {
		t.Fatalf("迁移后消息 = %d %q", messageID, content)
	}

	var activeID int64
	if err := db.QueryRowContext(ctx, `SELECT active_session_id FROM chat_state WHERE chat_id = 456`).Scan(&activeID); err != nil {
		t.Fatalf("读取 chat_state: %v", err)
	}
	if activeID != 1 {
		t.Fatalf("active_session_id = %d, want 1", activeID)
	}

	// 群组 partial unique index 生效。
	if _, err := db.ExecContext(ctx, `
INSERT INTO sessions (user_id, chat_id, scope, title) VALUES (1, 900, 'group', 'g')`); err != nil {
		t.Fatalf("插入群会话: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
INSERT INTO sessions (user_id, chat_id, scope, title) VALUES (1, 900, 'group', 'g2')`); err == nil {
		t.Fatal("重复的群会话应被 partial unique index 拒绝")
	}
}
