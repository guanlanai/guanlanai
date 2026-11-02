package telegram

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/guanlanai/guanlanai/internal/session"
	"github.com/guanlanai/guanlanai/internal/usage"
	"github.com/guanlanai/guanlanai/internal/user"
)

type fakeSessionStore struct {
	sess       *session.Session
	messages   []session.Message
	resetCalls int
}

func (f *fakeSessionStore) GetOrCreateSession(_ context.Context, userID, chatID int64) (*session.Session, error) {
	if f.sess == nil {
		f.sess = &session.Session{ID: 1, UserID: userID, ChatID: chatID}
	}
	return f.sess, nil
}

func (f *fakeSessionStore) AppendMessage(_ context.Context, _ int64, role, content string, tokens int) error {
	f.messages = append(f.messages, session.Message{Role: role, Content: content, Tokens: tokens})
	return nil
}

func (f *fakeSessionStore) RecentMessages(_ context.Context, _ int64, _ int) ([]session.Message, error) {
	return f.messages, nil
}

func (f *fakeSessionStore) SaveAnalysis(_ context.Context, _ int64, symbol, market, analysisJSON string) (*session.Session, error) {
	f.sess.CurrentSymbol = symbol
	f.sess.CurrentMarket = market
	f.sess.LastAnalysisJSON = analysisJSON
	return f.sess, nil
}

func (f *fakeSessionStore) UpdateSummary(_ context.Context, _ int64, summary string, _ int) error {
	f.sess.Summary = summary
	return nil
}

func (f *fakeSessionStore) TrimMessages(_ context.Context, _ int64, keep int) error {
	if keep < len(f.messages) {
		f.messages = f.messages[len(f.messages)-keep:]
	}
	return nil
}

func (f *fakeSessionStore) Reset(_ context.Context, _ int64, _ int64) (*session.Session, error) {
	f.resetCalls++
	f.messages = nil
	f.sess.LastAnalysisJSON = ""
	f.sess.Summary = ""
	return f.sess, nil
}

func (f *fakeSessionStore) CleanupMessages(_ context.Context, _ time.Time) (int64, error) {
	return 0, nil
}

type fakeUsageStore struct {
	events []usage.Event
}

func (f *fakeUsageStore) Record(_ context.Context, event usage.Event) error {
	f.events = append(f.events, event)
	return nil
}

func (f *fakeUsageStore) Today(_ context.Context, _ int64) (usage.Summary, error) {
	var summary usage.Summary
	for _, event := range f.events {
		summary.Requests++
		summary.PromptTokens += int64(event.PromptTokens)
		summary.CompletionTokens += int64(event.CompletionTokens)
	}
	return summary, nil
}

func TestFollowUpFlow(t *testing.T) {
	service, sender, store, _, _ := newPhase2Service()
	analyzer := &fakeAnalyzer{}
	ctx := context.Background()
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
	sessions := &fakeSessionStore{sess: &session.Session{
		ID: 1, UserID: 1, ChatID: 1,
		CurrentSymbol:    "BTC",
		LastAnalysisJSON: `{"report":{"symbol":"BTC"},"opinion":{"direction":"bullish"}}`,
	}}
	usageStore := &fakeUsageStore{}
	service.sessions = sessions
	service.usage = usageStore
	service.analyzer = analyzer

	service.handle(ctx, privateUpdate("那止损呢？"))

	if !strings.Contains(sender.last().Text, "answer") {
		t.Fatalf("回复 = %q", sender.last().Text)
	}
	if len(usageStore.events) == 0 || usageStore.events[0].Kind != "chat" {
		t.Fatalf("usage = %#v", usageStore.events)
	}
}

func TestResetSession(t *testing.T) {
	service, sender, store, _, _ := newPhase2Service()
	store.usr = user.User{ID: 1, TelegramUserID: 1}
	sessions := &fakeSessionStore{sess: &session.Session{ID: 1, UserID: 1, ChatID: 1, LastAnalysisJSON: "x"}}
	service.sessions = sessions

	service.handle(context.Background(), privateUpdate("/reset"))

	if sessions.resetCalls != 1 {
		t.Fatalf("resetCalls = %d", sessions.resetCalls)
	}
	if !strings.Contains(sender.last().Text, "已清空") {
		t.Fatalf("回复 = %q", sender.last().Text)
	}
}

func TestFollowUpRequiresAnalysis(t *testing.T) {
	service, sender, store, _, _ := newPhase2Service()
	store.usr = user.User{ID: 1, TelegramUserID: 1}
	service.sessions = &fakeSessionStore{sess: &session.Session{ID: 1, UserID: 1, ChatID: 1}}

	service.handle(context.Background(), privateUpdate("那止损呢？"))

	if !strings.Contains(sender.last().Text, "没有可追问") {
		t.Fatalf("回复 = %q", sender.last().Text)
	}
}
