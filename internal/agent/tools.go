package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"github.com/guanlanai/guanlanai/internal/analysis"
	"github.com/guanlanai/guanlanai/internal/llm"
	"github.com/guanlanai/guanlanai/internal/market"
)

// maxToolTimeframes 是一次工具调用最多拉取的周期数，避免上下文和上游压力失控。
const maxToolTimeframes = 6

// toolDefinitions 返回暴露给模型的只读工具。
// 这里刻意不注册交易、账户、转账等任何有副作用的工具。
func (s *Service) toolDefinitions() []llm.Tool {
	return []llm.Tool{
		{
			Name:        "resolve_symbol",
			Description: "把用户输入的币种代码解析成规范化交易对，例如 BTC -> BTCUSDT。",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"query": {"type": "string", "description": "币种代码或交易对，例如 BTC、ETH、BTCUSDT"}
				},
				"required": ["query"],
				"additionalProperties": false
			}`),
		},
		{
			Name:        "get_market_snapshot",
			Description: "拉取指定币种在指定周期的行情快照，只返回价格和每根序列的摘要，不返回完整 K 线。",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"symbol": {"type": "string", "description": "币种代码或交易对"},
					"timeframes": {
						"type": "array",
						"items": {"type": "string", "enum": ["1m","5m","15m","30m","1h","2h","4h","6h","12h","1D","1W","1M"]},
						"description": "需要拉取的周期，最多 6 个"
					}
				},
				"required": ["symbol", "timeframes"],
				"additionalProperties": false
			}`),
		},
		{
			Name:        "get_technical_analysis",
			Description: "拉取指定币种、单个周期的行情并计算确定性技术指标，返回结构化技术面报告。",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"symbol": {"type": "string", "description": "币种代码或交易对"},
					"timeframe": {"type": "string", "enum": ["1m","5m","15m","30m","1h","2h","4h","6h","12h","1D","1W","1M"]},
					"indicators": {
						"type": "array",
						"items": {"type": "string", "enum": ["ema","rsi","macd","atr","supertrend","volume","support_resistance"]},
						"description": "需要计算的指标组；省略表示全部"
					}
				},
				"required": ["symbol", "timeframe"],
				"additionalProperties": false
			}`),
		},
	}
}

type resolveSymbolArgs struct {
	Query string `json:"query"`
}

type marketSnapshotArgs struct {
	Symbol     string   `json:"symbol"`
	Timeframes []string `json:"timeframes"`
}

type technicalAnalysisArgs struct {
	Symbol     string   `json:"symbol"`
	Timeframe  string   `json:"timeframe"`
	Indicators []string `json:"indicators"`
}

// executeTool 执行一次只读工具调用；错误以 JSON 形式返回给模型，避免中断整个对话。
func (s *Service) executeTool(ctx context.Context, mode string, sessionID int64, cache map[string]string, call llm.ToolCall) string {
	key := call.Name + "|" + call.Arguments
	if result, ok := cache[key]; ok {
		return result
	}

	var result string
	switch call.Name {
	case "resolve_symbol":
		result = s.toolResolveSymbol(ctx, call.Arguments)
	case "get_market_snapshot":
		result = s.toolMarketSnapshot(ctx, sessionID, call.Arguments)
	case "get_technical_analysis":
		result = s.toolTechnicalAnalysis(ctx, mode, sessionID, call.Arguments)
	default:
		result = toolError(fmt.Errorf("未知工具 %q", call.Name))
	}
	cache[key] = result
	return result
}

func (s *Service) toolResolveSymbol(ctx context.Context, raw string) string {
	var args resolveSymbolArgs
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return toolError(fmt.Errorf("参数格式错误: %w", err))
	}
	symbol, err := s.resolver.Resolve(ctx, args.Query)
	if err != nil {
		return toolError(err)
	}
	return toolJSON(map[string]any{
		"symbol": symbol.Code,
		"pair":   symbol.Pair,
		"base":   symbol.Base,
		"quote":  symbol.Quote,
		"market": symbol.Market,
	})
}

func (s *Service) toolMarketSnapshot(ctx context.Context, sessionID int64, raw string) string {
	var args marketSnapshotArgs
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return toolError(fmt.Errorf("参数格式错误: %w", err))
	}
	timeframes, err := parseToolTimeframes(args.Timeframes)
	if err != nil {
		return toolError(err)
	}
	symbol, err := s.resolver.Resolve(ctx, args.Symbol)
	if err != nil {
		return toolError(err)
	}

	snapshot, ok := s.snapshots.GetMany(sessionID, symbol, timeframes)
	if !ok {
		snapshot, err = s.provider.Snapshot(ctx, symbol, timeframes)
		if err != nil {
			return toolError(err)
		}
		s.snapshots.Put(sessionID, snapshot)
	}

	series := make([]map[string]any, 0, len(timeframes))
	for _, timeframe := range timeframes {
		item, ok := snapshot.Series[timeframe]
		if !ok || len(item.Candles) == 0 {
			series = append(series, map[string]any{
				"timeframe": timeframe.String(),
				"available": false,
			})
			continue
		}
		first := item.Candles[0].Close
		last := item.Candles[len(item.Candles)-1]
		change := 0.0
		if first != 0 {
			change = (last.Close - first) / first * 100
		}
		series = append(series, map[string]any{
			"timeframe":  timeframe.String(),
			"available":  true,
			"as_of":      item.AsOf,
			"partial":    item.Partial,
			"candles":    len(item.Candles),
			"last_close": last.Close,
			"high":       last.High,
			"low":        last.Low,
			"change_pct": round2(change),
		})
	}
	return toolJSON(map[string]any{
		"symbol":     symbol.Code,
		"pair":       symbol.Pair,
		"price":      snapshot.Price,
		"change_pct": snapshot.ChangePct,
		"source":     snapshot.Source,
		"fetched_at": snapshot.FetchedAt,
		"series":     series,
	})
}

func (s *Service) toolTechnicalAnalysis(ctx context.Context, mode string, sessionID int64, raw string) string {
	var args technicalAnalysisArgs
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return toolError(fmt.Errorf("参数格式错误: %w", err))
	}
	timeframe, err := market.ParseTimeframe(args.Timeframe)
	if err != nil {
		return toolError(err)
	}
	indicators, err := analysis.ParseIndicators(strings.Join(args.Indicators, ","))
	if err != nil {
		return toolError(err)
	}
	symbol, err := s.resolver.Resolve(ctx, args.Symbol)
	if err != nil {
		return toolError(err)
	}

	snapshot, ok := s.snapshots.GetMany(sessionID, symbol, []market.Timeframe{timeframe})
	if !ok {
		snapshot, err = s.provider.Snapshot(ctx, symbol, []market.Timeframe{timeframe})
		if err != nil {
			return toolError(err)
		}
		s.snapshots.Put(sessionID, snapshot)
	}
	report, err := analysis.BuildReport(snapshot, mode, timeframe, indicators)
	if err != nil {
		return toolError(err)
	}
	return toolJSON(report)
}

func parseToolTimeframes(values []string) ([]market.Timeframe, error) {
	if len(values) == 0 {
		return nil, fmt.Errorf("timeframes 不能为空")
	}
	if len(values) > maxToolTimeframes {
		return nil, fmt.Errorf("timeframes 最多 %d 个", maxToolTimeframes)
	}
	result := make([]market.Timeframe, 0, len(values))
	seen := make(map[market.Timeframe]bool, len(values))
	for _, value := range values {
		timeframe, err := market.ParseTimeframe(value)
		if err != nil {
			return nil, err
		}
		if seen[timeframe] {
			continue
		}
		seen[timeframe] = true
		result = append(result, timeframe)
	}
	return result, nil
}

func toolJSON(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return toolError(fmt.Errorf("序列化工具结果: %w", err))
	}
	return string(data)
}

func toolError(err error) string {
	return toolJSON(map[string]string{"error": err.Error()})
}

func round2(value float64) float64 {
	return math.Round(value*100) / 100
}
