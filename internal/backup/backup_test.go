package backup

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/guanlanai/guanlanai/internal/storage/sqlite"
	"github.com/guanlanai/guanlanai/migrations"
)

func TestCreate(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "source.db")
	db, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if err := sqlite.Migrate(context.Background(), db, migrations.FS); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	store := sqlite.NewStore(db)
	if _, err := store.GetOrCreate(context.Background(), 1, "alice", "https://api.openai.com/v1", "swing"); err != nil {
		t.Fatalf("GetOrCreate() error = %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	target, err := Create(context.Background(), dbPath, filepath.Join(dir, "backups"))
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	backupDB, err := sqlite.Open(target)
	if err != nil {
		t.Fatalf("打开备份 error = %v", err)
	}
	defer backupDB.Close()

	var count int
	if err := backupDB.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&count); err != nil {
		t.Fatalf("查询备份 error = %v", err)
	}
	if count != 1 {
		t.Fatalf("users = %d, want 1", count)
	}
}
