package telegram

import (
	"context"
	"strings"
	"testing"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/guanlanai/guanlanai/internal/agent"
	"github.com/guanlanai/guanlanai/internal/llm"
	"github.com/guanlanai/guanlanai/internal/session"
	"github.com/guanlanai/guanlanai/internal/user"
)

type fakeEditor struct {
	edits []*bot.EditMessageTextParams
}

func (f *fakeEditor) EditMessageText(_ context.Context, params *bot.EditMessageTextParams) (*models.Message, error) {
	f.edits = append(f.edits, params)
	return &models.Message{}, nil
}

type streamingAnalyzer struct {
	fakeAnalyzer
	deltas  []string
	content string
}

func (f *streamingAnalyzer) ChatStream(_ context.Context, _ *user.User, _, _, _ string, _ int64, onDelta func(string) error) (*agent.ChatResult, error) {
	if f.err != nil {
		return nil, f.err
	}
	for _, delta := range f.deltas {
		if err := onDelta(delta); err != nil {
			return nil, err
		}
	}
	return &agent.ChatResult{Content: f.content, Usage: &llm.CompletionResponse{PromptTokens: 3, CompletionTokens: 2}}, nil
}

func TestFollowUpStreamsIntoPlaceholder(t *testing.T) {
	service, sender, store, _, _ := newPhase2Service()
	editor := &fakeEditor{}
	service.editor = editor
	service.analyzer = &streamingAnalyzer{
		deltas:  []string{"你", "好"},
		content: "你好",
	}
	store.usr = user.User{
		ID:             1,
		TelegramUserID: 1,
		KeyCiphertext:  []byte("enc:sk-test-key"),
		KeyNonce:       []byte("nonce"),
		KeyStatus:      "valid",
		APIBaseURL:     "https://api.openai.com/v1",
		Model:          "gpt-5-mini",
		Mode:           "swing",
	}
	service.sessions = &fakeSessionStore{sess: &session.Session{
		ID: 1, UserID: 1, ChatID: 1,
		LastAnalysisJSON: `{"report":{"symbol":"BTC"}}`,
	}}
	service.usage = &fakeUsageStore{}

	previous := streamEditInterval
	streamEditInterval = 0
	defer func() { streamEditInterval = previous }()

	service.handle(context.Background(), privateUpdate("那止损呢？"))

	if len(editor.edits) < 2 {
		t.Fatalf("流式编辑次数 = %d, want >= 2", len(editor.edits))
	}
	last := editor.edits[len(editor.edits)-1]
	if !strings.Contains(last.Text, "你好") {
		t.Fatalf("最终编辑内容 = %q", last.Text)
	}
	if last.MessageID == 0 {
		t.Fatal("缺少 message_id")
	}
	if sender.count() < 1 {
		t.Fatal("应至少发送一条占位消息")
	}
}

func TestFollowUpStreamErrorFallsBackToErrorText(t *testing.T) {
	service, sender, store, _, _ := newPhase2Service()
	service.editor = &fakeEditor{}
	service.analyzer = &streamingAnalyzer{content: ""}
	service.analyzer.(*streamingAnalyzer).err = context.DeadlineExceeded
	store.usr = user.User{
		ID:             1,
		TelegramUserID: 1,
		KeyCiphertext:  []byte("enc:sk-test-key"),
		KeyNonce:       []byte("nonce"),
		KeyStatus:      "valid",
		APIBaseURL:     "https://api.openai.com/v1",
		Model:          "gpt-5-mini",
	}
	service.sessions = &fakeSessionStore{sess: &session.Session{
		ID: 1, UserID: 1, ChatID: 1,
		LastAnalysisJSON: `{"report":{"symbol":"BTC"}}`,
	}}

	service.handle(context.Background(), privateUpdate("那止损呢？"))

	if !strings.Contains(sender.last().Text, "超时") && !strings.Contains(sender.last().Text, "稍后") {
		t.Fatalf("错误提示 = %q", sender.last().Text)
	}
}
