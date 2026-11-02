package render

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/guanlanai/guanlanai/internal/agent"
	"github.com/guanlanai/guanlanai/internal/analysis"
)

// OpinionCard 把结构化观点渲染成 Telegram HTML 卡片。
func OpinionCard(report *analysis.Report, opinion *agent.Opinion) string {
	if report == nil || opinion == nil {
		return "分析结果为空。"
	}

	var builder strings.Builder
	quote := report.Quote
	if quote == "" {
		quote = "USDT"
	}
	builder.WriteString("<b>")
	builder.WriteString(EscapeHTML(report.Symbol + "/" + quote))
	builder.WriteString(" · 加密 · ")
	builder.WriteString(modeLabel(report.Mode))
	builder.WriteString("</b>\n")

	builder.WriteString(directionEmoji(opinion.Direction))
	builder.WriteString(" <b>")
	builder.WriteString(directionLabel(opinion.Direction))
	builder.WriteString("</b>（")
	builder.WriteString(confidenceLabel(opinion.Confidence))
	builder.WriteString("信心）\n")
	builder.WriteString("现价：")
	builder.WriteString(formatPrice(report.Price))
	builder.WriteString(" ")
	builder.WriteString(EscapeHTML(quote))
	builder.WriteString("（")
	builder.WriteString(report.AsOf.UTC().Format("2006-01-02 15:04 UTC"))
	builder.WriteString("）\n")
	builder.WriteString("24h：")
	builder.WriteString(formatPercent(report.ChangePct))
	builder.WriteString("\n\n")

	builder.WriteString("<b>关键位</b>\n")
	builder.WriteString("支撑：")
	builder.WriteString(formatLevels(opinion.KeyLevels.Support))
	builder.WriteString("\n阻力：")
	builder.WriteString(formatLevels(opinion.KeyLevels.Resistance))
	builder.WriteString("\n")

	if opinion.TradePlan != nil {
		plan := opinion.TradePlan
		builder.WriteString("\n<b>计划</b>\n")
		builder.WriteString("入场：")
		builder.WriteString(formatRange(plan.EntryLow, plan.EntryHigh))
		builder.WriteString("\n止损：")
		builder.WriteString(formatPrice(plan.StopLoss))
		builder.WriteString("\n目标：")
		builder.WriteString(formatLevels(plan.TakeProfit))
		builder.WriteString("\n盈亏比：")
		builder.WriteString(strconv.FormatFloat(plan.RiskReward, 'f', 1, 64))
		builder.WriteString("\n")
	}

	if strings.TrimSpace(opinion.Summary) != "" {
		builder.WriteString("\n<b>结论</b>\n")
		builder.WriteString(EscapeHTML(opinion.Summary))
		builder.WriteString("\n")
	}

	if len(opinion.Reasons) > 0 {
		builder.WriteString("\n<b>理由</b>\n")
		for _, reason := range opinion.Reasons {
			builder.WriteString("• ")
			builder.WriteString(EscapeHTML(reason))
			builder.WriteString("\n")
		}
	}
	if len(opinion.Risks) > 0 {
		builder.WriteString("\n<b>风险</b>\n")
		for _, risk := range opinion.Risks {
			builder.WriteString("• ")
			builder.WriteString(EscapeHTML(risk))
			builder.WriteString("\n")
		}
	}

	builder.WriteString("\n数据：")
	source := report.Source
	if source == "" {
		source = "Binance"
	}
	builder.WriteString(EscapeHTML(source))
	if report.DataQuality == "partial" {
		builder.WriteString("（数据不完整）")
	}
	builder.WriteString("\n")
	builder.WriteString(agent.Disclaimer)
	return builder.String()
}

func modeLabel(mode string) string {
	if mode == "short" {
		return "短线"
	}
	return "波段"
}

func directionEmoji(direction string) string {
	switch direction {
	case "bullish":
		return "🟢"
	case "bearish":
		return "🔴"
	default:
		return "⚪"
	}
}

func directionLabel(direction string) string {
	switch direction {
	case "bullish":
		return "偏多"
	case "bearish":
		return "偏空"
	default:
		return "观望"
	}
}

func confidenceLabel(confidence string) string {
	switch confidence {
	case "high":
		return "高"
	case "low":
		return "低"
	default:
		return "中等"
	}
}

func formatLevels(values []float64) string {
	if len(values) == 0 {
		return "—"
	}
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, formatPrice(value))
	}
	return strings.Join(parts, " / ")
}

func formatRange(low, high float64) string {
	if low == high {
		return formatPrice(low)
	}
	return formatPrice(low) + "–" + formatPrice(high)
}

func formatPrice(value float64) string {
	decimals := 2
	switch {
	case value < 0.0001:
		decimals = 8
	case value < 1:
		decimals = 6
	case value < 100:
		decimals = 4
	}
	formatted := strconv.FormatFloat(value, 'f', decimals, 64)
	formatted = strings.TrimRight(formatted, "0")
	formatted = strings.TrimRight(formatted, ".")
	parts := strings.SplitN(formatted, ".", 2)
	parts[0] = addThousands(parts[0])
	if len(parts) == 2 {
		return parts[0] + "." + parts[1]
	}
	return parts[0]
}

func addThousands(value string) string {
	sign := ""
	if strings.HasPrefix(value, "-") {
		sign = "-"
		value = value[1:]
	}
	var builder strings.Builder
	for i, r := range value {
		if i > 0 && (len(value)-i)%3 == 0 {
			builder.WriteByte(',')
		}
		builder.WriteRune(r)
	}
	return sign + builder.String()
}

func formatPercent(value float64) string {
	sign := ""
	if value > 0 {
		sign = "+"
	}
	return fmt.Sprintf("%s%.2f%%", sign, value)
}
