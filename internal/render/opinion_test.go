package render

import (
	"strings"
	"testing"
	"time"

	"github.com/guanlanai/guanlanai/internal/agent"
	"github.com/guanlanai/guanlanai/internal/analysis"
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

	card := OpinionCard(report, opinion)
	for _, want := range []string{"BTC/USDT", "偏多", "68,432.1", "66,800", "70,500", "EMA 多头排列", "仅供参考"} {
		if !strings.Contains(card, want) {
			t.Fatalf("卡片缺少 %q:\n%s", want, card)
		}
	}
}
