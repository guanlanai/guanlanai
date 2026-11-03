package analysis

import (
	"strings"
	"testing"
)

func TestParseIndicators(t *testing.T) {
	got, err := ParseIndicators("rsi, ema，supertrend macd")
	if err != nil {
		t.Fatalf("ParseIndicators() error = %v", err)
	}
	want := []Indicator{IndicatorEMA, IndicatorRSI, IndicatorMACD, IndicatorSuperTrend}
	if len(got) != len(want) {
		t.Fatalf("ParseIndicators() = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ParseIndicators()[%d] = %q, want %q", i, got[i], want[i])
		}
	}

	got, err = ParseIndicators("rsi,rsi")
	if err != nil {
		t.Fatalf("去重 error = %v", err)
	}
	if len(got) != 1 || got[0] != IndicatorRSI {
		t.Fatalf("去重结果 = %#v", got)
	}

	got, err = ParseIndicators("")
	if err != nil || len(got) != len(AllIndicators) {
		t.Fatalf("空输入应返回默认集合: %#v, %v", got, err)
	}
}

func TestParseIndicatorsRejectsUnknown(t *testing.T) {
	if _, err := ParseIndicators("rsi,news"); err == nil {
		t.Fatal("未知指标应当报错")
	}
}

func TestJoinIndicators(t *testing.T) {
	joined := JoinIndicators([]Indicator{IndicatorRSI, IndicatorATR})
	if joined != "rsi,atr" {
		t.Fatalf("JoinIndicators() = %q", joined)
	}
	if !strings.Contains(IndicatorsLabel(), "support_resistance") {
		t.Fatalf("IndicatorsLabel() = %q", IndicatorsLabel())
	}
}

func TestHasIndicator(t *testing.T) {
	if !HasIndicator(nil, IndicatorRSI) {
		t.Fatal("空选择应视为全部指标")
	}
	if !HasIndicator([]Indicator{IndicatorRSI}, IndicatorRSI) {
		t.Fatal("应包含 rsi")
	}
	if HasIndicator([]Indicator{IndicatorRSI}, IndicatorEMA) {
		t.Fatal("不应包含 ema")
	}
}
