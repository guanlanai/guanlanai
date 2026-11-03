package agent

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/guanlanai/guanlanai/internal/analysis"
	"github.com/guanlanai/guanlanai/internal/llm"
	"github.com/guanlanai/guanlanai/internal/market"
)

// goldenSuite 是固定行情 fixture + 固定模型输出的回归基线。
// 它不评估模型文采，只检查管道结构、周期引用和本地业务校验是否稳定。
type goldenSuite struct {
	Candles goldenCandles `json:"candles"`
	Cases   []goldenCase  `json:"cases"`
}

type goldenCandles struct {
	Count     int     `json:"count"`
	Start     float64 `json:"start"`
	Drift     float64 `json:"drift"`
	Amplitude float64 `json:"amplitude"`
	Period    float64 `json:"period"`
}

type goldenCase struct {
	Name               string          `json:"name"`
	Query              string          `json:"query"`
	Strategy           string          `json:"strategy"`
	Timeframe          string          `json:"timeframe"`
	Mode               string          `json:"mode"`
	Trend              string          `json:"trend"`
	Opinion            json.RawMessage `json:"opinion"`
	WantError          string          `json:"want_error"`
	WantDirection      string          `json:"want_direction"`
	WantTimeframes     []string        `json:"want_timeframes"`
	WantPromptContains []string        `json:"want_prompt_contains"`
}

type goldenProvider struct {
	candles goldenCandles
	trend   string
	calls   int
}

func (p *goldenProvider) Snapshot(_ context.Context, symbol market.Symbol, timeframes []market.Timeframe) (*market.Snapshot, error) {
	p.calls++
	asOf := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	series := make(map[market.Timeframe]market.Series, len(timeframes))
	for _, timeframe := range timeframes {
		candles := p.generate(asOf)
		series[timeframe] = market.Series{
			Timeframe: timeframe,
			Candles:   candles,
			AsOf:      candles[len(candles)-1].OpenTime.Add(time.Hour),
		}
	}
	last := p.generate(asOf)[p.candles.Count-1]
	first := p.generate(asOf)[0]
	change := 0.0
	if first.Close != 0 {
		change = (last.Close - first.Close) / first.Close * 100
	}
	return &market.Snapshot{
		Symbol:    symbol,
		Series:    series,
		Price:     last.Close,
		ChangePct: change,
		FetchedAt: asOf,
		Source:    "golden",
	}, nil
}

func (p *goldenProvider) generate(asOf time.Time) []market.Candle {
	direction := 1.0
	switch p.trend {
	case "down":
		direction = -1
	case "flat":
		direction = 0
	}
	base := p.candles.Start
	if direction < 0 {
		base += float64(p.candles.Count) * p.candles.Drift
	}
	candles := make([]market.Candle, p.candles.Count)
	for i := range candles {
		center := base +
			direction*p.candles.Drift*float64(i) +
			p.candles.Amplitude*math.Sin(float64(i)/p.candles.Period)
		closePrice := center + direction*p.candles.Drift*0.4
		candles[i] = market.Candle{
			OpenTime: asOf.Add(time.Duration(i) * time.Hour),
			Open:     center,
			High:     math.Max(center, closePrice) + 1.5,
			Low:      math.Min(center, closePrice) - 1.5,
			Close:    closePrice,
			Volume:   1000,
		}
	}
	return candles
}

type goldenResolver struct{}

func (goldenResolver) Resolve(_ context.Context, query string) (market.Symbol, error) {
	code := strings.ToUpper(strings.TrimSpace(query))
	switch code {
	case "BTC", "ETH", "SOL":
		return market.Symbol{Code: code, Pair: code + "USDT", Base: code, Quote: "USDT", Market: "crypto"}, nil
	default:
		return market.Symbol{}, market.ErrSymbolNotFound
	}
}

func TestGoldenCases(t *testing.T) {
	data, err := os.ReadFile("testdata/golden_cases.json")
	if err != nil {
		t.Fatalf("读取 fixture: %v", err)
	}
	var suite goldenSuite
	if err := json.Unmarshal(data, &suite); err != nil {
		t.Fatalf("解析 fixture: %v", err)
	}
	if len(suite.Cases) == 0 {
		t.Fatal("fixture 没有用例")
	}

	for _, testCase := range suite.Cases {
		t.Run(testCase.Name, func(t *testing.T) {
			strategy, err := analysis.ParseStrategy(testCase.Strategy)
			if err != nil {
				t.Fatalf("策略: %v", err)
			}
			timeframe, err := market.ParseTimeframe(testCase.Timeframe)
			if err != nil {
				t.Fatalf("周期: %v", err)
			}
			response := string(testCase.Opinion)
			completer := &scriptedCompleter{responses: []*llm.CompletionResponse{
				{Content: response},
				{Content: response},
			}}
			provider := &goldenProvider{candles: suite.Candles, trend: testCase.Trend}
			service := New(goldenResolver{}, provider, completer, "", 1200, 4, testLogger())

			result, analyzeErr := service.Analyze(context.Background(), testUser(), "sk-test", testCase.Query,
				testCase.Mode, strategy, timeframe, analysis.DefaultIndicators(), 0)

			if testCase.WantError == "invalid_output" {
				if !errors.Is(analyzeErr, ErrInvalidOutput) {
					t.Fatalf("错误 = %v, want ErrInvalidOutput", analyzeErr)
				}
				if completer.calls != 2 {
					t.Fatalf("非法输出应触发一次修复，calls = %d", completer.calls)
				}
			} else {
				if analyzeErr != nil {
					t.Fatalf("Analyze() error = %v", analyzeErr)
				}
				if result.Opinion.Direction != testCase.WantDirection {
					t.Fatalf("direction = %q, want %q", result.Opinion.Direction, testCase.WantDirection)
				}
			}

			if len(completer.requests) == 0 {
				t.Fatal("没有记录到 LLM 请求")
			}
			first := completer.requests[0]
			if first.JSONSchema == nil {
				t.Fatal("分析请求应携带 JSON Schema")
			}
			for _, want := range testCase.WantPromptContains {
				if !strings.Contains(first.User, want) {
					t.Fatalf("prompt 缺少 %q\n%s", want, first.User)
				}
			}
			if testCase.WantError != "" {
				return
			}

			report := result.Report
			if report == nil {
				t.Fatal("缺少技术面报告")
			}
			var gotTimeframes []string
			for _, screen := range report.Screens {
				gotTimeframes = append(gotTimeframes, screen.Timeframe)
				if screen.AsOf.IsZero() {
					t.Fatalf("screen %s 缺少 as_of", screen.Timeframe)
				}
			}
			if strings.Join(gotTimeframes, ",") != strings.Join(testCase.WantTimeframes, ",") {
				t.Fatalf("timeframes = %#v, want %#v", gotTimeframes, testCase.WantTimeframes)
			}

			if report.Alignment != nil && !report.Alignment.Conflict && report.Alignment.Tide != "sideways" && report.Alignment.Tide != "" {
				want := map[string]string{"up": "bullish", "down": "bearish"}[report.Alignment.Tide]
				if want != "" && result.Opinion.Direction != want {
					t.Fatalf("方向与 alignment 不一致: tide=%s, direction=%s", report.Alignment.Tide, result.Opinion.Direction)
				}
			}
		})
	}
}
