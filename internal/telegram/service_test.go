package telegram

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/guanlanai/guanlanai/internal/config"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

type fakeSender struct {
	mu       sync.Mutex
	messages []*bot.SendMessageParams
}

func (f *fakeSender) SendMessage(_ context.Context, params *bot.SendMessageParams) (*models.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.messages = append(f.messages, params)
	return &models.Message{}, nil
}

func (f *fakeSender) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.messages)
}

func (f *fakeSender) last() *bot.SendMessageParams {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.messages) == 0 {
		return nil
	}
	return f.messages[len(f.messages)-1]
}

func newTestService(sender *fakeSender) *Service {
	return &Service{
		cfg: config.Config{
			InstanceName: "test",
			AppEnv:       "dev",
			DefaultModel: "",
			DefaultMode:  "swing",
		},
		sender:  sender,
		logger:  testLogger(),
		started: time.Now(),
	}
}

func privateUpdate(text string) *models.Update {
	return &models.Update{
		Message: &models.Message{
			Text: text,
			Chat: models.Chat{ID: 1, Type: models.ChatTypePrivate},
		},
	}
}

func TestHandleStart(t *testing.T) {
	sender := &fakeSender{}
	service := newTestService(sender)

	service.handle(context.Background(), privateUpdate("/start"))

	if sender.count() != 1 {
		t.Fatalf("消息数 = %d, want 1", sender.count())
	}
	message := sender.last()
	if message.ParseMode != models.ParseModeHTML {
		t.Fatalf("ParseMode = %q, want HTML", message.ParseMode)
	}
	if !strings.Contains(message.Text, "观澜") {
		t.Fatalf("欢迎语缺少项目名: %q", message.Text)
	}
}

func TestHandleSymbolWithoutKey(t *testing.T) {
	sender := &fakeSender{}
	service := newTestService(sender)

	service.handle(context.Background(), privateUpdate("BTC"))

	if sender.count() != 1 {
		t.Fatalf("消息数 = %d, want 1", sender.count())
	}
	if !strings.Contains(sender.last().Text, "/key") {
		t.Fatalf("未配置 Key 时应提示 /key: %q", sender.last().Text)
	}
}

func TestHandleAnalyzeWithoutSymbol(t *testing.T) {
	sender := &fakeSender{}
	service := newTestService(sender)

	service.handle(context.Background(), privateUpdate("/analyze"))

	if !strings.Contains(sender.last().Text, "币种代码") {
		t.Fatalf("回复 = %q", sender.last().Text)
	}
}

func TestGroupChatRejected(t *testing.T) {
	sender := &fakeSender{}
	service := newTestService(sender)

	service.onUpdate(context.Background(), nil, &models.Update{
		Message: &models.Message{
			Text: "BTC",
			Chat: models.Chat{ID: 2, Type: models.ChatTypeGroup},
		},
	})

	if !strings.Contains(sender.last().Text, "私聊") {
		t.Fatalf("回复 = %q", sender.last().Text)
	}
}

func TestParseCommand(t *testing.T) {
	tests := []struct {
		raw     string
		command string
		args    string
	}{
		{"/start", "/start", ""},
		{"/start@GuanLanAI_bot", "/start", ""},
		{"/analyze BTC", "/analyze", "BTC"},
		{"BTC", "", "BTC"},
	}
	for _, tt := range tests {
		command, args := parseCommand(tt.raw)
		if command != tt.command || args != tt.args {
			t.Fatalf("parseCommand(%q) = (%q, %q), want (%q, %q)", tt.raw, command, args, tt.command, tt.args)
		}
	}
}

func TestDispatcherSerializesPerChat(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var mu sync.Mutex
	var order []string
	done := make(chan struct{}, 2)

	dispatcher := NewDispatcher(ctx, func(_ context.Context, update *models.Update) {
		mu.Lock()
		order = append(order, update.Message.Text)
		mu.Unlock()
		time.Sleep(20 * time.Millisecond)
		done <- struct{}{}
	}, 2, testLogger())

	if !dispatcher.Enqueue(1, privateUpdate("a")) {
		t.Fatal("Enqueue(a) = false")
	}
	if !dispatcher.Enqueue(1, privateUpdate("b")) {
		t.Fatal("Enqueue(b) = false")
	}

	for i := 0; i < 2; i++ {
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("等待 dispatcher 超时")
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if len(order) != 2 || order[0] != "a" || order[1] != "b" {
		t.Fatalf("处理顺序 = %#v, want [a b]", order)
	}
}
