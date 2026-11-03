package binance

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/guanlanai/guanlanai/internal/market"
	"github.com/guanlanai/guanlanai/internal/resilience"
)

func testServer(t *testing.T, status int) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/exchangeInfo", func(w http.ResponseWriter, _ *http.Request) {
		if status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"symbols":[
			{"symbol":"BTCUSDT","baseAsset":"BTC","quoteAsset":"USDT","status":"TRADING"},
			{"symbol":"ETHUSDT","baseAsset":"ETH","quoteAsset":"USDT","status":"TRADING"},
			{"symbol":"JUPUSDT","baseAsset":"JUP","quoteAsset":"USDT","status":"TRADING"},
			{"symbol":"BTCUPUSDT","baseAsset":"BTCUP","quoteAsset":"USDT","status":"TRADING"}
		]}`))
	})
	mux.HandleFunc("/api/v3/ticker/24hr", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"lastPrice":"100.5","priceChangePercent":"1.5"}`))
	})
	mux.HandleFunc("/api/v3/klines", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var builder strings.Builder
		builder.WriteString("[")
		for i := 0; i < 300; i++ {
			if i > 0 {
				builder.WriteString(",")
			}
			builder.WriteString(fmt.Sprintf(`[%d,"100","101","99","100.5","1000",0,0,0,0,0,0]`, i*3600000))
		}
		builder.WriteString("]")
		_, _ = w.Write([]byte(builder.String()))
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

func TestResolve(t *testing.T) {
	server := testServer(t, http.StatusOK)
	client := NewClient(server.URL, "", 2*time.Second)
	ctx := context.Background()

	tests := []struct {
		query string
		pair  string
		base  string
	}{
		{"BTC", "BTCUSDT", "BTC"},
		{"BTC/USDT", "BTCUSDT", "BTC"},
		{"BTC-USD", "BTCUSDT", "BTC"},
		{"BTCUSDT", "BTCUSDT", "BTC"},
		{"JUP", "JUPUSDT", "JUP"},
	}
	for _, tt := range tests {
		symbol, err := client.Resolve(ctx, tt.query)
		if err != nil {
			t.Fatalf("Resolve(%q) error = %v", tt.query, err)
		}
		if symbol.Pair != tt.pair || symbol.Base != tt.base || symbol.Quote != "USDT" {
			t.Fatalf("Resolve(%q) = %#v", tt.query, symbol)
		}
	}

	if _, err := client.Resolve(ctx, "BTCUP"); !errors.Is(err, market.ErrSymbolNotFound) {
		t.Fatalf("杠杆代币 error = %v", err)
	}
	if _, err := client.Resolve(ctx, "NOPE"); !errors.Is(err, market.ErrSymbolNotFound) {
		t.Fatalf("未知币种 error = %v", err)
	}
}

func TestSnapshot(t *testing.T) {
	server := testServer(t, http.StatusOK)
	client := NewClient(server.URL, "", 2*time.Second)
	ctx := context.Background()

	symbol, err := client.Resolve(ctx, "BTC")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	snapshot, err := client.Snapshot(ctx, symbol, []market.Timeframe{market.TF1h, market.TF4h})
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if snapshot.Price != 100.5 || snapshot.ChangePct != 1.5 {
		t.Fatalf("snapshot price/change = %v/%v", snapshot.Price, snapshot.ChangePct)
	}
	if len(snapshot.Series[market.TF1h].Candles) != 300 {
		t.Fatalf("1h candles = %d", len(snapshot.Series[market.TF1h].Candles))
	}
	if snapshot.Series[market.TF1h].Partial {
		t.Fatal("300 根 K 线不应标记 partial")
	}
	if snapshot.Series[market.TF1h].AsOf.IsZero() {
		t.Fatal("Series.AsOf 未设置")
	}
}

func TestIntervalMapping(t *testing.T) {
	tests := []struct {
		timeframe market.Timeframe
		interval  string
	}{
		{market.TF1m, "1m"},
		{market.TF1h, "1h"},
		{market.TF4h, "4h"},
		{market.TF1D, "1d"},
		{market.TF1W, "1w"},
		{market.TF1M, "1M"},
	}
	for _, tt := range tests {
		interval, err := intervalFor(tt.timeframe)
		if err != nil {
			t.Fatalf("intervalFor(%s) error = %v", tt.timeframe, err)
		}
		if interval != tt.interval {
			t.Fatalf("intervalFor(%s) = %q, want %q", tt.timeframe, interval, tt.interval)
		}
	}
}

func TestSnapshotSingleTimeframeRequestsOneKline(t *testing.T) {
	var klineCalls atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/ticker/24hr", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"lastPrice":"100.5","priceChangePercent":"1.5"}`))
	})
	mux.HandleFunc("/api/v3/klines", func(w http.ResponseWriter, _ *http.Request) {
		klineCalls.Add(1)
		_, _ = w.Write([]byte(`[[0,"100","101","99","100.5","1000",0,0,0,0,0,0]]`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	client := NewClient(server.URL, "", 2*time.Second)
	symbol := market.Symbol{Pair: "BTCUSDT"}
	snapshot, err := client.Snapshot(context.Background(), symbol, []market.Timeframe{market.TF1D})
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if klineCalls.Load() != 1 {
		t.Fatalf("K 线请求次数 = %d, want 1", klineCalls.Load())
	}
	if _, ok := snapshot.Series[market.TF1D]; !ok {
		t.Fatalf("缺少 1D series: %#v", snapshot.Series)
	}

	// 同一周期在 TTL 内复用，不重复请求 K 线。
	if _, err := client.Snapshot(context.Background(), symbol, []market.Timeframe{market.TF1D}); err != nil {
		t.Fatalf("第二次 Snapshot() error = %v", err)
	}
	if klineCalls.Load() != 1 {
		t.Fatalf("同一周期重复请求 K 线: %d", klineCalls.Load())
	}

	// 不同周期使用独立缓存键。
	if _, err := client.Snapshot(context.Background(), symbol, []market.Timeframe{market.TF4h}); err != nil {
		t.Fatalf("4h Snapshot() error = %v", err)
	}
	if klineCalls.Load() != 2 {
		t.Fatalf("不同周期应单独请求: %d", klineCalls.Load())
	}
}

func TestSnapshotReturnsPartialWhenOneTimeframeFails(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/ticker/24hr", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"lastPrice":"100.5","priceChangePercent":"1.5"}`))
	})
	mux.HandleFunc("/api/v3/klines", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("interval") == "1w" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"msg":"invalid"}`))
			return
		}
		_, _ = w.Write([]byte(`[[0,"100","101","99","100.5","1000",0,0,0,0,0,0]]`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	client := NewClient(server.URL, "", 2*time.Second)
	snapshot, err := client.Snapshot(context.Background(), market.Symbol{Pair: "BTCUSDT"},
		[]market.Timeframe{market.TF1W, market.TF1D, market.TF1h})
	if err != nil {
		t.Fatalf("多周期部分失败不应整体失败: %v", err)
	}
	if _, ok := snapshot.Series[market.TF1W]; ok {
		t.Fatal("失败的 1W 不应出现在快照中")
	}
	if _, ok := snapshot.Series[market.TF1D]; !ok {
		t.Fatal("1D 应保留")
	}
	if _, ok := snapshot.Series[market.TF1h]; !ok {
		t.Fatal("1h 应保留")
	}
}

func TestBreakerOpensAfterRepeatedFailures(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	client := NewClient(server.URL, "", time.Second)
	client.breaker = resilience.New(2, time.Minute)
	for i := 0; i < 2; i++ {
		if _, err := client.Resolve(context.Background(), "BTC"); err == nil {
			t.Fatal("预期失败")
		}
	}
	before := requests.Load()
	if _, err := client.Resolve(context.Background(), "BTC"); !errors.Is(err, market.ErrDataUnavailable) {
		t.Fatalf("熔断错误 = %v", err)
	}
	if after := requests.Load(); after != before {
		t.Fatalf("熔断后仍发请求: %d -> %d", before, after)
	}
}

func TestSnapshotFallsBack(t *testing.T) {
	bad := testServer(t, http.StatusUnavailableForLegalReasons)
	good := testServer(t, http.StatusOK)
	client := NewClient(bad.URL, good.URL, 2*time.Second)
	ctx := context.Background()

	symbol, err := client.Resolve(ctx, "BTC")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if _, err := client.Snapshot(ctx, symbol, []market.Timeframe{market.TF1h}); err != nil {
		t.Fatalf("Snapshot() fallback error = %v", err)
	}
}
