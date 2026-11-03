package render

import (
	"strings"
	"testing"
	"time"

	"github.com/guanlanai/guanlanai/internal/agent"
	"github.com/guanlanai/guanlanai/internal/analysis"
	"github.com/guanlanai/guanlanai/internal/risk"
)

func TestOpinionCard(t *testing.T) {
	report := &analysis.Report{
		Symbol:      "BTC",
		Pair:        "BTCUSDT",
		Quote:       "USDT",
		Market:      "crypto",
		Mode:        "swing",
		AsOf:        time.Date(2026, 11, 2, 5, 0, 0, 0, time.UTC),
		Price:       68432.1,
		ChangePct:   1.23,
		Source:      "Binance",
		DataQuality: "ok",
	}
	opinion := &agent.Opinion{
		Direction:  "bullish",
		Confidence: "medium",
		Summary:    "趋势向上",
		KeyLevels:  agent.KeyLevels{Support: []float64{66800, 65200}, Resistance: []float64{70500}},
		TradePlan: &agent.TradePlan{
			EntryLow:   67800,
			EntryHigh:  68600,
			StopLoss:   65200,
			TakeProfit: []float64{70500, 72000},
			RiskReward: 2.1,
		},
		Reasons: []string{"EMA 多头排列", "放量突破"},
		Risks:   []string{"跌破止损则失效"},
	}

	card := OpinionCard(report, opinion, nil)
	for _, want := range []string{"BTC/USDT", "偏多", "68,432.1", "66,800", "70,500", "EMA 多头排列", "仅供参考"} {
		if !strings.Contains(card, want) {
			t.Fatalf("卡片缺少 %q:\n%s", want, card)
		}
	}
	if strings.Contains(card, "<b>仓位</b>") {
		t.Fatal("没有仓位时不应展示仓位区块")
	}
}

func TestOpinionCardTripleScreen(t *testing.T) {
	report := &analysis.Report{
		Symbol: "BTC", Quote: "USDT", Market: "crypto", Mode: "swing", Source: "Binance",
		Strategy: string(analysis.StrategyTripleScreen),
		Screens: []analysis.Screen{
			{Role: "tide", Timeframe: "1W", Indicators: analysis.Indicators{Trend: "up"}},
			{Role: "wave", Timeframe: "1D", Indicators: analysis.Indicators{Trend: "up"}},
			{Role: "entry", Timeframe: "1h", Indicators: analysis.Indicators{Trend: "up"}},
		},
		Alignment: &analysis.Alignment{Tide: "up", Wave: "pullback", Entry: "triggered"},
	}
	opinion := &agent.Opinion{
		Direction: "bullish", Confidence: "medium", Summary: "x",
		Reasons: []string{"r"}, Risks: []string{"x"},
	}

	card := OpinionCard(report, opinion, nil)
	for _, want := range []string{"多周期", "潮汐：偏多", "波浪：回调", "涟漪：已触发", "冲突：否"} {
		if !strings.Contains(card, want) {
			t.Fatalf("卡片缺少 %q:\n%s", want, card)
		}
	}
}

func TestOpinionCardWithPosition(t *testing.T) {
	report := &analysis.Report{Symbol: "BTC", Quote: "USDT", Market: "crypto", Mode: "swing", Source: "Binance"}
	opinion := &agent.Opinion{
		Direction: "bullish", Confidence: "medium", Summary: "x",
		Reasons: []string{"r"}, Risks: []string{"x"},
	}
	position := &risk.Position{
		Direction:  "bullish",
		EntryPrice: 101,
		StopLoss:   95,
		RiskAmount: 100,
		Quantity:   16.6667,
		Notional:   1683.33,
		Leverage:   1,
	}

	card := OpinionCard(report, opinion, position)
	for _, want := range []string{"仓位", "风险上限：100 USDT", "入场参考：101", "止损：95", "16.6667 BTC", "不使用（现货）"} {
		if !strings.Contains(card, want) {
			t.Fatalf("卡片缺少 %q:\n%s", want, card)
		}
	}
}
