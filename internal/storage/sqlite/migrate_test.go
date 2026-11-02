package sqlite

import (
	"context"
	"path/filepath"
	"testing"

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
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name IN ('users', 'sessions', 'messages', 'usage_events')`,
	).Scan(&tableCount); err != nil {
		t.Fatalf("查询表失败: %v", err)
	}
	if tableCount != 4 {
		t.Fatalf("业务表数量 = %d, want 4", tableCount)
	}

	var migrationCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&migrationCount); err != nil {
		t.Fatalf("查询迁移记录失败: %v", err)
	}
	if migrationCount != 1 {
		t.Fatalf("迁移记录数 = %d, want 1", migrationCount)
	}

	if err := Migrate(ctx, db, migrations.FS); err != nil {
		t.Fatalf("重复 Migrate() error = %v", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&migrationCount); err != nil {
		t.Fatalf("重复查询迁移记录失败: %v", err)
	}
	if migrationCount != 1 {
		t.Fatalf("重复迁移后记录数 = %d, want 1", migrationCount)
	}
}
