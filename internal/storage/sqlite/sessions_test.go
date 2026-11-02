package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/guanlanai/guanlanai/internal/usage"
)

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

	sess, err := store.GetOrCreateSession(ctx, usr.ID, 456)
	if err != nil {
		t.Fatalf("GetOrCreateSession() error = %v", err)
	}
	if sess.ChatID != 456 || sess.UserID != usr.ID {
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

	if _, err := store.SaveAnalysis(ctx, sess.ID, "BTC", "crypto", `{"opinion":"bullish"}`); err != nil {
		t.Fatalf("SaveAnalysis() error = %v", err)
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

	reset, err := store.Reset(ctx, usr.ID, 456)
	if err != nil {
		t.Fatalf("Reset() error = %v", err)
	}
	if reset.Generation != 1 || reset.Summary != "" || reset.LastAnalysisJSON != "" {
		t.Fatalf("reset = %#v", reset)
	}
	messages, _ = store.RecentMessages(ctx, sess.ID, 10)
	if len(messages) != 0 {
		t.Fatalf("reset messages = %#v", messages)
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
	sess, _ := store.GetOrCreateSession(ctx, usr.ID, 456)
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
