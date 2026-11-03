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
	if timeframe.SuperTrend == nil {
		t.Fatal("SuperTrend 缺失")
	}
	if timeframe.SuperTrend.Direction != "up" {
		t.Fatalf("SuperTrend.Direction = %q, want up", timeframe.SuperTrend.Direction)
	}
	if timeframe.SuperTrend.Value >= 399 {
		t.Fatalf("上涨趋势中 SuperTrend 应低于价格: %#v", timeframe.SuperTrend)
	}
	if timeframe.SuperTrend.DistancePct <= 0 {
		t.Fatalf("SuperTrend.DistancePct = %v, want > 0", timeframe.SuperTrend.DistancePct)
	}
}

func TestAnalyzeSuperTrendDowntrend(t *testing.T) {
	candles := makeCandles(300, 400, -1)
	timeframe, err := analyzeCandles(candles)
	if err != nil {
		t.Fatalf("analyzeCandles() error = %v", err)
	}
	if timeframe.SuperTrend == nil {
		t.Fatal("SuperTrend 缺失")
	}
	if timeframe.SuperTrend.Direction != "down" {
		t.Fatalf("SuperTrend = %#v", timeframe.SuperTrend)
	}
	price := candles[len(candles)-1].Close
	if timeframe.SuperTrend.Value <= price {
		t.Fatalf("下跌趋势中 SuperTrend 应高于价格: %#v", timeframe.SuperTrend)
	}
	if timeframe.SuperTrend.DistancePct >= 0 {
		t.Fatalf("SuperTrend.DistancePct = %v, want < 0", timeframe.SuperTrend.DistancePct)
	}
}

func TestAnalyzeSuperTrendConstantSeries(t *testing.T) {
	candles := makeCandles(300, 100, 0)
	timeframe, err := analyzeCandles(candles)
	if err != nil {
		t.Fatalf("analyzeCandles() error = %v", err)
	}
	if timeframe.SuperTrend == nil {
		t.Fatal("SuperTrend 缺失")
	}
	// ATR = 2，乘数 3，因此下轨 = 100 - 6 = 94。
	if timeframe.SuperTrend.Value != 94 {
		t.Fatalf("SuperTrend.Value = %v, want 94", timeframe.SuperTrend.Value)
	}
	if timeframe.SuperTrend.Direction != "up" {
		t.Fatalf("SuperTrend.Direction = %q, want up", timeframe.SuperTrend.Direction)
	}
	if timeframe.SuperTrend.Period != 10 || timeframe.SuperTrend.Multiplier != 3 {
		t.Fatalf("SuperTrend 参数 = %#v", timeframe.SuperTrend)
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
		Series: map[market.Timeframe]market.Series{
			market.TF1h: {Timeframe: market.TF1h, Candles: candles, AsOf: candles[len(candles)-1].OpenTime},
		},
		FetchedAt: time.Now().UTC(),
	}
	report, err := BuildReport(snapshot, "swing", market.TF1h, nil)
	if err != nil {
		t.Fatalf("BuildReport() error = %v", err)
	}
	if report.Symbol != "BTC" || report.Pair != "BTCUSDT" || report.Mode != "swing" {
		t.Fatalf("report = %#v", report)
	}
	if report.DataQuality != "ok" {
		t.Fatalf("DataQuality = %q", report.DataQuality)
	}
	if len(report.Screens) != 1 {
		t.Fatalf("Screens = %#v", report.Screens)
	}
	if report.Strategy != "single" || report.Screens[0].Role != "primary" || report.Screens[0].Timeframe != "1h" {
		t.Fatalf("screen = %#v", report.Screens[0])
	}
}

func TestBuildReportFiltersIndicators(t *testing.T) {
	candles := makeCandles(300, 100, 1)
	snapshot := &market.Snapshot{
		Symbol: market.Symbol{Code: "BTC", Pair: "BTCUSDT", Market: "crypto"},
		Price:  399,
		Series: map[market.Timeframe]market.Series{
			market.TF1D: {Timeframe: market.TF1D, Candles: candles, AsOf: candles[len(candles)-1].OpenTime},
		},
		FetchedAt: time.Now().UTC(),
	}
	report, err := BuildReport(snapshot, "swing", market.TF1D, []Indicator{IndicatorRSI})
	if err != nil {
		t.Fatalf("BuildReport() error = %v", err)
	}
	indicators := report.Screens[0].Indicators
	if indicators.RSI14 == nil {
		t.Fatal("选择的 rsi 应保留")
	}
	if indicators.EMA20 != nil || indicators.EMA50 != nil || indicators.MACDHist != nil ||
		indicators.SuperTrend != nil || indicators.VolumeRatio != nil {
		t.Fatalf("未选择的指标不应输出: %#v", indicators)
	}
	if indicators.ATRPct == nil || len(indicators.Support) == 0 || len(indicators.Resistance) == 0 {
		t.Fatalf("基础风控指标必须保留: %#v", indicators)
	}
	if indicators.Trend == "" {
		t.Fatal("trend 应始终保留")
	}
}

func TestBuildReportMarksPartial(t *testing.T) {
	candles := makeCandles(100, 100, 1)
	snapshot := &market.Snapshot{
		Symbol: market.Symbol{Code: "BTC", Pair: "BTCUSDT", Market: "crypto"},
		Price:  199,
		Series: map[market.Timeframe]market.Series{
			market.TF1M: {Timeframe: market.TF1M, Candles: candles, Partial: true, AsOf: candles[len(candles)-1].OpenTime},
		},
		FetchedAt: time.Now().UTC(),
	}
	report, err := BuildReport(snapshot, "swing", market.TF1M, nil)
	if err != nil {
		t.Fatalf("BuildReport() error = %v", err)
	}
	if report.DataQuality != "partial" || report.Screens[0].DataQuality != "partial" {
		t.Fatalf("partial 标记缺失: report=%q screen=%q", report.DataQuality, report.Screens[0].DataQuality)
	}
}
