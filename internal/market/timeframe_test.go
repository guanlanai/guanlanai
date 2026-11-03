package market

import (
	"errors"
	"testing"
)

func TestParseTimeframe(t *testing.T) {
	tests := []struct {
		input string
		want  Timeframe
	}{
		{"1m", TF1m},
		{"1h", TF1h},
		{"1D", TF1D},
		{"1W", TF1W},
		{"1M", TF1M},
		{"D", TF1D},
		{"W", TF1W},
		{"M", TF1M},
		{"1d", TF1D},
		{"1w", TF1W},
		{" 4h ", TF4h},
	}
	for _, tt := range tests {
		got, err := ParseTimeframe(tt.input)
		if err != nil {
			t.Fatalf("ParseTimeframe(%q) error = %v", tt.input, err)
		}
		if got != tt.want {
			t.Fatalf("ParseTimeframe(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestParseTimeframeRejectsInvalid(t *testing.T) {
	for _, input := range []string{"", "2d", "1y", "d1", "1H2"} {
		if _, err := ParseTimeframe(input); !errors.Is(err, ErrInvalidTimeframe) {
			t.Fatalf("ParseTimeframe(%q) error = %v, want ErrInvalidTimeframe", input, err)
		}
	}
}

func TestDefaultTimeframe(t *testing.T) {
	if DefaultTimeframe != TF1D {
		t.Fatalf("DefaultTimeframe = %q, want 1D", DefaultTimeframe)
	}
	if !DefaultTimeframe.Valid() {
		t.Fatal("默认周期必须是标准周期")
	}
}
