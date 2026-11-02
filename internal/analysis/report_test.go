package analysis

import (
	"testing"
	"time"

	"github.com/guanlanai/guanlanai/internal/market"
)

func makeCandles(count int, start, step float64) []market.Candle {
	candles := make([]market.Candle, 0, count)
	for i := 0; i < count; i++ {
		closePrice := start + float64(i)*step
		candles = append(candles, market.Candle{
			OpenTime: time.Unix(int64(i*3600), 0).UTC(),
			Open:     closePrice - step/2,
			High:     closePrice + 1,
			Low:      closePrice - 1,
			Close:    closePrice,
			Volume:   1000 + float64(i),
		})
	}
	return candles
}

func TestAnalyzeUptrend(t *testing.T) {
	candles := makeCandles(300, 100, 1)
	timeframe, err := analyzeCandles(candles)
	if err != nil {
		t.Fatalf("analyzeCandles() error = %v", err)
	}
	if timeframe.Trend != "up" {
		t.Fatalf("Trend = %q, want up", timeframe.Trend)
	}
	if timeframe.RSI14 == nil || *timeframe.RSI14 <= 50 {
		t.Fatalf("RSI14 = %v", timeframe.RSI14)
	}
	if timeframe.EMA20 == nil || timeframe.EMA50 == nil || timeframe.EMA200 == nil {
		t.Fatal("EMA 指标缺失")
	}
	if len(timeframe.Support) == 0 {
		t.Fatal("缺少支撑位")
	}
}

func TestAnalyzeConstantSeries(t *testing.T) {
	candles := makeCandles(300, 100, 0)
	timeframe, err := analyzeCandles(candles)
	if err != nil {
		t.Fatalf("analyzeCandles() error = %v", err)
	}
	if timeframe.Trend != "sideways" {
		t.Fatalf("Trend = %q, want sideways", timeframe.Trend)
	}
	if timeframe.RSI14 == nil || *timeframe.RSI14 != 50 {
		t.Fatalf("RSI14 = %v, want 50", timeframe.RSI14)
	}
	if timeframe.ATRPct == nil || *timeframe.ATRPct != 2 {
		t.Fatalf("ATRPct = %v, want 2", timeframe.ATRPct)
	}
}

func TestBuildReport(t *testing.T) {
	candles := makeCandles(300, 100, 1)
	snapshot := &market.Snapshot{
		Symbol: market.Symbol{Code: "BTC", Pair: "BTCUSDT", Market: "crypto"},
		Price:  399,
		Timeframes: map[string][]market.Candle{
			"1h": candles,
		},
		FetchedAt: time.Now().UTC(),
	}
	report, err := BuildReport(snapshot, "swing")
	if err != nil {
		t.Fatalf("BuildReport() error = %v", err)
	}
	if report.Symbol != "BTC" || report.Pair != "BTCUSDT" || report.Mode != "swing" {
		t.Fatalf("report = %#v", report)
	}
	if report.DataQuality != "ok" {
		t.Fatalf("DataQuality = %q", report.DataQuality)
	}
	if len(report.Timeframes) != 1 {
		t.Fatalf("Timeframes = %#v", report.Timeframes)
	}
}
