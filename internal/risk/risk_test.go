package risk

import (
	"errors"
	"testing"
)

func TestCalculateLong(t *testing.T) {
	position, err := Calculate("bullish", 100, 102, 95, 100)
	if err != nil {
		t.Fatalf("Calculate() error = %v", err)
	}
	if position.EntryPrice != 101 {
		t.Fatalf("EntryPrice = %v, want 101", position.EntryPrice)
	}
	if position.Quantity != 100.0/6.0 {
		t.Fatalf("Quantity = %v", position.Quantity)
	}
	if position.RiskAmount != 100 || position.Leverage != 1 {
		t.Fatalf("position = %#v", position)
	}
	if position.Notional != position.Quantity*101 {
		t.Fatalf("Notional = %v", position.Notional)
	}
}

func TestCalculateShort(t *testing.T) {
	position, err := Calculate("bearish", 98, 100, 105, 50)
	if err != nil {
		t.Fatalf("Calculate() error = %v", err)
	}
	if position.EntryPrice != 99 || position.Quantity != 50.0/6.0 {
		t.Fatalf("position = %#v", position)
	}
}

func TestCalculateRejectsMissingInputs(t *testing.T) {
	if _, err := Calculate("bullish", 100, 102, 95, 0); !errors.Is(err, ErrNoRiskPreference) {
		t.Fatalf("未设置风险金额 error = %v", err)
	}
	if _, err := Calculate("neutral", 100, 102, 95, 100); !errors.Is(err, ErrNoTradePlan) {
		t.Fatalf("中性观点 error = %v", err)
	}
	if _, err := Calculate("bullish", 100, 102, 101, 100); !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("多头止损高于入场 error = %v", err)
	}
	if _, err := Calculate("bearish", 98, 100, 99, 100); !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("空头止损低于入场 error = %v", err)
	}
	if _, err := Calculate("bullish", 0, 102, 95, 100); !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("入场价格缺失 error = %v", err)
	}
}
