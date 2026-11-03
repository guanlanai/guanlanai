package analysis

import (
	"fmt"
	"strings"
)

// Indicator 是用户可以选择的指标组。
// ATR 和支撑阻力属于基础风控指标，始终保留，不受选择影响。
type Indicator string

const (
	IndicatorEMA               Indicator = "ema"
	IndicatorRSI               Indicator = "rsi"
	IndicatorMACD              Indicator = "macd"
	IndicatorATR               Indicator = "atr"
	IndicatorSuperTrend        Indicator = "supertrend"
	IndicatorVolume            Indicator = "volume"
	IndicatorSupportResistance Indicator = "support_resistance"
)

// MaxIndicators 是一次分析最多传给 LLM 的指标组数量。
const MaxIndicators = 8

// AllIndicators 按展示顺序列出全部指标组。
var AllIndicators = []Indicator{
	IndicatorEMA,
	IndicatorRSI,
	IndicatorMACD,
	IndicatorATR,
	IndicatorSuperTrend,
	IndicatorVolume,
	IndicatorSupportResistance,
}

// ParseIndicators 解析逗号或空白分隔的指标列表，去重并按标准顺序归一化。
// 空字符串表示使用默认集合（全部指标）。
func ParseIndicators(raw string) ([]Indicator, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return DefaultIndicators(), nil
	}

	fields := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == '，' || r == ' ' || r == '\t' || r == '\n'
	})
	if len(fields) == 0 {
		return DefaultIndicators(), nil
	}

	selected := make(map[Indicator]bool, len(fields))
	for _, field := range fields {
		indicator, ok := parseIndicator(field)
		if !ok {
			return nil, fmt.Errorf("不支持的指标 %q", field)
		}
		selected[indicator] = true
	}
	if len(selected) == 0 {
		return DefaultIndicators(), nil
	}
	if len(selected) > MaxIndicators {
		return nil, fmt.Errorf("最多选择 %d 个指标", MaxIndicators)
	}

	result := make([]Indicator, 0, len(selected))
	for _, indicator := range AllIndicators {
		if selected[indicator] {
			result = append(result, indicator)
		}
	}
	return result, nil
}

func parseIndicator(raw string) (Indicator, bool) {
	value := strings.ToLower(strings.TrimSpace(raw))
	value = strings.ReplaceAll(value, "-", "_")
	switch Indicator(value) {
	case IndicatorEMA, IndicatorRSI, IndicatorMACD, IndicatorATR,
		IndicatorSuperTrend, IndicatorVolume, IndicatorSupportResistance:
		return Indicator(value), true
	default:
		return "", false
	}
}

// DefaultIndicators 返回未设置偏好时的完整指标集合。
func DefaultIndicators() []Indicator {
	return append([]Indicator(nil), AllIndicators...)
}

// JoinIndicators 把指标列表转换成稳定的逗号分隔字符串，用于持久化。
func JoinIndicators(indicators []Indicator) string {
	parts := make([]string, 0, len(indicators))
	for _, indicator := range indicators {
		parts = append(parts, string(indicator))
	}
	return strings.Join(parts, ",")
}

// IndicatorsLabel 返回可直接展示的指标列表。
func IndicatorsLabel() string {
	return JoinIndicators(AllIndicators)
}

// HasIndicator 表示选择集合中是否包含某个指标。
// 空选择视为默认全部，便于旧调用和旧配置兼容。
func HasIndicator(selection []Indicator, target Indicator) bool {
	if len(selection) == 0 {
		return true
	}
	for _, indicator := range selection {
		if indicator == target {
			return true
		}
	}
	return false
}
