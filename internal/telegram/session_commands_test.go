package telegram

import (
	"context"
	"strings"
	"testing"

	"github.com/guanlanai/guanlanai/internal/session"
	"github.com/guanlanai/guanlanai/internal/user"
)

func privateSessionService() (*Service, *fakeSender, *fakeSessionStore) {
	service, sender, store, _, _ := newPhase2Service()
	store.usr = user.User{ID: 1, TelegramUserID: 1}
	sessions := &fakeSessionStore{sess: &session.Session{
		ID: 1, UserID: 1, ChatID: 1, Scope: session.ScopePrivate, Title: "会话 1",
	}}
	service.sessions = sessions
	return service, sender, sessions
}

func TestNewSessionCommand(t *testing.T) {
	service, sender, sessions := privateSessionService()

	service.handle(context.Background(), privateUpdate("/new BTC 短线"))

	if sessions.sess.Title != "BTC 短线" {
		t.Fatalf("title = %q", sessions.sess.Title)
	}
	if !strings.Contains(sender.last().Text, "已新建会话") || !strings.Contains(sender.last().Text, "BTC 短线") {
		t.Fatalf("回复 = %q", sender.last().Text)
	}
}

func TestSessionListCommand(t *testing.T) {
	service, sender, _ := privateSessionService()

	service.handle(context.Background(), privateUpdate("/sessions"))

	if !strings.Contains(sender.last().Text, "会话列表") || !strings.Contains(sender.last().Text, "active") {
		t.Fatalf("回复 = %q", sender.last().Text)
	}
}

func TestSwitchRenameDeleteCommands(t *testing.T) {
	service, sender, sessions := privateSessionService()
	ctx := context.Background()

	service.handle(ctx, privateUpdate("/switch 1"))
	if !strings.Contains(sender.last().Text, "已切换") {
		t.Fatalf("switch 回复 = %q", sender.last().Text)
	}

	service.handle(ctx, privateUpdate("/rename 1 ETH 波段"))
	if sessions.sess.Title != "ETH 波段" {
		t.Fatalf("title = %q", sessions.sess.Title)
	}
	if !strings.Contains(sender.last().Text, "已重命名") {
		t.Fatalf("rename 回复 = %q", sender.last().Text)
	}

	service.handle(ctx, privateUpdate("/delete 1"))
	if !strings.Contains(sender.last().Text, "确认删除") {
		t.Fatalf("delete 回复 = %q", sender.last().Text)
	}

	service.handle(ctx, privateUpdate("/delete 1 confirm"))
	if !strings.Contains(sender.last().Text, "至少需要保留") {
		t.Fatalf("delete confirm 回复 = %q", sender.last().Text)
	}
}

func TestSessionCommandValidation(t *testing.T) {
	service, sender, _ := privateSessionService()
	ctx := context.Background()

	service.handle(ctx, privateUpdate("/switch abc"))
	if !strings.Contains(sender.last().Text, "用法") {
		t.Fatalf("switch 回复 = %q", sender.last().Text)
	}

	service.handle(ctx, privateUpdate("/rename 1"))
	if !strings.Contains(sender.last().Text, "用法") {
		t.Fatalf("rename 回复 = %q", sender.last().Text)
	}

	service.handle(ctx, privateUpdate("/delete"))
	if !strings.Contains(sender.last().Text, "用法") {
		t.Fatalf("delete 回复 = %q", sender.last().Text)
	}

	service.handle(ctx, privateUpdate("/switch 99"))
	if !strings.Contains(sender.last().Text, "找不到该会话") {
		t.Fatalf("switch 不存在回复 = %q", sender.last().Text)
	}

	longTitle := strings.Repeat("长", session.MaxTitleLength+1)
	service.handle(ctx, privateUpdate("/new "+longTitle))
	if !strings.Contains(sender.last().Text, "最多") {
		t.Fatalf("超长标题回复 = %q", sender.last().Text)
	}
}

func TestStatusShowsActiveSession(t *testing.T) {
	service, sender, _ := privateSessionService()

	service.handle(context.Background(), privateUpdate("/status"))

	if !strings.Contains(sender.last().Text, "当前会话") {
		t.Fatalf("回复 = %q", sender.last().Text)
	}
}
