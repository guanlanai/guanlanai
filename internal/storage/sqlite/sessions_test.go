package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/guanlanai/guanlanai/internal/session"
	"github.com/guanlanai/guanlanai/internal/usage"
	"github.com/guanlanai/guanlanai/migrations"
)

func testStoreWithLimit(t *testing.T, limit int) *Store {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := Migrate(context.Background(), db, migrations.FS); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	return NewStore(db, WithMaxPrivateSessions(limit))
}

func TestSessionLifecycle(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	usr, err := store.GetOrCreate(ctx, 123, "alice", "https://api.openai.com/v1", "swing")
	if err != nil {
		t.Fatalf("GetOrCreate() error = %v", err)
	}
	if usr.ID == 0 {
		t.Fatal("用户 ID 未返回")
	}

	sess, err := store.GetOrCreateActivePrivate(ctx, usr.ID, 456)
	if err != nil {
		t.Fatalf("GetOrCreateActivePrivate() error = %v", err)
	}
	if sess.ChatID != 456 || sess.UserID != usr.ID || sess.Scope != session.ScopePrivate {
		t.Fatalf("session = %#v", sess)
	}

	if err := store.AppendMessage(ctx, sess.ID, "user", "分析 BTC", 5); err != nil {
		t.Fatalf("AppendMessage() error = %v", err)
	}
	if err := store.AppendMessage(ctx, sess.ID, "assistant", "偏多", 3); err != nil {
		t.Fatalf("AppendMessage() error = %v", err)
	}
	messages, err := store.RecentMessages(ctx, sess.ID, 10)
	if err != nil {
		t.Fatalf("RecentMessages() error = %v", err)
	}
	if len(messages) != 2 || messages[0].Role != "user" || messages[1].Role != "assistant" {
		t.Fatalf("messages = %#v", messages)
	}

	saved, err := store.SaveAnalysis(ctx, sess.ID, "BTC", "crypto", "4h", `{"opinion":"bullish"}`)
	if err != nil {
		t.Fatalf("SaveAnalysis() error = %v", err)
	}
	if saved.CurrentTimeframe != "4h" {
		t.Fatalf("CurrentTimeframe = %q, want 4h", saved.CurrentTimeframe)
	}
	if err := store.UpdateSummary(ctx, sess.ID, "summary", 5); err != nil {
		t.Fatalf("UpdateSummary() error = %v", err)
	}
	if err := store.TrimMessages(ctx, sess.ID, 1); err != nil {
		t.Fatalf("TrimMessages() error = %v", err)
	}
	messages, _ = store.RecentMessages(ctx, sess.ID, 10)
	if len(messages) != 1 || messages[0].Content != "偏多" {
		t.Fatalf("trim messages = %#v", messages)
	}

	reset, err := store.Reset(ctx, sess.ID)
	if err != nil {
		t.Fatalf("Reset() error = %v", err)
	}
	if reset.Generation != 1 || reset.Summary != "" || reset.LastAnalysisJSON != "" || reset.CurrentSymbol != "" || reset.CurrentTimeframe != "" {
		t.Fatalf("reset = %#v", reset)
	}
	messages, _ = store.RecentMessages(ctx, sess.ID, 10)
	if len(messages) != 0 {
		t.Fatalf("reset messages = %#v", messages)
	}
}

func TestPrivateSessionManagement(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	usr, _ := store.GetOrCreate(ctx, 123, "alice", "https://api.openai.com/v1", "swing")
	other, _ := store.GetOrCreate(ctx, 456, "bob", "https://api.openai.com/v1", "swing")

	first, err := store.GetOrCreateActivePrivate(ctx, usr.ID, 900)
	if err != nil {
		t.Fatalf("GetOrCreateActivePrivate() error = %v", err)
	}
	second, err := store.CreatePrivate(ctx, usr.ID, 900, "BTC 短线")
	if err != nil {
		t.Fatalf("CreatePrivate() error = %v", err)
	}
	if second.Title != "BTC 短线" {
		t.Fatalf("title = %q", second.Title)
	}

	active, err := store.GetOrCreateActivePrivate(ctx, usr.ID, 900)
	if err != nil || active.ID != second.ID {
		t.Fatalf("active = %#v, err = %v", active, err)
	}

	list, err := store.ListPrivate(ctx, usr.ID, 900)
	if err != nil {
		t.Fatalf("ListPrivate() error = %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("list = %#v", list)
	}

	// 跨用户/跨 chat 的 ID 操作必须被拒绝。
	if _, err := store.GetPrivate(ctx, other.ID, 900, first.ID); !errors.Is(err, session.ErrNotFound) {
		t.Fatalf("跨用户读取 err = %v", err)
	}
	if err := store.SetActivePrivate(ctx, other.ID, 900, first.ID); !errors.Is(err, session.ErrNotFound) {
		t.Fatalf("跨用户切换 err = %v", err)
	}
	if err := store.RenamePrivate(ctx, other.ID, 900, first.ID, "hack"); !errors.Is(err, session.ErrNotFound) {
		t.Fatalf("跨用户重命名 err = %v", err)
	}
	if err := store.DeletePrivate(ctx, other.ID, 900, first.ID); !errors.Is(err, session.ErrNotFound) {
		t.Fatalf("跨用户删除 err = %v", err)
	}

	if err := store.SetActivePrivate(ctx, usr.ID, 900, first.ID); err != nil {
		t.Fatalf("SetActivePrivate() error = %v", err)
	}
	active, _ = store.GetOrCreateActivePrivate(ctx, usr.ID, 900)
	if active.ID != first.ID {
		t.Fatalf("active = %#v", active)
	}

	if err := store.RenamePrivate(ctx, usr.ID, 900, first.ID, "ETH 波段"); err != nil {
		t.Fatalf("RenamePrivate() error = %v", err)
	}
	renamed, _ := store.GetPrivate(ctx, usr.ID, 900, first.ID)
	if renamed.Title != "ETH 波段" {
		t.Fatalf("renamed = %#v", renamed)
	}

	// 删除 active 后自动切到剩余会话。
	if err := store.DeletePrivate(ctx, usr.ID, 900, first.ID); err != nil {
		t.Fatalf("DeletePrivate() error = %v", err)
	}
	active, _ = store.GetOrCreateActivePrivate(ctx, usr.ID, 900)
	if active.ID != second.ID {
		t.Fatalf("删除 active 后 = %#v", active)
	}
	if _, err := store.GetByID(ctx, first.ID); !errors.Is(err, session.ErrNotFound) {
		t.Fatalf("已删除会话仍可读取: %v", err)
	}

	if err := store.DeletePrivate(ctx, usr.ID, 900, second.ID); !errors.Is(err, session.ErrLastSession) {
		t.Fatalf("删除最后一条 err = %v", err)
	}
}

func TestPrivateSessionLimit(t *testing.T) {
	store := testStoreWithLimit(t, 2)
	ctx := context.Background()
	usr, _ := store.GetOrCreate(ctx, 123, "alice", "https://api.openai.com/v1", "swing")

	if _, err := store.GetOrCreateActivePrivate(ctx, usr.ID, 900); err != nil {
		t.Fatalf("GetOrCreateActivePrivate() error = %v", err)
	}
	if _, err := store.CreatePrivate(ctx, usr.ID, 900, "second"); err != nil {
		t.Fatalf("CreatePrivate() error = %v", err)
	}
	if _, err := store.CreatePrivate(ctx, usr.ID, 900, "third"); !errors.Is(err, session.ErrLimitReached) {
		t.Fatalf("超限 err = %v", err)
	}
}

func TestGroupSessionIsolation(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	alice, _ := store.GetOrCreate(ctx, 1, "alice", "https://api.openai.com/v1", "swing")
	bob, _ := store.GetOrCreate(ctx, 2, "bob", "https://api.openai.com/v1", "swing")

	aliceGroupA, err := store.GetOrCreateGroup(ctx, alice.ID, 100)
	if err != nil {
		t.Fatalf("GetOrCreateGroup() error = %v", err)
	}
	again, _ := store.GetOrCreateGroup(ctx, alice.ID, 100)
	if again.ID != aliceGroupA.ID {
		t.Fatalf("同一用户同一群出现多条会话: %#v / %#v", aliceGroupA, again)
	}
	aliceGroupB, _ := store.GetOrCreateGroup(ctx, alice.ID, 200)
	if aliceGroupB.ID == aliceGroupA.ID {
		t.Fatal("不同群应使用不同会话")
	}
	bobGroupA, _ := store.GetOrCreateGroup(ctx, bob.ID, 100)
	if bobGroupA.ID == aliceGroupA.ID {
		t.Fatal("同群不同成员应使用不同会话")
	}
	if aliceGroupA.Scope != session.ScopeGroup {
		t.Fatalf("scope = %q", aliceGroupA.Scope)
	}
}

func TestUsage(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	usr, err := store.GetOrCreate(ctx, 123, "alice", "https://api.openai.com/v1", "swing")
	if err != nil {
		t.Fatalf("GetOrCreate() error = %v", err)
	}

	if err := store.Record(ctx, usage.Event{
		UserID: usr.ID, RequestID: "req1", Kind: "analysis", Model: "gpt-5-mini",
		PromptTokens: 10, CompletionTokens: 20, LatencyMS: 100, Status: "ok",
	}); err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	summary, err := store.Today(ctx, usr.ID)
	if err != nil {
		t.Fatalf("Today() error = %v", err)
	}
	if summary.Requests != 1 || summary.PromptTokens != 10 || summary.CompletionTokens != 20 {
		t.Fatalf("summary = %#v", summary)
	}
}

func TestCleanupMessages(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	usr, _ := store.GetOrCreate(ctx, 123, "alice", "https://api.openai.com/v1", "swing")
	sess, _ := store.GetOrCreateActivePrivate(ctx, usr.ID, 456)
	if err := store.AppendMessage(ctx, sess.ID, "user", "old", 1); err != nil {
		t.Fatalf("AppendMessage() error = %v", err)
	}

	count, err := store.CleanupMessages(ctx, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("CleanupMessages() error = %v", err)
	}
	if count != 1 {
		t.Fatalf("count = %d, want 1", count)
	}
}
