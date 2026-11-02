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
	snapshot, err := client.Snapshot(ctx, symbol, []string{"1h", "4h"})
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if snapshot.Price != 100.5 || snapshot.ChangePct != 1.5 {
		t.Fatalf("snapshot price/change = %v/%v", snapshot.Price, snapshot.ChangePct)
	}
	if len(snapshot.Timeframes["1h"]) != 300 {
		t.Fatalf("1h candles = %d", len(snapshot.Timeframes["1h"]))
	}
	if snapshot.Partial {
		t.Fatal("300 根 K 线不应标记 partial")
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
	if _, err := client.Snapshot(ctx, symbol, []string{"1h"}); err != nil {
		t.Fatalf("Snapshot() fallback error = %v", err)
	}
}
