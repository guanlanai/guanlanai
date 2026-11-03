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
	"github.com/guanlanai/guanlanai/internal/session"
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

func (fakeProvider) Snapshot(_ context.Context, symbol market.Symbol, timeframes []market.Timeframe) (*market.Snapshot, error) {
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
	series := make(map[market.Timeframe]market.Series, len(timeframes))
	for _, timeframe := range timeframes {
		series[timeframe] = market.Series{
			Timeframe: timeframe,
			Candles:   candles,
			AsOf:      candles[len(candles)-1].OpenTime,
		}
	}
	return &market.Snapshot{
		Symbol:    symbol,
		Price:     399,
		ChangePct: 1.2,
		Series:    series,
		FetchedAt: time.Now().UTC(),
		Source:    "test",
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

	result, err := service.Analyze(context.Background(), testUser(), "sk-test", "BTC", "swing", analysis.StrategySingle, market.TF1D, analysis.DefaultIndicators(), 0)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if result.Opinion.Direction != "bullish" || result.Opinion.Symbol != "BTC" {
		t.Fatalf("opinion = %#v", result.Opinion)
	}
	if result.Report.Pair != "BTCUSDT" || result.Report.Source != "test" {
		t.Fatalf("report = %#v", result.Report)
	}
	if len(result.Report.Screens) != 1 || result.Report.Screens[0].Timeframe != "1D" {
		t.Fatalf("report screens = %#v", result.Report.Screens)
	}
	if completer.calls != 1 || !completer.requests[0].JSONMode {
		t.Fatalf("completer = %#v", completer)
	}
}

func TestAnalyzeUsesRequestedTimeframe(t *testing.T) {
	completer := &fakeCompleter{responses: []string{validOpinion()}}
	service := New(fakeResolver{}, fakeProvider{}, completer, "", 1200, 4, testLogger())

	result, err := service.Analyze(context.Background(), testUser(), "sk-test", "BTC", "swing", analysis.StrategySingle, market.TF4h, analysis.DefaultIndicators(), 0)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if len(result.Report.Screens) != 1 || result.Report.Screens[0].Timeframe != "4h" {
		t.Fatalf("report screens = %#v", result.Report.Screens)
	}
}

type countingProvider struct {
	calls      int
	timeframes []market.Timeframe
}

func (p *countingProvider) Snapshot(_ context.Context, symbol market.Symbol, timeframes []market.Timeframe) (*market.Snapshot, error) {
	p.calls++
	p.timeframes = append([]market.Timeframe(nil), timeframes...)
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
	series := make(map[market.Timeframe]market.Series, len(timeframes))
	for _, timeframe := range timeframes {
		series[timeframe] = market.Series{Timeframe: timeframe, Candles: candles, AsOf: candles[len(candles)-1].OpenTime}
	}
	return &market.Snapshot{
		Symbol:    symbol,
		Price:     399,
		ChangePct: 1.2,
		Series:    series,
		FetchedAt: time.Now().UTC(),
		Source:    "test",
	}, nil
}

func TestAnalyzeReusesSessionSnapshot(t *testing.T) {
	completer := &fakeCompleter{responses: []string{validOpinion(), validOpinion()}}
	provider := &countingProvider{}
	service := New(fakeResolver{}, provider, completer, "", 1200, 4, testLogger())

	if _, err := service.Analyze(context.Background(), testUser(), "sk-test", "BTC", "swing", analysis.StrategySingle, market.TF1D, analysis.DefaultIndicators(), 7); err != nil {
		t.Fatalf("第一次 Analyze() error = %v", err)
	}
	if _, err := service.Analyze(context.Background(), testUser(), "sk-test", "BTC", "swing", analysis.StrategySingle, market.TF1D, analysis.DefaultIndicators(), 7); err != nil {
		t.Fatalf("第二次 Analyze() error = %v", err)
	}
	if provider.calls != 1 {
		t.Fatalf("同一会话应复用快照，provider 调用 = %d, want 1", provider.calls)
	}
}

func TestAnalyzeDoesNotShareSnapshotAcrossSessions(t *testing.T) {
	completer := &fakeCompleter{responses: []string{validOpinion(), validOpinion()}}
	provider := &countingProvider{}
	service := New(fakeResolver{}, provider, completer, "", 1200, 4, testLogger())

	if _, err := service.Analyze(context.Background(), testUser(), "sk-test", "BTC", "swing", analysis.StrategySingle, market.TF1D, analysis.DefaultIndicators(), 1); err != nil {
		t.Fatalf("第一次 Analyze() error = %v", err)
	}
	if _, err := service.Analyze(context.Background(), testUser(), "sk-test", "BTC", "swing", analysis.StrategySingle, market.TF1D, analysis.DefaultIndicators(), 2); err != nil {
		t.Fatalf("第二次 Analyze() error = %v", err)
	}
	if provider.calls != 2 {
		t.Fatalf("不同会话不应共享快照，provider 调用 = %d, want 2", provider.calls)
	}
}

func TestAnalyzeTripleScreenReusesSnapshot(t *testing.T) {
	completer := &fakeCompleter{responses: []string{validOpinion(), validOpinion()}}
	provider := &countingProvider{}
	service := New(fakeResolver{}, provider, completer, "", 1200, 4, testLogger())

	result, err := service.Analyze(context.Background(), testUser(), "sk-test", "BTC", "swing",
		analysis.StrategyTripleScreen, market.TF4h, analysis.DefaultIndicators(), 9)
	if err != nil {
		t.Fatalf("第一次 Analyze() error = %v", err)
	}
	if len(provider.timeframes) != 3 ||
		provider.timeframes[0] != market.TF1W ||
		provider.timeframes[1] != market.TF1D ||
		provider.timeframes[2] != market.TF1h {
		t.Fatalf("请求周期 = %#v", provider.timeframes)
	}
	if result.Report.Strategy != "triple_screen" || len(result.Report.Screens) != 3 {
		t.Fatalf("report = %#v", result.Report)
	}

	if _, err := service.Analyze(context.Background(), testUser(), "sk-test", "BTC", "swing",
		analysis.StrategyTripleScreen, market.TF4h, []analysis.Indicator{analysis.IndicatorRSI}, 9); err != nil {
		t.Fatalf("第二次 Analyze() error = %v", err)
	}
	if provider.calls != 1 {
		t.Fatalf("三重滤网应复用快照，provider 调用 = %d", provider.calls)
	}
}

func TestAnalyzeRetriesInvalidOutput(t *testing.T) {
	completer := &fakeCompleter{responses: []string{`{"bad":true}`, validOpinion()}}
	service := New(fakeResolver{}, fakeProvider{}, completer, "", 1200, 4, testLogger())

	result, err := service.Analyze(context.Background(), testUser(), "sk-test", "BTC", "swing", analysis.StrategySingle, market.TF1D, analysis.DefaultIndicators(), 0)
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

func TestChatUsesConfiguredMaxOutput(t *testing.T) {
	completer := &fakeCompleter{responses: []string{"answer"}}
	service := New(fakeResolver{}, fakeProvider{}, completer, "", 4000, 4, testLogger())

	if _, err := service.Chat(context.Background(), testUser(), "sk-test", "能分析超级趋势吗", "context", 0); err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if len(completer.requests) != 1 || completer.requests[0].MaxOutputTokens != 4000 {
		t.Fatalf("Chat MaxOutputTokens = %#v", completer.requests)
	}
}

func TestChatEmptyResponse(t *testing.T) {
	completer := &fakeCompleter{responses: []string{""}}
	service := New(fakeResolver{}, fakeProvider{}, completer, "", 4000, 4, testLogger())

	_, err := service.Chat(context.Background(), testUser(), "sk-test", "问题", "context", 0)
	if !errors.Is(err, llm.ErrEmptyResponse) {
		t.Fatalf("Chat() error = %v, want ErrEmptyResponse", err)
	}
}

func TestAnalyzeInvalidOutput(t *testing.T) {
	completer := &fakeCompleter{responses: []string{`{"bad":true}`, `{"bad":true}`}}
	service := New(fakeResolver{}, fakeProvider{}, completer, "", 1200, 4, testLogger())

	_, err := service.Analyze(context.Background(), testUser(), "sk-test", "BTC", "swing", analysis.StrategySingle, market.TF1D, analysis.DefaultIndicators(), 0)
	if !errors.Is(err, ErrInvalidOutput) {
		t.Fatalf("Analyze() error = %v, want ErrInvalidOutput", err)
	}
}

func TestAnalyzeNoModel(t *testing.T) {
	usr := testUser()
	usr.Model = ""
	service := New(fakeResolver{}, fakeProvider{}, &fakeCompleter{}, "", 1200, 4, testLogger())

	_, err := service.Analyze(context.Background(), usr, "sk-test", "BTC", "swing", analysis.StrategySingle, market.TF1D, analysis.DefaultIndicators(), 0)
	if !errors.Is(err, ErrNoModel) {
		t.Fatalf("Analyze() error = %v, want ErrNoModel", err)
	}
}

func TestChatAndSummarize(t *testing.T) {
	completer := &fakeCompleter{responses: []string{"追问回答", "会话摘要"}}
	service := New(fakeResolver{}, fakeProvider{}, completer, "", 1200, 4, testLogger())

	chat, err := service.Chat(context.Background(), testUser(), "sk-test", "那止损呢？", "最近分析：{}", 0)
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if chat.Content != "追问回答" || chat.Usage == nil {
		t.Fatalf("chat = %#v", chat)
	}
	if completer.requests[0].JSONMode {
		t.Fatal("追问不应使用 JSON 模式")
	}

	summary, err := service.Summarize(context.Background(), testUser(), "sk-test", "", []session.Message{
		{Role: "user", Content: "分析 BTC"},
		{Role: "assistant", Content: "偏多"},
	})
	if err != nil {
		t.Fatalf("Summarize() error = %v", err)
	}
	if summary.Content != "会话摘要" {
		t.Fatalf("summary = %#v", summary)
	}
}

func TestParseOpinionTruncatesLongLists(t *testing.T) {
	report := &analysis.Report{Symbol: "BTC", Pair: "BTCUSDT", Market: "crypto", Mode: "swing", AsOf: time.Now()}
	opinion, err := ParseOpinion(`{
		"direction":"neutral","confidence":"medium","summary":"x",
		"reasons":["r1","r2","r3","r4","r5","r6"],"risks":["x1","x2","x3","x4","x5","x6"]
	}`, report)
	if err != nil {
		t.Fatalf("ParseOpinion() error = %v", err)
	}
	if len(opinion.Reasons) != 5 || len(opinion.Risks) != 5 {
		t.Fatalf("列表未裁剪: %d/%d", len(opinion.Reasons), len(opinion.Risks))
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
