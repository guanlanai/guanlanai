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
// CloseTime 用于区分已收盘和仍在形成的当前 K 线；零值视为已收盘。
type Candle struct {
	OpenTime  time.Time
	CloseTime time.Time
	Open      float64
	High      float64
	Low       float64
	Close     float64
	Volume    float64
}

// Series 是某个周期的 K 线序列及其数据质量。
type Series struct {
	Timeframe Timeframe
	Candles   []Candle
	Partial   bool
	AsOf      time.Time
}

// Snapshot 是一次分析所需的行情快照。
// 按“分析计划需要的周期集合”建模；单周期分析只包含一个 Series。
type Snapshot struct {
	Symbol    Symbol
	Series    map[Timeframe]Series
	Price     float64
	ChangePct float64
	FetchedAt time.Time
	Source    string
}

// Resolver 负责把用户输入解析成交易对。
type Resolver interface {
	Resolve(ctx context.Context, query string) (Symbol, error)
}

// Provider 负责拉取一次分析所需的多个周期行情。
type Provider interface {
	Snapshot(ctx context.Context, symbol Symbol, timeframes []Timeframe) (*Snapshot, error)
}
