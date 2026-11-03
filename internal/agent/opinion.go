package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/guanlanai/guanlanai/internal/analysis"
)

// ErrInvalidOutput 表示模型没有返回可用的结构化观点。
var ErrInvalidOutput = errors.New("模型输出格式异常")

// Opinion 是模型返回的结构化交易观点。
type Opinion struct {
	Symbol     string     `json:"symbol"`
	Pair       string     `json:"pair"`
	Market     string     `json:"market"`
	Mode       string     `json:"mode"`
	Direction  string     `json:"direction"`
	Confidence string     `json:"confidence"`
	Summary    string     `json:"summary"`
	KeyLevels  KeyLevels  `json:"key_levels"`
	TradePlan  *TradePlan `json:"trade_plan,omitempty"`
	Reasons    []string   `json:"reasons"`
	Risks      []string   `json:"risks"`
	DataAsOf   string     `json:"data_as_of"`
	Disclaimer string     `json:"disclaimer"`
}

type KeyLevels struct {
	Support    []float64 `json:"support"`
	Resistance []float64 `json:"resistance"`
}

type TradePlan struct {
	EntryLow   float64   `json:"entry_low"`
	EntryHigh  float64   `json:"entry_high"`
	StopLoss   float64   `json:"stop_loss"`
	TakeProfit []float64 `json:"take_profit"`
	RiskReward float64   `json:"risk_reward"`
}

const Disclaimer = "仅供参考，不构成投资建议。"

const ChatSystemPrompt = `你是观澜（GuanLanAI）的加密货币技术分析助手。
请基于用户提供的最近一次分析、摘要和对话回答追问。
只能使用给定数据，不要编造新闻、财报、基本面或链上数据。
数据中的 supertrend.value 是 SuperTrend 线位，direction=up 表示价格在线上方、趋势偏多，down 相反；bars_since_flip 是距离最近一次翻转的 K 线数。
当 strategy=triple_screen 时，alignment 是代码计算的跨周期结论，不要自行改写。
使用中文，回答简洁，明确观点失效条件。
不要保证收益，不构成投资建议；不要输出 JSON 或 Markdown 代码块。`

const SummarizeSystemPrompt = `请用中文压缩以下加密货币分析对话。
保留当前标的、最近观点、关键价位、用户偏好和未解决问题。
不要编造信息，不超过 300 字。只输出摘要正文。`

const SystemPrompt = `你是观澜（GuanLanAI）的加密货币技术分析助手。
只能使用用户提供的行情和指标数据，不要编造新闻、财报、基本面或链上数据。
数据中的 supertrend.value 是 SuperTrend 线位，direction=up 表示价格在线上方、趋势偏多，down 相反；bars_since_flip 是距离最近一次翻转的 K 线数。
当 strategy=triple_screen 时，screens 按 tide（潮汐，定方向）、wave（波浪，找回调）、entry（涟漪，找触发）排列；alignment 是代码按确定性规则计算的跨周期结论，不要自行改写，只负责解释一致性和冲突风险。每个 screen 的 as_of 独立，引用数据时必须保留对应周期。
不要保证收益，不推荐杠杆或满仓；必须给出观点失效条件。
方向必须是 bullish、bearish、neutral 之一；信心必须是 low、medium、high 之一。
中性观点可以不提供交易计划；非中性观点必须提供交易计划。
只输出一个合法 JSON 对象，不要输出 Markdown 代码块或解释文字。
JSON 结构：
{
  "symbol": "BTC",
  "pair": "BTCUSDT",
  "market": "crypto",
  "mode": "short|swing",
  "direction": "bullish|bearish|neutral",
  "confidence": "low|medium|high",
  "summary": "一句话结论",
  "key_levels": {"support": [数字], "resistance": [数字]},
  "trade_plan": {
    "entry_low": 数字,
    "entry_high": 数字,
    "stop_loss": 数字,
    "take_profit": [数字],
    "risk_reward": 数字
  } 或 null,
  "reasons": ["理由"],
  "risks": ["风险"],
  "data_as_of": "RFC3339 时间",
  "disclaimer": "仅供参考，不构成投资建议。"
}`

// ParseOpinion 解析并校验模型输出。
func ParseOpinion(content string, report *analysis.Report) (*Opinion, error) {
	content = extractJSON(content)
	if content == "" {
		return nil, ErrInvalidOutput
	}

	var opinion Opinion
	if err := json.Unmarshal([]byte(content), &opinion); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidOutput, err)
	}
	if err := validateOpinion(&opinion, report); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidOutput, err)
	}
	return &opinion, nil
}

func validateOpinion(opinion *Opinion, report *analysis.Report) error {
	if report == nil {
		return errors.New("缺少技术面报告")
	}
	opinion.Direction = strings.ToLower(strings.TrimSpace(opinion.Direction))
	opinion.Confidence = strings.ToLower(strings.TrimSpace(opinion.Confidence))
	opinion.Symbol = strings.ToUpper(strings.TrimSpace(opinion.Symbol))
	opinion.Pair = strings.ToUpper(strings.TrimSpace(opinion.Pair))
	opinion.Mode = strings.ToLower(strings.TrimSpace(opinion.Mode))

	switch opinion.Direction {
	case "bullish", "bearish", "neutral":
	default:
		return errors.New("direction 不合法")
	}
	switch opinion.Confidence {
	case "low", "medium", "high":
	default:
		return errors.New("confidence 不合法")
	}
	if opinion.Symbol != "" && opinion.Symbol != report.Symbol {
		return errors.New("symbol 与请求不一致")
	}
	if opinion.Pair != "" && opinion.Pair != report.Pair {
		return errors.New("pair 与请求不一致")
	}
	if strings.TrimSpace(opinion.Summary) == "" {
		return errors.New("summary 不能为空")
	}
	if len(opinion.Reasons) == 0 {
		return errors.New("reasons 不能为空")
	}
	if len(opinion.Risks) == 0 {
		return errors.New("risks 不能为空")
	}

	// 控制卡片长度，避免 Telegram 4096 限制和模型过度输出。
	opinion.Summary = truncateRunes(strings.TrimSpace(opinion.Summary), 500)
	opinion.Reasons = limitList(opinion.Reasons, 5, 240)
	opinion.Risks = limitList(opinion.Risks, 5, 240)
	if len(opinion.Reasons) == 0 || len(opinion.Risks) == 0 {
		return errors.New("reasons/risks 不能为空")
	}

	opinion.Symbol = report.Symbol
	opinion.Pair = report.Pair
	opinion.Market = report.Market
	opinion.Mode = report.Mode
	opinion.Disclaimer = Disclaimer
	if opinion.DataAsOf == "" {
		opinion.DataAsOf = report.AsOf.Format(time.RFC3339)
	}

	if opinion.TradePlan == nil {
		if opinion.Direction != "neutral" {
			return errors.New("非中性观点必须提供 trade_plan")
		}
		return nil
	}

	plan := opinion.TradePlan
	if !positive(plan.EntryLow) || !positive(plan.EntryHigh) || !positive(plan.StopLoss) || !positive(plan.RiskReward) {
		return errors.New("trade_plan 数字必须为正")
	}
	if plan.EntryLow > plan.EntryHigh {
		return errors.New("entry_low 不能大于 entry_high")
	}
	if len(plan.TakeProfit) == 0 {
		return errors.New("take_profit 不能为空")
	}
	for _, target := range plan.TakeProfit {
		if !positive(target) {
			return errors.New("take_profit 必须为正")
		}
	}

	switch opinion.Direction {
	case "bullish":
		if plan.StopLoss >= plan.EntryLow {
			return errors.New("多头止损必须低于入场")
		}
		for _, target := range plan.TakeProfit {
			if target <= plan.EntryHigh {
				return errors.New("多头目标必须高于入场")
			}
		}
	case "bearish":
		if plan.StopLoss <= plan.EntryHigh {
			return errors.New("空头止损必须高于入场")
		}
		for _, target := range plan.TakeProfit {
			if target >= plan.EntryLow {
				return errors.New("空头目标必须低于入场")
			}
		}
	}
	return nil
}

func positive(value float64) bool {
	return value > 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}

func truncateRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "…"
}

func limitList(values []string, maxItems, maxRunes int) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		result = append(result, truncateRunes(value, maxRunes))
		if len(result) == maxItems {
			break
		}
	}
	return result
}

func extractJSON(content string) string {
	content = strings.TrimSpace(content)
	if strings.HasPrefix(content, "```") {
		if index := strings.Index(content, "\n"); index >= 0 {
			content = content[index+1:]
		}
		content = strings.TrimSuffix(strings.TrimSpace(content), "```")
		content = strings.TrimSpace(content)
	}
	start := strings.Index(content, "{")
	end := strings.LastIndex(content, "}")
	if start >= 0 && end > start {
		return content[start : end+1]
	}
	return content
}
