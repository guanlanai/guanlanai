package sqlite

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/guanlanai/guanlanai/migrations"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := Migrate(context.Background(), db, migrations.FS); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	return NewStore(db)
}

func TestUserLifecycle(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	usr, err := store.GetOrCreate(ctx, 123, "alice", "https://api.openai.com/v1", "swing")
	if err != nil {
		t.Fatalf("GetOrCreate() error = %v", err)
	}
	if usr.TelegramUserID != 123 || usr.Username != "alice" {
		t.Fatalf("GetOrCreate() = %#v", usr)
	}
	if usr.APIBaseURL != "https://api.openai.com/v1" || usr.Mode != "swing" {
		t.Fatalf("默认配置错误: %#v", usr)
	}
	if usr.HasKey() {
		t.Fatal("新用户不应有 Key")
	}

	if err := store.UpdateKey(ctx, 123, []byte("cipher"), []byte("nonce"), 1); err != nil {
		t.Fatalf("UpdateKey() error = %v", err)
	}
	usr, err = store.GetOrCreate(ctx, 123, "alice", "https://api.openai.com/v1", "swing")
	if err != nil {
		t.Fatalf("GetOrCreate() error = %v", err)
	}
	if !usr.HasKey() || usr.KeyVersion != 1 || usr.KeyStatus != "valid" {
		t.Fatalf("Key 未保存: %#v", usr)
	}

	if err := store.UpdateBaseURL(ctx, 123, "https://proxy.example.com/v1"); err != nil {
		t.Fatalf("UpdateBaseURL() error = %v", err)
	}
	if err := store.UpdateModel(ctx, 123, "model-x"); err != nil {
		t.Fatalf("UpdateModel() error = %v", err)
	}
	if err := store.UpdateMode(ctx, 123, "short"); err != nil {
		t.Fatalf("UpdateMode() error = %v", err)
	}
	usr, err = store.GetOrCreate(ctx, 123, "alice", "https://api.openai.com/v1", "swing")
	if err != nil {
		t.Fatalf("GetOrCreate() error = %v", err)
	}
	if usr.APIBaseURL != "https://proxy.example.com/v1" || usr.Model != "model-x" || usr.Mode != "short" {
		t.Fatalf("配置未更新: %#v", usr)
	}
	if usr.PreferredTimeframe != "" {
		t.Fatalf("新用户不应有周期偏好: %#v", usr)
	}

	if err := store.UpdateTimeframe(ctx, 123, "4h"); err != nil {
		t.Fatalf("UpdateTimeframe() error = %v", err)
	}
	usr, err = store.GetOrCreate(ctx, 123, "alice", "https://api.openai.com/v1", "swing")
	if err != nil {
		t.Fatalf("GetOrCreate() error = %v", err)
	}
	if usr.PreferredTimeframe != "4h" {
		t.Fatalf("PreferredTimeframe = %q, want 4h", usr.PreferredTimeframe)
	}

	if err := store.UpdateTimeframe(ctx, 123, ""); err != nil {
		t.Fatalf("清除周期偏好 error = %v", err)
	}
	usr, err = store.GetOrCreate(ctx, 123, "alice", "https://api.openai.com/v1", "swing")
	if err != nil {
		t.Fatalf("GetOrCreate() error = %v", err)
	}
	if usr.PreferredTimeframe != "" {
		t.Fatalf("周期偏好未清空: %#v", usr)
	}
	if usr.PreferredIndicators != "" {
		t.Fatalf("新用户不应有指标偏好: %#v", usr)
	}

	if err := store.UpdateIndicators(ctx, 123, "rsi,atr"); err != nil {
		t.Fatalf("UpdateIndicators() error = %v", err)
	}
	usr, err = store.GetOrCreate(ctx, 123, "alice", "https://api.openai.com/v1", "swing")
	if err != nil {
		t.Fatalf("GetOrCreate() error = %v", err)
	}
	if usr.PreferredIndicators != "rsi,atr" {
		t.Fatalf("PreferredIndicators = %q, want rsi,atr", usr.PreferredIndicators)
	}
	if err := store.UpdateIndicators(ctx, 123, ""); err != nil {
		t.Fatalf("清除指标偏好 error = %v", err)
	}
	usr, err = store.GetOrCreate(ctx, 123, "alice", "https://api.openai.com/v1", "swing")
	if err != nil {
		t.Fatalf("GetOrCreate() error = %v", err)
	}
	if usr.PreferredIndicators != "" {
		t.Fatalf("指标偏好未清空: %#v", usr)
	}
	if usr.RiskPerTrade != 0 {
		t.Fatalf("新用户不应有风险金额: %#v", usr)
	}
	if usr.PreferredStrategy != "" {
		t.Fatalf("新用户不应有策略偏好: %#v", usr)
	}

	if err := store.UpdateStrategy(ctx, 123, "triple_screen"); err != nil {
		t.Fatalf("UpdateStrategy() error = %v", err)
	}
	usr, err = store.GetOrCreate(ctx, 123, "alice", "https://api.openai.com/v1", "swing")
	if err != nil {
		t.Fatalf("GetOrCreate() error = %v", err)
	}
	if usr.PreferredStrategy != "triple_screen" {
		t.Fatalf("PreferredStrategy = %q", usr.PreferredStrategy)
	}
	if err := store.UpdateStrategy(ctx, 123, ""); err != nil {
		t.Fatalf("清除策略偏好 error = %v", err)
	}

	if err := store.UpdateRiskPerTrade(ctx, 123, 100); err != nil {
		t.Fatalf("UpdateRiskPerTrade() error = %v", err)
	}
	usr, err = store.GetOrCreate(ctx, 123, "alice", "https://api.openai.com/v1", "swing")
	if err != nil {
		t.Fatalf("GetOrCreate() error = %v", err)
	}
	if usr.RiskPerTrade != 100 {
		t.Fatalf("RiskPerTrade = %v, want 100", usr.RiskPerTrade)
	}
	if err := store.UpdateRiskPerTrade(ctx, 123, 0); err != nil {
		t.Fatalf("清除风险金额 error = %v", err)
	}
	usr, err = store.GetOrCreate(ctx, 123, "alice", "https://api.openai.com/v1", "swing")
	if err != nil {
		t.Fatalf("GetOrCreate() error = %v", err)
	}
	if usr.RiskPerTrade != 0 {
		t.Fatalf("风险金额未清空: %#v", usr)
	}

	if err := store.DeleteKey(ctx, 123); err != nil {
		t.Fatalf("DeleteKey() error = %v", err)
	}
	usr, err = store.GetOrCreate(ctx, 123, "alice", "https://api.openai.com/v1", "swing")
	if err != nil {
		t.Fatalf("GetOrCreate() error = %v", err)
	}
	if usr.HasKey() || usr.KeyStatus != "missing" {
		t.Fatalf("Key 未删除: %#v", usr)
	}
}
