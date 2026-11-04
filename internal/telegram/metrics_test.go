package telegram

import (
	"context"
	"testing"

	"github.com/guanlanai/guanlanai/internal/ratelimit"
	"github.com/guanlanai/guanlanai/internal/session"
	"github.com/guanlanai/guanlanai/internal/user"
)

type fakeTelegramMetrics struct {
	updates  []string
	limits   []string
	messages []string
}

func (f *fakeTelegramMetrics) ObserveTelegramUpdate(result string) {
	f.updates = append(f.updates, result)
}

func (f *fakeTelegramMetrics) ObserveRateLimit(kind string) {
	f.limits = append(f.limits, kind)
}

func (f *fakeTelegramMetrics) ObserveTelegramMessage(status string) {
	f.messages = append(f.messages, status)
}

func TestSendRecordsMetrics(t *testing.T) {
	recorder := &fakeTelegramMetrics{}
	service := newTestService(&fakeSender{})
	service.metrics = recorder

	service.send(context.Background(), 1, "hello")

	if len(recorder.messages) != 1 || recorder.messages[0] != "ok" {
		t.Fatalf("messages = %#v", recorder.messages)
	}
}

func TestUpdateOverloadRecordsMetrics(t *testing.T) {
	recorder := &fakeTelegramMetrics{}
	service := newTestService(&fakeSender{})
	service.metrics = recorder

	service.onUpdate(context.Background(), nil, privateUpdate("BTC"))

	if len(recorder.updates) != 1 || recorder.updates[0] != "overloaded" {
		t.Fatalf("updates = %#v", recorder.updates)
	}
}

func TestRateLimitRecordsMetrics(t *testing.T) {
	recorder := &fakeTelegramMetrics{}
	service := newTestService(&fakeSender{})
	service.metrics = recorder
	service.analyzer = &fakeAnalyzer{}
	service.limiter = ratelimit.New(1, 10, 10)
	usr := &user.User{
		TelegramUserID: 1,
		KeyCiphertext:  []byte("ciphertext"),
		KeyNonce:       []byte("nonce"),
	}
	if ok, _ := service.limiter.AllowAnalysis(usr.TelegramUserID); !ok {
		t.Fatal("首次分析应当被允许")
	}

	service.handleAnalyze(context.Background(), 1, usr, nil, "BTC", session.ScopePrivate)

	if len(recorder.limits) != 1 || recorder.limits[0] != "analysis" {
		t.Fatalf("limits = %#v", recorder.limits)
	}
}
