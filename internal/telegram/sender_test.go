package telegram

import (
	"context"
	"testing"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

type retrySender struct {
	calls int
	fail  int
}

func (s *retrySender) SendMessage(_ context.Context, _ *bot.SendMessageParams) (*models.Message, error) {
	s.calls++
	if s.calls <= s.fail {
		return nil, &bot.TooManyRequestsError{RetryAfter: 0}
	}
	return &models.Message{}, nil
}

func TestRateLimitedSenderSuccess(t *testing.T) {
	next := &fakeSender{}
	sender := NewRateLimitedSender(next, 0, 100, 2)

	if _, err := sender.SendMessage(context.Background(), &bot.SendMessageParams{ChatID: int64(1), Text: "hi"}); err != nil {
		t.Fatalf("SendMessage() error = %v", err)
	}
	if next.count() != 1 {
		t.Fatalf("calls = %d", next.count())
	}
}

func TestRateLimitedSenderRetries429(t *testing.T) {
	next := &retrySender{fail: 1}
	sender := NewRateLimitedSender(next, 0, 100, 2)
	sender.baseWait = time.Millisecond

	if _, err := sender.SendMessage(context.Background(), &bot.SendMessageParams{ChatID: int64(1), Text: "hi"}); err != nil {
		t.Fatalf("SendMessage() error = %v", err)
	}
	if next.calls != 2 {
		t.Fatalf("calls = %d, want 2", next.calls)
	}
}
