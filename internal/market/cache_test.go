package market

import (
	"testing"
	"time"
)

func TestSeriesTTL(t *testing.T) {
	tests := []struct {
		timeframe Timeframe
		want      time.Duration
	}{
		{TF15m, 2 * time.Minute},
		{TF4h, 10 * time.Minute},
		{TF1D, 30 * time.Minute},
		{TF1W, 30 * time.Minute},
		{TF1M, 2 * time.Hour},
	}
	for _, tt := range tests {
		if got := SeriesTTL(tt.timeframe); got != tt.want {
			t.Fatalf("SeriesTTL(%s) = %v, want %v", tt.timeframe, got, tt.want)
		}
	}
}

func TestSnapshotCacheReusesWithinTTL(t *testing.T) {
	cache := NewSnapshotCache(10)
	now := time.Now().UTC()
	cache.now = func() time.Time { return now }
	symbol := Symbol{Code: "BTC", Pair: "BTCUSDT"}
	snapshot := &Snapshot{
		Symbol:    symbol,
		FetchedAt: now,
		Series: map[Timeframe]Series{
			TF1D: {Timeframe: TF1D, Candles: []Candle{{Close: 100}}},
		},
	}
	cache.Put(1, snapshot)

	got, ok := cache.Get(1, symbol, TF1D)
	if !ok || got != snapshot {
		t.Fatal("TTL 内应命中快照")
	}
	if got.FetchedAt != now {
		t.Fatalf("命中不应改变 FetchedAt: %v", got.FetchedAt)
	}

	now = now.Add(31 * time.Minute)
	if _, ok := cache.Get(1, symbol, TF1D); ok {
		t.Fatal("超过 TTL 后不应命中")
	}
}

func TestSnapshotCacheGetMany(t *testing.T) {
	cache := NewSnapshotCache(10)
	now := time.Now().UTC()
	cache.now = func() time.Time { return now }
	symbol := Symbol{Pair: "BTCUSDT"}
	snapshot := &Snapshot{
		Symbol:    symbol,
		FetchedAt: now,
		Series: map[Timeframe]Series{
			TF1W: {Timeframe: TF1W},
			TF1D: {Timeframe: TF1D},
			TF1h: {Timeframe: TF1h},
		},
	}
	cache.Put(1, snapshot)

	if _, ok := cache.GetMany(1, symbol, []Timeframe{TF1W, TF1D, TF1h}); !ok {
		t.Fatal("三个周期齐全时应命中")
	}
	if _, ok := cache.GetMany(1, symbol, []Timeframe{TF1W, TF4h}); ok {
		t.Fatal("缺少任一周期的快照不应命中")
	}

	// 1h 的 TTL 最短，过期后整组不再命中。
	now = now.Add(11 * time.Minute)
	if _, ok := cache.GetMany(1, symbol, []Timeframe{TF1W, TF1D, TF1h}); ok {
		t.Fatal("任一周期过期后不应命中")
	}
}

func TestSnapshotCacheMatchesSymbolAndTimeframe(t *testing.T) {
	cache := NewSnapshotCache(10)
	now := time.Now().UTC()
	cache.now = func() time.Time { return now }
	snapshot := &Snapshot{
		Symbol:    Symbol{Pair: "BTCUSDT"},
		FetchedAt: now,
		Series:    map[Timeframe]Series{TF1D: {Timeframe: TF1D}},
	}
	cache.Put(1, snapshot)

	if _, ok := cache.Get(1, Symbol{Pair: "ETHUSDT"}, TF1D); ok {
		t.Fatal("不同交易对不应命中")
	}
	if _, ok := cache.Get(1, Symbol{Pair: "BTCUSDT"}, TF4h); ok {
		t.Fatal("快照没有该周期时不应命中")
	}
	if _, ok := cache.Get(2, Symbol{Pair: "BTCUSDT"}, TF1D); ok {
		t.Fatal("不同会话不应命中")
	}
}

func TestSnapshotCacheEvictsOldestSession(t *testing.T) {
	cache := NewSnapshotCache(2)
	now := time.Now().UTC()
	cache.now = func() time.Time { return now }
	snapshot := func(pair string) *Snapshot {
		return &Snapshot{Symbol: Symbol{Pair: pair}, FetchedAt: now, Series: map[Timeframe]Series{TF1h: {}}}
	}
	cache.Put(1, snapshot("BTCUSDT"))
	now = now.Add(time.Minute)
	cache.Put(2, snapshot("ETHUSDT"))
	now = now.Add(time.Minute)
	cache.Put(3, snapshot("SOLUSDT"))

	if _, ok := cache.Get(1, Symbol{Pair: "BTCUSDT"}, TF1h); ok {
		t.Fatal("最旧会话应被淘汰")
	}
	if _, ok := cache.Get(3, Symbol{Pair: "SOLUSDT"}, TF1h); !ok {
		t.Fatal("最新会话应保留")
	}
}
