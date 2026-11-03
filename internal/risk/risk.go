package risk

import (
	"errors"
	"fmt"
	"math"
)

var (
	// ErrNoRiskPreference 表示用户还没有设置每笔最大亏损。
	ErrNoRiskPreference = errors.New("未设置每笔最大亏损")
	// ErrNoTradePlan 表示当前观点没有可执行的交易计划。
	ErrNoTradePlan = errors.New("没有可执行的交易计划")
	// ErrInvalidPlan 表示交易计划的价格关系不合法。
	ErrInvalidPlan = errors.New("交易计划价格不合法")
)

// Position 是由每笔风险金额和止损距离确定性计算出的现货仓位。
type Position struct {
	Direction  string  `json:"direction"`
	EntryPrice float64 `json:"entry_price"`
	StopLoss   float64 `json:"stop_loss"`
	RiskAmount float64 `json:"risk_amount"`
	Quantity   float64 `json:"quantity"`
	Notional   float64 `json:"notional"`
	Leverage   int     `json:"leverage"`
}

// Calculate 按“每笔风险金额 / |入场价 - 止损价|”计算现货数量。
// 入场价取交易计划区间中点；杠杆固定为 1，不做杠杆建议。
func Calculate(direction string, entryLow, entryHigh, stopLoss, maxLoss float64) (*Position, error) {
	if maxLoss <= 0 || math.IsNaN(maxLoss) || math.IsInf(maxLoss, 0) {
		return nil, ErrNoRiskPreference
	}
	if direction != "bullish" && direction != "bearish" {
		return nil, ErrNoTradePlan
	}
	if !positive(entryLow) || !positive(entryHigh) || !positive(stopLoss) || entryLow > entryHigh {
		return nil, fmt.Errorf("%w: 入场或止损价格缺失", ErrInvalidPlan)
	}

	entry := (entryLow + entryHigh) / 2
	switch direction {
	case "bullish":
		if stopLoss >= entryLow {
			return nil, fmt.Errorf("%w: 多头止损必须低于入场", ErrInvalidPlan)
		}
	case "bearish":
		if stopLoss <= entryHigh {
			return nil, fmt.Errorf("%w: 空头止损必须高于入场", ErrInvalidPlan)
		}
	}

	distance := math.Abs(entry - stopLoss)
	if distance <= 0 || math.IsNaN(distance) || math.IsInf(distance, 0) {
		return nil, fmt.Errorf("%w: 入场与止损距离必须大于 0", ErrInvalidPlan)
	}

	quantity := maxLoss / distance
	notional := quantity * entry
	if !positive(quantity) || !positive(notional) || math.IsInf(quantity, 0) || math.IsInf(notional, 0) {
		return nil, fmt.Errorf("%w: 计算出的仓位不合法", ErrInvalidPlan)
	}

	return &Position{
		Direction:  direction,
		EntryPrice: entry,
		StopLoss:   stopLoss,
		RiskAmount: maxLoss,
		Quantity:   quantity,
		Notional:   notional,
		Leverage:   1,
	}, nil
}

func positive(value float64) bool {
	return value > 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}
