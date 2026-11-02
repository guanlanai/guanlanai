package agent

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/guanlanai/guanlanai/internal/analysis"
	"github.com/guanlanai/guanlanai/internal/llm"
	"github.com/guanlanai/guanlanai/internal/market"
	"github.com/guanlanai/guanlanai/internal/user"
)

type fakeResolver struct{}

func (fakeResolver) Resolve(_ context.Context, query string) (market.Symbol, error) {
	if strings.EqualFold(query, "NOPE") {
		return market.Symbol{}, market.ErrSymbolNotFound
	}
	return market.Symbol{Code: "BTC", Pair: "BTCUSDT", Base: "BTC", Quote: "USDT", Market: "crypto"}, nil
}

type fakeProvider struct{}

func (fakeProvider) Snapshot(_ context.Context, symbol market.Symbol, _ []string) (*market.Snapshot, error) {
	candles := make([]market.Candle, 300)
	for i := range candles {
		price := 100 + float64(i)
		candles[i] = market.Candle{
			OpenTime: time.Unix(int64(i*3600), 0).UTC(),
			Open:     price,
			High:     price + 1,
			Low:      price - 1,
			Close:    price,
			Volume:   1000,
		}
	}
	return &market.Snapshot{
		Symbol:     symbol,
		Price:      399,
		ChangePct:  1.2,
		Timeframes: map[string][]market.Candle{"1h": candles},
		FetchedAt:  time.Now().UTC(),
		Source:     "test",
	}, nil
}

type fakeCompleter struct {
	responses []string
	errs      []error
	calls     int
	requests  []llm.CompletionRequest
}

func (f *fakeCompleter) Complete(_ context.Context, req llm.CompletionRequest) (*llm.CompletionResponse, error) {
	f.calls++
	f.requests = append(f.requests, req)
	index := f.calls - 1
	if index < len(f.errs) && f.errs[index] != nil {
		return nil, f.errs[index]
	}
	if index >= len(f.responses) {
		index = len(f.responses) - 1
	}
	return &llm.CompletionResponse{Content: f.responses[index], Model: req.Model, PromptTokens: 10, CompletionTokens: 5}, nil
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func validOpinion() string {
	return `{
		"symbol":"BTC","pair":"BTCUSDT","market":"crypto","mode":"swing",
		"direction":"bullish","confidence":"medium","summary":"趋势向上",
		"key_levels":{"support":[390],"resistance":[410]},
		"trade_plan":{"entry_low":395,"entry_high":400,"stop_loss":380,"take_profit":[420],"risk_reward":2.0},
		"reasons":["EMA 多头排列"],"risks":["跌破止损则失效"],
		"data_as_of":"2026-11-02T00:00:00Z","disclaimer":"仅供参考"
	}`
}

func testUser() *user.User {
	return &user.User{
		TelegramUserID: 1,
		APIBaseURL:     "https://api.openai.com/v1",
		Model:          "gpt-5-mini",
		Mode:           "swing",
	}
}

func TestAnalyze(t *testing.T) {
	completer := &fakeCompleter{responses: []string{validOpinion()}}
	service := New(fakeResolver{}, fakeProvider{}, completer, "", 1200, 4, testLogger())

	result, err := service.Analyze(context.Background(), testUser(), "sk-test", "BTC", "swing")
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if result.Opinion.Direction != "bullish" || result.Opinion.Symbol != "BTC" {
		t.Fatalf("opinion = %#v", result.Opinion)
	}
	if result.Report.Pair != "BTCUSDT" || result.Report.Source != "test" {
		t.Fatalf("report = %#v", result.Report)
	}
	if completer.calls != 1 || !completer.requests[0].JSONMode {
		t.Fatalf("completer = %#v", completer)
	}
}

func TestAnalyzeRetriesInvalidOutput(t *testing.T) {
	completer := &fakeCompleter{responses: []string{`{"bad":true}`, validOpinion()}}
	service := New(fakeResolver{}, fakeProvider{}, completer, "", 1200, 4, testLogger())

	result, err := service.Analyze(context.Background(), testUser(), "sk-test", "BTC", "swing")
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if result.Opinion.Direction != "bullish" {
		t.Fatalf("opinion = %#v", result.Opinion)
	}
	if completer.calls != 2 {
		t.Fatalf("calls = %d, want 2", completer.calls)
	}
	if !strings.Contains(completer.requests[1].User, "上一次输出") {
		t.Fatalf("修复提示缺少说明: %q", completer.requests[1].User)
	}
}

func TestAnalyzeInvalidOutput(t *testing.T) {
	completer := &fakeCompleter{responses: []string{`{"bad":true}`, `{"bad":true}`}}
	service := New(fakeResolver{}, fakeProvider{}, completer, "", 1200, 4, testLogger())

	_, err := service.Analyze(context.Background(), testUser(), "sk-test", "BTC", "swing")
	if !errors.Is(err, ErrInvalidOutput) {
		t.Fatalf("Analyze() error = %v, want ErrInvalidOutput", err)
	}
}

func TestAnalyzeNoModel(t *testing.T) {
	usr := testUser()
	usr.Model = ""
	service := New(fakeResolver{}, fakeProvider{}, &fakeCompleter{}, "", 1200, 4, testLogger())

	_, err := service.Analyze(context.Background(), usr, "sk-test", "BTC", "swing")
	if !errors.Is(err, ErrNoModel) {
		t.Fatalf("Analyze() error = %v, want ErrNoModel", err)
	}
}

func TestParseOpinionRejectsBadPlan(t *testing.T) {
	report := &analysis.Report{Symbol: "BTC", Pair: "BTCUSDT", Market: "crypto", Mode: "swing", AsOf: time.Now()}
	_, err := ParseOpinion(`{
		"direction":"bullish","confidence":"medium","summary":"x",
		"trade_plan":{"entry_low":100,"entry_high":101,"stop_loss":110,"take_profit":[120],"risk_reward":2},
		"reasons":["r"],"risks":["x"]
	}`, report)
	if err == nil {
		t.Fatal("多头止损高于入场应当报错")
	}
}
