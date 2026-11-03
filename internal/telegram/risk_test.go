package telegram

import (
	"context"
	"strings"
	"testing"

	"github.com/guanlanai/guanlanai/internal/agent"
	"github.com/guanlanai/guanlanai/internal/analysis"
	"github.com/guanlanai/guanlanai/internal/user"
)

func TestRiskViewAndSet(t *testing.T) {
	service, sender, store, _, _ := newPhase2Service()
	ctx := context.Background()

	service.handle(ctx, privateUpdate("/risk"))
	if !strings.Contains(sender.last().Text, "未设置") {
		t.Fatalf("未设置提示 = %q", sender.last().Text)
	}

	service.handle(ctx, privateUpdate("/risk 100"))
	if store.usr.RiskPerTrade != 100 {
		t.Fatalf("RiskPerTrade = %v, want 100", store.usr.RiskPerTrade)
	}
	if !strings.Contains(sender.last().Text, "100 USDT") {
		t.Fatalf("设置回复 = %q", sender.last().Text)
	}

	service.handle(ctx, privateUpdate("/risk $1,500.5 USDT"))
	if store.usr.RiskPerTrade != 1500.5 {
		t.Fatalf("RiskPerTrade = %v, want 1500.5", store.usr.RiskPerTrade)
	}

	service.handle(ctx, privateUpdate("/risk"))
	if !strings.Contains(sender.last().Text, "1500.5") {
		t.Fatalf("查看回复 = %q", sender.last().Text)
	}
}

func TestRiskReset(t *testing.T) {
	service, sender, store, _, _ := newPhase2Service()
	store.usr.RiskPerTrade = 100

	service.handle(context.Background(), privateUpdate("/risk reset"))

	if store.usr.RiskPerTrade != 0 {
		t.Fatalf("风险金额未清空: %v", store.usr.RiskPerTrade)
	}
	if !strings.Contains(sender.last().Text, "不再提供仓位建议") {
		t.Fatalf("重置回复 = %q", sender.last().Text)
	}
}

func TestRiskInvalid(t *testing.T) {
	service, sender, store, _, _ := newPhase2Service()

	for _, input := range []string{"/risk 0", "/risk -1", "/risk abc"} {
		service.handle(context.Background(), privateUpdate(input))
		if store.usr.RiskPerTrade != 0 {
			t.Fatalf("%s 不应保存: %v", input, store.usr.RiskPerTrade)
		}
		if !strings.Contains(sender.last().Text, "风险金额不合法") {
			t.Fatalf("%s 回复 = %q", input, sender.last().Text)
		}
	}
}

func TestRiskGroupRejected(t *testing.T) {
	service := groupService(&fakeSender{})
	service.handle(context.Background(), groupUpdate("/risk 100", 1, "alice"))

	if !senderHasText(service.sender.(*fakeSender), "私聊") {
		t.Fatalf("群组应引导回私聊: %q", service.sender.(*fakeSender).last().Text)
	}
}

func TestAnalyzeShowsPositionInPrivate(t *testing.T) {
	service, sender, store, _, _ := newPhase2Service()
	store.usr = user.User{
		ID:             1,
		TelegramUserID: 1,
		KeyCiphertext:  []byte("enc:sk-test-key"),
		KeyNonce:       []byte("nonce"),
		KeyStatus:      "valid",
		APIBaseURL:     "https://api.openai.com/v1",
		Model:          "gpt-5-mini",
		Mode:           "swing",
		RiskPerTrade:   100,
	}
	service.analyzer = positionAnalyzer()

	service.handle(context.Background(), privateUpdate("/analyze BTC"))

	if !strings.Contains(sender.last().Text, "<b>仓位</b>") ||
		!strings.Contains(sender.last().Text, "风险上限：100 USDT") {
		t.Fatalf("观点卡片缺少仓位: %q", sender.last().Text)
	}
}

func TestAnalyzeHidesPositionInGroup(t *testing.T) {
	service := groupService(&fakeSender{})
	store := service.store.(*fakeStore)
	store.usr = user.User{
		ID:             1,
		TelegramUserID: 1,
		KeyCiphertext:  []byte("enc:sk-test-key"),
		KeyNonce:       []byte("nonce"),
		KeyStatus:      "valid",
		APIBaseURL:     "https://api.openai.com/v1",
		Model:          "gpt-5-mini",
		Mode:           "swing",
		RiskPerTrade:   100,
	}
	service.analyzer = positionAnalyzer()

	service.handle(context.Background(), groupUpdate("/analyze BTC", 1, "alice"))

	sender := service.sender.(*fakeSender)
	if strings.Contains(sender.last().Text, "仓位") || strings.Contains(sender.last().Text, "风险上限") {
		t.Fatalf("群组不应展示风险金额和仓位: %q", sender.last().Text)
	}
}

func positionAnalyzer() *fakeAnalyzer {
	return &fakeAnalyzer{result: &agent.Result{
		Report: &analysis.Report{
			Symbol: "BTC", Pair: "BTCUSDT", Quote: "USDT", Market: "crypto",
			Mode: "swing", Price: 101, Source: "test",
		},
		Opinion: &agent.Opinion{
			Direction: "bullish", Confidence: "medium", Summary: "test",
			Reasons: []string{"r"}, Risks: []string{"x"},
			TradePlan: &agent.TradePlan{
				EntryLow: 100, EntryHigh: 102, StopLoss: 95,
				TakeProfit: []float64{120}, RiskReward: 3,
			},
		},
	}}
}
