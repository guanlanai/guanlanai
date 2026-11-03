package telegram

import (
	"context"
	"strings"
	"testing"

	"github.com/go-telegram/bot/models"

	"github.com/guanlanai/guanlanai/internal/session"
	"github.com/guanlanai/guanlanai/internal/user"
)

const testBotUsername = "GuanLanAI_bot"

func groupService(sender *fakeSender) *Service {
	service, _, _, _, _ := newPhase2Service()
	service.sender = sender
	service.cfg.GroupsEnabled = true
	service.botUsername = testBotUsername
	service.botID = 42
	return service
}

func groupUpdate(text string, fromID int64, username string) *models.Update {
	return &models.Update{
		Message: &models.Message{
			Text: text,
			Chat: models.Chat{ID: 200, Type: models.ChatTypeSupergroup},
			From: &models.User{ID: fromID, Username: username},
		},
	}
}

func groupReplyUpdate(text string, fromID int64, username string) *models.Update {
	update := groupUpdate(text, fromID, username)
	update.Message.ReplyToMessage = &models.Message{
		From: &models.User{ID: 42, Username: testBotUsername},
	}
	return update
}

func TestGroupIgnoresMessagesWithoutMention(t *testing.T) {
	service, sender, _, _, _ := newPhase2Service()

	service.onUpdate(context.Background(), nil, groupUpdate("BTC", 1, "alice"))

	if sender.count() != 0 {
		t.Fatalf("未提及 Bot 时不应回复，实际 = %q", sender.last().Text)
	}
}

func TestGroupAcceptsMentionReplyAndCommand(t *testing.T) {
	tests := []struct {
		name   string
		update *models.Update
	}{
		{name: "mention", update: groupUpdate("@GuanLanAI_bot BTC", 1, "alice")},
		{name: "reply", update: groupReplyUpdate("那止损呢？", 1, "alice")},
		{name: "command", update: groupUpdate("/status", 1, "alice")},
		{name: "command with username", update: groupUpdate("/analyze@GuanLanAI_bot BTC", 1, "alice")},
	}
	for _, tt := range tests {
		if !serviceAddressed(t, tt.update) {
			t.Fatalf("%s: groupAddressed = false", tt.name)
		}
	}
}

func serviceAddressed(t *testing.T, update *models.Update) bool {
	t.Helper()
	service := groupService(&fakeSender{})
	return service.groupAddressed(update.Message)
}

func TestGroupCommandForOtherBotIgnored(t *testing.T) {
	service := groupService(&fakeSender{})
	if service.groupAddressed(groupUpdate("/status@OtherBot", 1, "alice").Message) {
		t.Fatal("发给其他 Bot 的命令不应处理")
	}
}

func TestGroupKeyCommandRejected(t *testing.T) {
	service, sender, _, _, _ := newPhase2Service()

	service.handle(context.Background(), groupUpdate("/key", 1, "alice"))

	if !strings.Contains(sender.last().Text, "私聊") {
		t.Fatalf("回复 = %q", sender.last().Text)
	}
}

func TestGroupSessionCommandRejected(t *testing.T) {
	service, sender, _, _, _ := newPhase2Service()

	for _, command := range []string{"/new", "/sessions", "/switch 1", "/rename 1 x", "/delete 1", "/baseurl", "/model", "/mode"} {
		service.handle(context.Background(), groupUpdate(command, 1, "alice"))
		if !strings.Contains(sender.last().Text, "私聊") {
			t.Fatalf("%s 回复 = %q", command, sender.last().Text)
		}
	}
}

func TestGroupAnalyzeRequiresKey(t *testing.T) {
	service, sender, _, _, _ := newPhase2Service()
	service.botUsername = testBotUsername
	service.botID = 42
	sessions := &fakeSessionStore{sess: &session.Session{ID: 7, UserID: 1, ChatID: 200, Scope: session.ScopeGroup, Title: "群会话"}}
	service.sessions = sessions

	service.handle(context.Background(), groupUpdate("@GuanLanAI_bot BTC", 1, "alice"))

	if !strings.Contains(sender.last().Text, "私聊") || !strings.Contains(sender.last().Text, "/key") {
		t.Fatalf("回复 = %q", sender.last().Text)
	}
}

func TestGroupResetUsesSenderSession(t *testing.T) {
	service, sender, store, _, _ := newPhase2Service()
	store.usr = user.User{ID: 1, TelegramUserID: 1}
	sessions := &fakeSessionStore{sess: &session.Session{ID: 7, UserID: 1, ChatID: 200, Scope: session.ScopeGroup, Title: "群会话"}}
	service.sessions = sessions

	service.handle(context.Background(), groupUpdate("/reset", 1, "alice"))

	if sessions.resetCalls != 1 {
		t.Fatalf("resetCalls = %d", sessions.resetCalls)
	}
	if !strings.Contains(sender.last().Text, "已清空") {
		t.Fatalf("回复 = %q", sender.last().Text)
	}
}

func TestStripBotMention(t *testing.T) {
	service := groupService(&fakeSender{})
	if got := service.stripBotMention("@GuanLanAI_bot 那止损呢？"); got != "那止损呢？" {
		t.Fatalf("stripBotMention = %q", got)
	}
	if got := service.stripBotMention("@guanlanai_bot, BTC"); got != "BTC" {
		t.Fatalf("stripBotMention = %q", got)
	}
}
