package telegram

import (
	"context"
	"strings"
	"testing"

	"github.com/guanlanai/guanlanai/internal/session"
	"github.com/guanlanai/guanlanai/internal/user"
)

func TestFollowUpRejectsOversizedQuestion(t *testing.T) {
	service, sender, store, _, _ := newPhase2Service()
	analyzer := &fakeAnalyzer{}
	service.analyzer = analyzer
	service.sessions = &fakeSessionStore{sess: &session.Session{
		ID: 1, UserID: 1, ChatID: 1,
		LastAnalysisJSON: `{"report":{"symbol":"BTC"}}`,
	}}
	store.usr = user.User{
		ID:             1,
		TelegramUserID: 1,
		KeyCiphertext:  []byte("enc:sk-test-key"),
		KeyNonce:       []byte("nonce"),
		KeyStatus:      "valid",
		APIBaseURL:     "https://api.openai.com/v1",
		Model:          "gpt-5-mini",
	}

	service.handle(context.Background(), privateUpdate(strings.Repeat("问", maxFollowUpRunes+1)))

	if !strings.Contains(sender.last().Text, "问题太长") {
		t.Fatalf("回复 = %q", sender.last().Text)
	}
	if analyzer.calls != 0 {
		t.Fatalf("超长问题不应触发分析，calls = %d", analyzer.calls)
	}
}
