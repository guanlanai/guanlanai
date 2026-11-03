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

func TestStrategyViewAndSet(t *testing.T) {
	service, sender, store, _, _ := newPhase2Service()
	ctx := context.Background()

	service.handle(ctx, privateUpdate("/strategy"))
	if !strings.Contains(sender.last().Text, "单周期") || !strings.Contains(sender.last().Text, "（默认）") {
		t.Fatalf("默认策略提示 = %q", sender.last().Text)
	}
	if !strings.Contains(sender.last().Text, "1W/1D/1h") {
		t.Fatalf("三重滤网模板缺失 = %q", sender.last().Text)
	}

	service.handle(ctx, privateUpdate("/strategy triple"))
	if store.usr.PreferredStrategy != "triple_screen" {
		t.Fatalf("PreferredStrategy = %q", store.usr.PreferredStrategy)
	}
	if !strings.Contains(sender.last().Text, "三重滤网") {
		t.Fatalf("设置回复 = %q", sender.last().Text)
	}

	service.handle(ctx, privateUpdate("/strategy"))
	if strings.Contains(sender.last().Text, "（默认）") {
		t.Fatalf("已设置策略时不应标记默认 = %q", sender.last().Text)
	}

	service.handle(ctx, privateUpdate("/strategy single"))
	if store.usr.PreferredStrategy != "" {
		t.Fatalf("单选单周期时应清空偏好: %q", store.usr.PreferredStrategy)
	}
}

func TestStrategyReset(t *testing.T) {
	service, sender, store, _, _ := newPhase2Service()
	store.usr.PreferredStrategy = "triple_screen"

	service.handle(context.Background(), privateUpdate("/strategy reset"))

	if store.usr.PreferredStrategy != "" {
		t.Fatalf("策略偏好未清空: %q", store.usr.PreferredStrategy)
	}
	if !strings.Contains(sender.last().Text, "默认分析策略") {
		t.Fatalf("重置回复 = %q", sender.last().Text)
	}
}

func TestStrategyInvalid(t *testing.T) {
	service, sender, store, _, _ := newPhase2Service()

	service.handle(context.Background(), privateUpdate("/strategy multi"))

	if store.usr.PreferredStrategy != "" {
		t.Fatalf("非法策略不应保存: %q", store.usr.PreferredStrategy)
	}
	if !strings.Contains(sender.last().Text, "不支持的分析策略") {
		t.Fatalf("非法策略回复 = %q", sender.last().Text)
	}
}

func TestStrategyGroupRejected(t *testing.T) {
	service := groupService(&fakeSender{})
	service.handle(context.Background(), groupUpdate("/strategy triple", 1, "alice"))

	if !senderHasText(service.sender.(*fakeSender), "私聊") {
		t.Fatalf("群组应引导回私聊: %q", service.sender.(*fakeSender).last().Text)
	}
}

func TestStatusShowsTripleScreenStrategy(t *testing.T) {
	service, sender, store, _, _ := newPhase2Service()
	store.usr = user.User{TelegramUserID: 1, PreferredStrategy: "triple_screen"}

	service.handle(context.Background(), privateUpdate("/status"))

	if !strings.Contains(sender.last().Text, "三重滤网") || !strings.Contains(sender.last().Text, "1W/1D/1h") {
		t.Fatalf("状态未显示三重滤网: %q", sender.last().Text)
	}
}

func TestAnalyzeUsesTripleScreenStrategy(t *testing.T) {
	service, sender, store, _, _ := newPhase2Service()
	sessions := &fakeSessionStore{}
	service.sessions = sessions
	store.usr = user.User{
		ID:                 1,
		TelegramUserID:     1,
		KeyCiphertext:      []byte("enc:sk-test-key"),
		KeyNonce:           []byte("nonce"),
		KeyStatus:          "valid",
		APIBaseURL:         "https://api.openai.com/v1",
		Model:              "gpt-5-mini",
		Mode:               "swing",
		PreferredTimeframe: "4h",
		PreferredStrategy:  "triple_screen",
	}
	analyzer := &fakeAnalyzer{result: &agent.Result{
		Report: &analysis.Report{Symbol: "BTC", Pair: "BTCUSDT", Market: "crypto", Mode: "swing", Strategy: "triple_screen"},
		Opinion: &agent.Opinion{
			Direction: "bullish", Confidence: "medium", Summary: "test",
			Reasons: []string{"r"}, Risks: []string{"x"},
		},
	}}
	service.analyzer = analyzer

	service.handle(context.Background(), privateUpdate("/analyze BTC"))

	if analyzer.strategy != analysis.StrategyTripleScreen {
		t.Fatalf("分析策略 = %q", analyzer.strategy)
	}
	if !senderHasText(sender, "正在分析", "1W/1D/1h") {
		t.Fatal("分析开始提示未带三重滤网周期")
	}
	if sessions.sess.CurrentTimeframe != "1W/1D/1h" {
		t.Fatalf("CurrentTimeframe = %q", sessions.sess.CurrentTimeframe)
	}
	if analyzer.timeframe != market.TF4h {
		t.Fatalf("单周期偏好仍应传给 agent: %q", analyzer.timeframe)
	}
}
