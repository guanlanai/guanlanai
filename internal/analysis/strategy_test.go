package analysis

import (
	"testing"
	"time"

	"github.com/guanlanai/guanlanai/internal/market"
)

func TestParseStrategy(t *testing.T) {
	tests := []struct {
		input string
		want  Strategy
	}{
		{"", StrategySingle},
		{"single", StrategySingle},
		{"triple", StrategyTripleScreen},
		{"triple_screen", StrategyTripleScreen},
	}
	for _, tt := range tests {
		got, err := ParseStrategy(tt.input)
		if err != nil {
			t.Fatalf("ParseStrategy(%q) error = %v", tt.input, err)
		}
		if got != tt.want {
			t.Fatalf("ParseStrategy(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
	if _, err := ParseStrategy("multi"); err == nil {
		t.Fatal("未知策略应当报错")
	}
}

func TestStrategyTimeframes(t *testing.T) {
	got := Timeframes(StrategyTripleScreen, market.TF4h)
	want := []market.Timeframe{market.TF1W, market.TF1D, market.TF1h}
	if len(got) != len(want) {
		t.Fatalf("Timeframes() = %#v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Timeframes()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if TimeframeLabel(StrategyTripleScreen, market.TF4h) != "1W/1D/1h" {
		t.Fatalf("TimeframeLabel() = %q", TimeframeLabel(StrategyTripleScreen, market.TF4h))
	}
	if TimeframeLabel(StrategySingle, market.TF4h) != "4h" {
		t.Fatalf("单周期 TimeframeLabel() = %q", TimeframeLabel(StrategySingle, market.TF4h))
	}
}

func TestBuildTripleScreenReport(t *testing.T) {
	candles := makeCandles(300, 100, 1)
	snapshot := &market.Snapshot{
		Symbol: market.Symbol{Code: "BTC", Pair: "BTCUSDT", Market: "crypto"},
		Price:  399,
		Series: map[market.Timeframe]market.Series{
			market.TF1W: {Timeframe: market.TF1W, Candles: candles, AsOf: candles[len(candles)-1].OpenTime},
			market.TF1D: {Timeframe: market.TF1D, Candles: candles, AsOf: candles[len(candles)-1].OpenTime},
			market.TF1h: {Timeframe: market.TF1h, Candles: candles, AsOf: candles[len(candles)-1].OpenTime},
		},
		FetchedAt: time.Now().UTC(),
		Source:    "test",
	}
	report, err := BuildStrategyReport(
		snapshot, "swing", StrategyTripleScreen,
		ScreenSpecs(StrategyTripleScreen, market.TF1D), nil,
	)
	if err != nil {
		t.Fatalf("BuildStrategyReport() error = %v", err)
	}
	if report.Strategy != "triple_screen" || len(report.Screens) != 3 {
		t.Fatalf("report = %#v", report)
	}
	if report.Screens[0].Role != "tide" || report.Screens[1].Role != "wave" || report.Screens[2].Role != "entry" {
		t.Fatalf("screens = %#v", report.Screens)
	}
	if report.Alignment == nil {
		t.Fatal("缺少 alignment")
	}
	if len(report.Screens[0].Indicators.Support) > 2 || len(report.Screens[0].Indicators.Resistance) > 2 {
		t.Fatalf("多周期支撑阻力未压缩: %#v", report.Screens[0].Indicators)
	}
}

func TestBuildTripleScreenReportDegradesMissingScreen(t *testing.T) {
	candles := makeCandles(300, 100, 1)
	snapshot := &market.Snapshot{
		Symbol: market.Symbol{Code: "NEW", Pair: "NEWUSDT", Market: "crypto"},
		Price:  399,
		Series: map[market.Timeframe]market.Series{
			market.TF1D: {Timeframe: market.TF1D, Candles: candles, AsOf: candles[len(candles)-1].OpenTime},
			market.TF1h: {Timeframe: market.TF1h, Candles: candles, AsOf: candles[len(candles)-1].OpenTime},
		},
		FetchedAt: time.Now().UTC(),
	}
	report, err := BuildStrategyReport(
		snapshot, "swing", StrategyTripleScreen,
		ScreenSpecs(StrategyTripleScreen, market.TF1D), nil,
	)
	if err != nil {
		t.Fatalf("BuildStrategyReport() error = %v", err)
	}
	if report.DataQuality != "partial" {
		t.Fatalf("DataQuality = %q, want partial", report.DataQuality)
	}
	if report.Screens[0].DataQuality != "partial" || report.Alignment == nil || !report.Alignment.Conflict {
		t.Fatalf("缺失 tide 时应降级并标记冲突: %#v", report)
	}
}

func TestSelectCandlesDropsFormingHighTimeframe(t *testing.T) {
	now := time.Now().UTC()
	candles := []market.Candle{
		{OpenTime: now.Add(-48 * time.Hour), CloseTime: now.Add(-24 * time.Hour), Close: 100},
		{OpenTime: now, CloseTime: now.Add(24 * time.Hour), Close: 200},
	}

	closed, forming := selectCandles(candles, market.TF1D, now)
	if forming || len(closed) != 1 || closed[0].Close != 100 {
		t.Fatalf("日线应丢弃未收盘 K 线: len=%d forming=%v", len(closed), forming)
	}

	kept, forming := selectCandles(candles, market.TF1h, now)
	if !forming || len(kept) != 2 {
		t.Fatalf("小时线应保留未收盘 K 线并标记: len=%d forming=%v", len(kept), forming)
	}

	closed, forming = selectCandles(candles[:1], market.TF1W, now)
	if forming || len(closed) != 1 {
		t.Fatalf("已收盘 K 线不应被丢弃: len=%d forming=%v", len(closed), forming)
	}
}

func TestComputeAlignment(t *testing.T) {
	rsi := 45.0
	macd := 1.0
	screens := []Screen{
		{Role: "tide", Indicators: Indicators{Trend: "up"}},
		{Role: "wave", Indicators: Indicators{Trend: "up", RSI14: &rsi}},
		{Role: "entry", Indicators: Indicators{
			Trend:      "up",
			MACDHist:   &macd,
			SuperTrend: &SuperTrend{Direction: "up"},
		}},
	}
	alignment := computeAlignment(screens)
	if alignment.Tide != "up" || alignment.Wave != "pullback" ||
		alignment.Entry != "triggered" || alignment.Conflict {
		t.Fatalf("alignment = %#v", alignment)
	}

	screens[1].Indicators.Trend = "down"
	alignment = computeAlignment(screens)
	if alignment.Wave != "against" || !alignment.Conflict {
		t.Fatalf("逆向 wave alignment = %#v", alignment)
	}

	alignment = computeAlignment(append(screens[:1], screens[2:]...))
	if alignment.Wave != "unavailable" || !alignment.Conflict {
		t.Fatalf("缺少 screen alignment = %#v", alignment)
	}
}
