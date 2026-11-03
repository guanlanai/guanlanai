package market

import (
	"errors"
	"fmt"
	"strings"
)

// ErrInvalidTimeframe 表示用户输入或配置的周期不在标准集合内。
var ErrInvalidTimeframe = errors.New("不支持的周期")

// Timeframe 是 TradingView 惯例的分析周期。
// 内部、数据库和展示统一使用该类型，不暴露行情源的 interval 命名。
type Timeframe string

const (
	TF1m  Timeframe = "1m"
	TF5m  Timeframe = "5m"
	TF15m Timeframe = "15m"
	TF30m Timeframe = "30m"
	TF1h  Timeframe = "1h"
	TF2h  Timeframe = "2h"
	TF4h  Timeframe = "4h"
	TF6h  Timeframe = "6h"
	TF12h Timeframe = "12h"
	TF1D  Timeframe = "1D"
	TF1W  Timeframe = "1W"
	TF1M  Timeframe = "1M"
)

// DefaultTimeframe 是用户未设置偏好时的系统默认周期。
const DefaultTimeframe = TF1D

// AllTimeframes 按从短到长列出全部标准周期。
var AllTimeframes = []Timeframe{
	TF1m, TF5m, TF15m, TF30m, TF1h, TF2h, TF4h, TF6h, TF12h, TF1D, TF1W, TF1M,
}

// String 返回标准周期名。
func (t Timeframe) String() string {
	return string(t)
}

// Valid 表示是否为标准周期。
func (t Timeframe) Valid() bool {
	for _, standard := range AllTimeframes {
		if t == standard {
			return true
		}
	}
	return false
}

// ParseTimeframe 归一化用户输入的周期，支持 D/W/M/1d/1w 等别名。
func ParseTimeframe(raw string) (Timeframe, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", fmt.Errorf("%w: %q", ErrInvalidTimeframe, raw)
	}

	for _, standard := range AllTimeframes {
		if value == standard.String() {
			return standard, nil
		}
	}

	// 大小写不敏感别名；1m 已在上面按分钟精确匹配，不会被当成月份。
	switch strings.ToUpper(value) {
	case "D", "1D":
		return TF1D, nil
	case "W", "1W":
		return TF1W, nil
	case "M", "1M":
		return TF1M, nil
	}
	return "", fmt.Errorf("%w: %q", ErrInvalidTimeframe, raw)
}

// TimeframesLabel 返回适合直接展示的标准周期列表。
func TimeframesLabel() string {
	parts := make([]string, 0, len(AllTimeframes))
	for _, timeframe := range AllTimeframes {
		parts = append(parts, timeframe.String())
	}
	return strings.Join(parts, ", ")
}
