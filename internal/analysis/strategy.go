package analysis

import (
	"fmt"
	"strings"

	"github.com/guanlanai/guanlanai/internal/market"
)

// Strategy 是分析计划使用的 screen 组合。
type Strategy string

const (
	// StrategySingle 是单周期策略：只有一个 primary screen。
	StrategySingle Strategy = "single"
	// StrategyTripleScreen 是 Elder 三重滤网：潮汐、波浪、涟漪各一个 screen。
	StrategyTripleScreen Strategy = "triple_screen"
)

// ScreenSpec 声明一个 screen 的角色和周期。
type ScreenSpec struct {
	Role      string
	Timeframe market.Timeframe
}

// TripleScreenTemplate 是三重滤网的默认模板：1W / 1D / 1h。
var TripleScreenTemplate = []ScreenSpec{
	{Role: "tide", Timeframe: market.TF1W},
	{Role: "wave", Timeframe: market.TF1D},
	{Role: "entry", Timeframe: market.TF1h},
}

// ParseStrategy 归一化用户输入；空字符串表示单周期。
func ParseStrategy(raw string) (Strategy, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "single", "one":
		return StrategySingle, nil
	case "triple", "triple_screen", "triple-screen", "three":
		return StrategyTripleScreen, nil
	default:
		return "", fmt.Errorf("不支持的分析策略 %q", raw)
	}
}

// StrategyLabel 返回适合展示的策略名。
func StrategyLabel(strategy Strategy) string {
	if strategy == StrategyTripleScreen {
		return "三重滤网"
	}
	return "单周期"
}

// ScreenSpecs 返回策略对应的 screen 列表。
func ScreenSpecs(strategy Strategy, singleTimeframe market.Timeframe) []ScreenSpec {
	if strategy == StrategyTripleScreen {
		return append([]ScreenSpec(nil), TripleScreenTemplate...)
	}
	if singleTimeframe == "" {
		singleTimeframe = market.DefaultTimeframe
	}
	return []ScreenSpec{{Role: "primary", Timeframe: singleTimeframe}}
}

// Timeframes 返回策略需要拉取的周期。
func Timeframes(strategy Strategy, singleTimeframe market.Timeframe) []market.Timeframe {
	specs := ScreenSpecs(strategy, singleTimeframe)
	result := make([]market.Timeframe, 0, len(specs))
	for _, spec := range specs {
		result = append(result, spec.Timeframe)
	}
	return result
}

// computeAlignment 使用确定性规则计算三重滤网对齐，不交给 LLM。
func computeAlignment(screens []Screen) *Alignment {
	alignment := &Alignment{
		Tide:     "unavailable",
		Wave:     "unavailable",
		Entry:    "unavailable",
		Conflict: true,
	}
	byRole := make(map[string]Indicators, len(screens))
	for _, screen := range screens {
		byRole[screen.Role] = screen.Indicators
	}

	tide, ok := byRole["tide"]
	if !ok || tide.Trend == "" || tide.Trend == "sideways" {
		if ok && tide.Trend != "" {
			alignment.Tide = tide.Trend
		}
		return alignment
	}
	alignment.Tide = tide.Trend

	wave, waveOK := byRole["wave"]
	entry, entryOK := byRole["entry"]
	if !waveOK || !entryOK {
		return alignment
	}
	alignment.Wave = waveState(wave, tide.Trend)
	alignment.Entry = entryState(entry, tide.Trend)
	alignment.Conflict = alignment.Wave == "unavailable" ||
		alignment.Wave == "against" ||
		alignment.Entry == "unavailable" ||
		alignment.Entry == "against"
	return alignment
}

func waveState(indicator Indicators, bias string) string {
	if indicator.Trend == "" {
		return "unavailable"
	}
	switch bias {
	case "up":
		if indicator.Trend == "down" {
			return "against"
		}
		if indicator.RSI14 != nil && *indicator.RSI14 <= 50 {
			return "pullback"
		}
		return "aligned"
	case "down":
		if indicator.Trend == "up" {
			return "against"
		}
		if indicator.RSI14 != nil && *indicator.RSI14 >= 50 {
			return "pullback"
		}
		return "aligned"
	default:
		return "unclear"
	}
}

func entryState(indicator Indicators, bias string) string {
	if indicator.Trend == "" {
		return "unavailable"
	}
	if indicator.SuperTrend != nil {
		if bias == "up" && indicator.SuperTrend.Direction == "down" {
			return "against"
		}
		if bias == "down" && indicator.SuperTrend.Direction == "up" {
			return "against"
		}
	}
	macdUp := indicator.MACDHist != nil && *indicator.MACDHist > 0
	macdDown := indicator.MACDHist != nil && *indicator.MACDHist < 0
	recentFlipUp := indicator.SuperTrend != nil &&
		indicator.SuperTrend.Direction == "up" && indicator.SuperTrend.BarsSinceFlip <= 5
	recentFlipDown := indicator.SuperTrend != nil &&
		indicator.SuperTrend.Direction == "down" && indicator.SuperTrend.BarsSinceFlip <= 5

	switch bias {
	case "up":
		if indicator.Trend == "down" {
			return "against"
		}
		if recentFlipUp || (indicator.Trend == "up" && macdUp) {
			return "triggered"
		}
		return "waiting"
	case "down":
		if indicator.Trend == "up" {
			return "against"
		}
		if recentFlipDown || (indicator.Trend == "down" && macdDown) {
			return "triggered"
		}
		return "waiting"
	default:
		return "unclear"
	}
}

// TimeframeLabel 返回适合写入会话和展示的周期文案。
func TimeframeLabel(strategy Strategy, singleTimeframe market.Timeframe) string {
	if strategy != StrategyTripleScreen {
		if singleTimeframe == "" {
			singleTimeframe = market.DefaultTimeframe
		}
		return singleTimeframe.String()
	}
	specs := ScreenSpecs(strategy, singleTimeframe)
	parts := make([]string, 0, len(specs))
	for _, spec := range specs {
		parts = append(parts, spec.Timeframe.String())
	}
	return strings.Join(parts, "/")
}
