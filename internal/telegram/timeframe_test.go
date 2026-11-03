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

func TestTimeframeViewAndSet(t *testing.T) {
	service, sender, store, _, _ := newPhase2Service()
	ctx := context.Background()

	service.handle(ctx, privateUpdate("/timeframe"))
	if !strings.Contains(sender.last().Text, "1D") || !strings.Contains(sender.last().Text, "默认") {
		t.Fatalf("默认周期提示 = %q", sender.last().Text)
	}
	if !strings.Contains(sender.last().Text, "4h") {
		t.Fatalf("可用周期列表缺失 = %q", sender.last().Text)
	}

	service.handle(ctx, privateUpdate("/timeframe 4h"))
	if store.usr.PreferredTimeframe != "4h" {
		t.Fatalf("PreferredTimeframe = %q, want 4h", store.usr.PreferredTimeframe)
	}
	if !strings.Contains(sender.last().Text, "4h") {
		t.Fatalf("设置回复 = %q", sender.last().Text)
	}

	service.handle(ctx, privateUpdate("/timeframe D"))
	if store.usr.PreferredTimeframe != "1D" {
		t.Fatalf("别名未归一化: %q", store.usr.PreferredTimeframe)
	}

	service.handle(ctx, privateUpdate("/timeframe"))
	if strings.Contains(sender.last().Text, "（默认）") {
		t.Fatalf("已设置偏好时不应标记默认 = %q", sender.last().Text)
	}
}

func TestTimeframeReset(t *testing.T) {
	service, sender, store, _, _ := newPhase2Service()
	ctx := context.Background()

	store.usr.PreferredTimeframe = "4h"
	service.handle(ctx, privateUpdate("/timeframe reset"))

	if store.usr.PreferredTimeframe != "" {
		t.Fatalf("周期偏好未清空: %q", store.usr.PreferredTimeframe)
	}
	if !strings.Contains(sender.last().Text, "默认周期") {
		t.Fatalf("重置回复 = %q", sender.last().Text)
	}
}

func TestTimeframeInvalid(t *testing.T) {
	service, sender, store, _, _ := newPhase2Service()

	service.handle(context.Background(), privateUpdate("/timeframe 2d"))

	if store.usr.PreferredTimeframe != "" {
		t.Fatalf("非法周期不应保存: %q", store.usr.PreferredTimeframe)
	}
	if !strings.Contains(sender.last().Text, "不支持的周期") {
		t.Fatalf("非法周期回复 = %q", sender.last().Text)
	}
}

func TestTimeframeGroupRejected(t *testing.T) {
	service := groupService(&fakeSender{})
	update := groupUpdate("/timeframe 4h", 1, "alice")

	service.handle(context.Background(), update)

	messages := service.sender.(*fakeSender)
	if !strings.Contains(messages.last().Text, "私聊") {
		t.Fatalf("群组应引导回私聊: %q", messages.last().Text)
	}
}

func TestAnalyzeUsesPreferredTimeframe(t *testing.T) {
	service, sender, store, _, _ := newPhase2Service()
	store.usr = user.User{
		TelegramUserID:     1,
		KeyCiphertext:      []byte("enc:sk-test-key"),
		KeyNonce:           []byte("nonce"),
		KeyStatus:          "valid",
		APIBaseURL:         "https://api.openai.com/v1",
		Model:              "gpt-5-mini",
		Mode:               "swing",
		PreferredTimeframe: "4h",
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

	if analyzer.timeframe != market.TF4h {
		t.Fatalf("分析周期 = %q, want 4h", analyzer.timeframe)
	}
	if !senderHasText(sender, "正在分析", "4h") {
		t.Fatalf("分析开始提示未带周期")
	}
}

func TestAnalyzeUsesDefaultTimeframe(t *testing.T) {
	service, _, _, _, _ := newPhase2Service()
	service.cfg.DefaultTimeframe = market.TF1h
	store := service.store.(*fakeStore)
	store.usr = user.User{
		TelegramUserID: 1,
		KeyCiphertext:  []byte("enc:sk-test-key"),
		KeyNonce:       []byte("nonce"),
		KeyStatus:      "valid",
		APIBaseURL:     "https://api.openai.com/v1",
		Model:          "gpt-5-mini",
		Mode:           "swing",
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

	if analyzer.timeframe != market.TF1h {
		t.Fatalf("分析周期 = %q, want 1h", analyzer.timeframe)
	}
}

func senderHasText(sender *fakeSender, parts ...string) bool {
	sender.mu.Lock()
	defer sender.mu.Unlock()
	for _, message := range sender.messages {
		matched := true
		for _, part := range parts {
			if !strings.Contains(message.Text, part) {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

func TestStatusShowsDefaultTimeframe(t *testing.T) {
	service, sender, _, _, _ := newPhase2Service()
	service.cfg.DefaultTimeframe = market.TF1D

	service.handle(context.Background(), privateUpdate("/status"))

	if !strings.Contains(sender.last().Text, "周期") || !strings.Contains(sender.last().Text, "默认") {
		t.Fatalf("状态未显示默认周期: %q", sender.last().Text)
	}
}
