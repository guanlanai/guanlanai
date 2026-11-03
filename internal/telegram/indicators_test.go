package telegram

import (
	"context"
	"strings"
	"testing"

	"github.com/guanlanai/guanlanai/internal/agent"
	"github.com/guanlanai/guanlanai/internal/analysis"
	"github.com/guanlanai/guanlanai/internal/market"
	"github.com/guanlanai/guanlanai/internal/user"
)

func TestIndicatorsViewAndSet(t *testing.T) {
	service, sender, store, _, _ := newPhase2Service()
	ctx := context.Background()

	service.handle(ctx, privateUpdate("/indicators"))
	if !strings.Contains(sender.last().Text, "当前指标") || !strings.Contains(sender.last().Text, "（默认）") {
		t.Fatalf("默认指标提示 = %q", sender.last().Text)
	}
	if !strings.Contains(sender.last().Text, "supertrend") {
		t.Fatalf("可用指标列表缺失 = %q", sender.last().Text)
	}

	service.handle(ctx, privateUpdate("/indicators rsi,atr"))
	if store.usr.PreferredIndicators != "rsi,atr" {
		t.Fatalf("PreferredIndicators = %q, want rsi,atr", store.usr.PreferredIndicators)
	}
	if !strings.Contains(sender.last().Text, "rsi,atr") {
		t.Fatalf("设置回复 = %q", sender.last().Text)
	}

	service.handle(ctx, privateUpdate("/indicators"))
	if strings.Contains(sender.last().Text, "（默认）") {
		t.Fatalf("已设置偏好时不应标记默认 = %q", sender.last().Text)
	}
}

func TestIndicatorsReset(t *testing.T) {
	service, sender, store, _, _ := newPhase2Service()
	ctx := context.Background()
	store.usr.PreferredIndicators = "rsi"

	service.handle(ctx, privateUpdate("/indicators reset"))

	if store.usr.PreferredIndicators != "" {
		t.Fatalf("指标偏好未清空: %q", store.usr.PreferredIndicators)
	}
	if !strings.Contains(sender.last().Text, "默认指标") {
		t.Fatalf("重置回复 = %q", sender.last().Text)
	}
}

func TestIndicatorsInvalid(t *testing.T) {
	service, sender, store, _, _ := newPhase2Service()

	service.handle(context.Background(), privateUpdate("/indicators rsi,news"))

	if store.usr.PreferredIndicators != "" {
		t.Fatalf("非法指标不应保存: %q", store.usr.PreferredIndicators)
	}
	if !strings.Contains(sender.last().Text, "指标设置失败") {
		t.Fatalf("非法指标回复 = %q", sender.last().Text)
	}
}

func TestIndicatorsGroupRejected(t *testing.T) {
	service := groupService(&fakeSender{})
	service.handle(context.Background(), groupUpdate("/indicators rsi", 1, "alice"))

	if !senderHasText(service.sender.(*fakeSender), "私聊") {
		t.Fatalf("群组应引导回私聊: %q", service.sender.(*fakeSender).last().Text)
	}
}

func TestAnalyzeUsesPreferredIndicators(t *testing.T) {
	service, _, store, _, _ := newPhase2Service()
	sessions := &fakeSessionStore{}
	service.sessions = sessions
	store.usr = user.User{
		ID:                  1,
		TelegramUserID:      1,
		KeyCiphertext:       []byte("enc:sk-test-key"),
		KeyNonce:            []byte("nonce"),
		KeyStatus:           "valid",
		APIBaseURL:          "https://api.openai.com/v1",
		Model:               "gpt-5-mini",
		Mode:                "swing",
		PreferredIndicators: "rsi,atr",
	}
	analyzer := &fakeAnalyzer{result: &agent.Result{
		Report: &analysis.Report{Symbol: "BTC", Pair: "BTCUSDT", Market: "crypto", Mode: "swing"},
		Opinion: &agent.Opinion{
			Direction: "bullish", Confidence: "medium", Summary: "test",
			Reasons: []string{"r"}, Risks: []string{"x"},
		},
	}}
	service.analyzer = analyzer

	service.handle(context.Background(), privateUpdate("/analyze BTC"))

	if len(analyzer.indicators) != 2 || analyzer.indicators[0] != analysis.IndicatorRSI ||
		analyzer.indicators[1] != analysis.IndicatorATR {
		t.Fatalf("分析指标 = %#v", analyzer.indicators)
	}
	if analyzer.timeframe != market.DefaultTimeframe {
		t.Fatalf("默认周期 = %q", analyzer.timeframe)
	}
	if analyzer.sessionID != 1 {
		t.Fatalf("sessionID = %d, want 1", analyzer.sessionID)
	}
}
