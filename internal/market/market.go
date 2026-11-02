package market

import (
	"context"
	"errors"
	"time"
)

var (
	// ErrSymbolNotFound 表示交易对不存在或暂不支持。
	ErrSymbolNotFound = errors.New("交易对不存在或暂不支持")
	// ErrDataUnavailable 表示行情源暂时不可用。
	ErrDataUnavailable = errors.New("行情数据暂时不可用")
)

// Symbol 是规范化后的交易对。
type Symbol struct {
	Code   string // BTC
	Pair   string // BTCUSDT
	Base   string // BTC
	Quote  string // USDT
	Market string // crypto
}

// Candle 是统一后的 K 线。
type Candle struct {
	OpenTime time.Time
	Open     float64
	High     float64
	Low      float64
	Close    float64
	Volume   float64
}

// Snapshot 是一次分析所需的行情快照。
type Snapshot struct {
	Symbol     Symbol
	Price      float64
	ChangePct  float64
	Timeframes map[string][]Candle
	FetchedAt  time.Time
	Source     string
	Partial    bool
}

// Resolver 负责把用户输入解析成交易对。
type Resolver interface {
	Resolve(ctx context.Context, query string) (Symbol, error)
}

// Provider 负责拉取多周期行情。
type Provider interface {
	Snapshot(ctx context.Context, symbol Symbol, timeframes []string) (*Snapshot, error)
}

// TimeframesForMode 返回分析模式对应的周期。
func TimeframesForMode(mode string) []string {
	if mode == "short" {
		return []string{"15m", "1h", "4h"}
	}
	return []string{"1h", "4h", "1d", "1w"}
}
