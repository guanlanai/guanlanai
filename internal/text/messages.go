package text

import (
	"fmt"
	"html"
	"strconv"
	"strings"
	"time"

	"github.com/guanlanai/guanlanai/internal/analysis"
	"github.com/guanlanai/guanlanai/internal/market"
)

const disclaimer = "⚠️ 仅供参考，不构成投资建议。"

// Welcome 是 /start 的欢迎语。
func Welcome() string {
	return `<b>观澜 · GuanLanAI</b>
Telegram 加密货币交易分析助手（私聊多会话 · 群组隔离）

我可以根据币种代码给出结构化交易观点，并在分析后继续追问。当前已接入 Telegram、用户 Key、Binance 行情、LLM 分析和会话记忆。

常用命令：
/start - 欢迎与说明
/help - 使用帮助
/key - 配置 OpenAI API Key
/baseurl - 配置 OpenAI 兼容代理
/model - 选择模型
/mode - 切换短线/波段
/timeframe - 设置常用周期
/indicators - 设置常用指标
/risk - 设置每笔最大亏损
/strategy - 切换单周期/三重滤网
/status - 运行状态
/reset - 清空当前会话
/new [标题] - 新建私聊会话
/sessions - 查看私聊会话
/analyze BTC - 分析币种

首次使用请先发送 /key 配置 API Key。

当前支持私聊和群组，仅限加密货币，暂不支持股票，也不提供真实下单。
` + disclaimer
}

// Help 是 /help 的说明。
func Help() string {
	return `<b>使用说明</b>
直接发送币种代码，例如 <code>BTC</code>、<code>ETH</code>。
也可以使用 /analyze BTC。

命令：
/start - 欢迎与说明
/help - 使用帮助
/status - 查看实例、Key、模型和接入进度
/reset - 清空当前会话
/new [标题] - 新建私聊会话
/sessions - 列出私聊会话
/switch &lt;id&gt; - 切换当前会话
/rename &lt;id&gt; &lt;标题&gt; - 重命名会话
/delete &lt;id&gt; confirm - 删除会话
/key - 配置 OpenAI API Key
/key delete - 删除已保存的 Key
/baseurl - 配置 OpenAI 兼容代理
/baseurl reset - 恢复官方 endpoint
/model - 查看或切换模型
/mode - 查看或切换短线/波段
/timeframe - 查看或设置常用周期
/indicators - 查看或设置常用指标
/risk - 查看或设置每笔最大亏损（USDT）
/strategy - 查看或切换单周期/三重滤网

分析后可以直接追问，例如“那止损呢？”、“如果跌破支撑怎么办？”。
Key 配置流程：/key → 下一条消息发送 Key → Bot 删除该消息并加密保存。
自定义代理必须是公网 HTTPS 地址，且包含 /v1 等路径。

当前支持私聊和群组，仅限加密货币和技术面观点；股票、新闻、真实下单暂不支持。
` + disclaimer
}

// Status 展示当前实例和用户配置状态。
func Status(instance, version, env, model, mode, strategy, timeframe string, timeframeDefault bool, key, endpoint string, uptime time.Duration) string {
	if model == "" {
		model = "未配置"
	}
	timeframeLabel := timeframe
	if timeframeDefault {
		timeframeLabel += "（默认）"
	}
	return fmt.Sprintf(`<b>运行状态</b>
实例：<code>%s</code>
环境：<code>%s</code>
版本：<code>%s</code>
运行时长：%s
模型：<code>%s</code>
模式：<code>%s</code>
策略：<code>%s</code>
周期：<code>%s</code>
API Key：<code>%s</code>
Endpoint：<code>%s</code>

Telegram：已连接
行情：已接入
会话：已接入

%s`,
		html.EscapeString(instance),
		html.EscapeString(env),
		html.EscapeString(version),
		formatUptime(uptime),
		html.EscapeString(model),
		html.EscapeString(mode),
		html.EscapeString(strategy),
		html.EscapeString(timeframeLabel),
		html.EscapeString(key),
		html.EscapeString(endpoint),
		disclaimer,
	)
}

// TimeframeCurrent 展示用户当前周期和可用周期。
func TimeframeCurrent(current string, isDefault bool) string {
	label := current
	if isDefault {
		label += "（默认）"
	}
	return fmt.Sprintf(`当前周期：<code>%s</code>

可用周期：<code>%s</code>

使用 /timeframe &lt;周期&gt; 设置，例如 /timeframe 4h；发送 /timeframe reset 恢复默认。`,
		html.EscapeString(label), html.EscapeString(market.TimeframesLabel()))
}

// TimeframeSet 表示常用周期已保存。
func TimeframeSet(timeframe string) string {
	return fmt.Sprintf("常用周期已设置为 <code>%s</code>，下一次分析生效。", html.EscapeString(timeframe))
}

// TimeframeReset 表示已恢复系统默认周期。
func TimeframeReset(timeframe string) string {
	return fmt.Sprintf("已恢复系统默认周期 <code>%s</code>。", html.EscapeString(timeframe))
}

// TimeframeInvalid 表示周期输入不合法。
func TimeframeInvalid(available string) string {
	return fmt.Sprintf("不支持的周期。可用周期：<code>%s</code>", html.EscapeString(available))
}

// IndicatorsCurrent 展示用户当前指标组和可选指标。
func IndicatorsCurrent(current string, isDefault bool) string {
	label := current
	if isDefault {
		label += "（默认）"
	}
	return fmt.Sprintf(`当前指标：<code>%s</code>

可用指标：<code>%s</code>
ATR 和支撑阻力属于基础风控指标，始终保留，最多选择 %d 个。

使用 /indicators ema,rsi,macd 设置；发送 /indicators reset 恢复默认。`,
		html.EscapeString(label), html.EscapeString(analysis.IndicatorsLabel()), analysis.MaxIndicators)
}

// IndicatorsSet 表示常用指标已保存。
func IndicatorsSet(indicators string) string {
	return fmt.Sprintf("常用指标已设置为 <code>%s</code>，下一次分析生效，并优先复用会话已有 K 线。",
		html.EscapeString(indicators))
}

// IndicatorsReset 表示已恢复默认指标集合。
func IndicatorsReset(indicators string) string {
	return fmt.Sprintf("已恢复默认指标集合 <code>%s</code>。", html.EscapeString(indicators))
}

// IndicatorsInvalid 表示指标输入不合法。
func IndicatorsInvalid(reason, available string) string {
	return fmt.Sprintf("指标设置失败：%s\n可用指标：<code>%s</code>",
		html.EscapeString(reason), html.EscapeString(available))
}

// StrategyCurrent 展示当前分析策略。
func StrategyCurrent(current, tripleLabel string, isDefault bool) string {
	label := current
	if isDefault {
		label += "（默认）"
	}
	return fmt.Sprintf(`当前策略：<code>%s</code>

可用策略：
- <code>single</code> 单周期分析
- <code>triple</code> 三重滤网（潮汐/波浪/涟漪：%s）

使用 /strategy triple 开启三重滤网，/strategy single 恢复单周期，/strategy reset 恢复默认。`,
		html.EscapeString(label), html.EscapeString(tripleLabel))
}

// StrategySet 表示分析策略已保存。
func StrategySet(triple bool, tripleLabel string) string {
	if triple {
		return fmt.Sprintf("分析策略已设置为 <b>三重滤网</b>（%s），下一次分析生效。", html.EscapeString(tripleLabel))
	}
	return "分析策略已恢复为 <b>单周期</b>，下一次分析生效。"
}

// StrategyReset 表示已恢复默认策略。
func StrategyReset(strategy string) string {
	return fmt.Sprintf("已恢复默认分析策略：<b>%s</b>。", html.EscapeString(strategy))
}

// StrategyInvalid 表示策略输入不合法。
func StrategyInvalid(tripleLabel string) string {
	return fmt.Sprintf("不支持的分析策略。请使用 /strategy single 或 /strategy triple（%s）。",
		html.EscapeString(tripleLabel))
}

// RiskCurrent 展示当前每笔最大亏损。
func RiskCurrent(amount float64) string {
	return fmt.Sprintf(`当前每笔最大亏损：<code>%s USDT</code>

仓位会按“风险金额 / |入场价 - 止损价|”计算，不使用杠杆。
使用 /risk &lt;金额&gt; 修改，例如 /risk 100；发送 /risk reset 清除。`,
		formatAmount(amount))
}

// RiskNotSet 表示还没有设置每笔最大亏损。
func RiskNotSet() string {
	return `当前未设置每笔最大亏损，因此不会给出仓位建议。

使用 /risk &lt;金额&gt; 设置，例如 /risk 100（单位 USDT）。`
}

// RiskSet 表示每笔最大亏损已保存。
func RiskSet(amount float64) string {
	return fmt.Sprintf("每笔最大亏损已设置为 <code>%s USDT</code>，下一次分析生效。", formatAmount(amount))
}

// RiskReset 表示已清除每笔最大亏损。
func RiskReset() string {
	return "已清除每笔最大亏损，后续分析不再提供仓位建议。"
}

// RiskInvalid 表示风险金额输入不合法。
func RiskInvalid() string {
	return "风险金额不合法。请发送正数金额，例如 /risk 100 或 /risk 100 USDT。"
}

func formatAmount(amount float64) string {
	return strconv.FormatFloat(amount, 'f', -1, 64)
}

// KeyPrompt 提示用户发送 API Key。
func KeyPrompt() string {
	return `请在下一条消息发送你的 OpenAI API Key。
- 收到后我会立即删除该消息
- Key 会使用 AES-256-GCM 加密存储
- 发送 /cancel 取消

如果你使用第三方兼容代理，先发送 /baseurl 配置地址。`
}

// KeySaved 表示 Key 已保存。
func KeySaved(mask, endpoint string) string {
	return fmt.Sprintf("API Key 已保存：<code>%s</code>\nEndpoint：<code>%s</code>\n\n%s",
		html.EscapeString(mask), html.EscapeString(endpoint), disclaimer)
}

// KeyInvalid 表示 Key 校验失败。
func KeyInvalid(reason string) string {
	return "API Key 校验失败：" + html.EscapeString(reason)
}

// KeyDeleted 表示 Key 已删除。
func KeyDeleted() string {
	return "已删除保存的 API Key。发送 /key 可重新配置。"
}

// NeedKey 提示先配置 Key。
func NeedKey() string {
	return "请先使用 /key 配置 OpenAI API Key。"
}

// BaseURLPrompt 提示用户发送兼容地址。
func BaseURLPrompt() string {
	return `请发送 OpenAI 兼容的 base_url，必须包含路径，例如：
<code>https://api.openai.com/v1</code>
<code>https://your-proxy.example.com/v1</code>

只允许公网 HTTPS 地址；发送 /cancel 取消。`
}

// BaseURLSaved 表示 endpoint 已保存。
func BaseURLSaved(endpoint string) string {
	return fmt.Sprintf("已保存 endpoint：<code>%s</code>\n如已配置 Key，建议重新发送 /key 校验一次。",
		html.EscapeString(endpoint))
}

// BaseURLReset 表示 endpoint 已恢复默认。
func BaseURLReset(endpoint string) string {
	return fmt.Sprintf("已恢复默认 endpoint：<code>%s</code>", html.EscapeString(endpoint))
}

// BaseURLInvalid 表示 endpoint 校验失败。
func BaseURLInvalid(reason string) string {
	return "base_url 校验失败：" + html.EscapeString(reason)
}

// ModelList 展示可选模型。
func ModelList(current string, models []string) string {
	if current == "" {
		current = "未配置"
	}
	var builder strings.Builder
	builder.WriteString("<b>可用模型</b>\n当前：<code>")
	builder.WriteString(html.EscapeString(current))
	builder.WriteString("</code>\n\n")
	for _, model := range models {
		builder.WriteString("- <code>")
		builder.WriteString(html.EscapeString(model))
		builder.WriteString("</code>\n")
	}
	builder.WriteString("\n使用 /model &lt;模型名&gt; 切换。")
	return builder.String()
}

// ModelSet 表示模型已保存。
func ModelSet(model string) string {
	return fmt.Sprintf("模型已保存为 <code>%s</code>。", html.EscapeString(model))
}

// ModelInvalid 表示模型不可用。
func ModelInvalid(reason string) string {
	return "模型切换失败：" + html.EscapeString(reason)
}

// ModeSet 表示模式已保存。
func ModeSet(mode string) string {
	return fmt.Sprintf("分析模式已保存为 <code>%s</code>；分析接入后生效。", html.EscapeString(mode))
}

// ModeCurrent 展示当前模式。
func ModeCurrent(mode string) string {
	return fmt.Sprintf("当前分析模式：<code>%s</code>\n使用 /mode short 或 /mode swing 切换。", html.EscapeString(mode))
}

// Unavailable 表示功能尚未接入。
func Unavailable(feature string) string {
	return feature + " 尚未接入。"
}

// GenericError 用于不暴露内部细节的失败提示。
func GenericError() string {
	return "操作失败，请稍后再试。"
}

// Reset 是 /reset 的回复。
func Reset() string {
	return "已清空当前会话上下文和摘要。"
}

// SessionItem 是 /sessions 列表中的一项。
type SessionItem struct {
	ID    int64
	Title string
}

// SessionList 展示私聊会话列表。
func SessionList(items []SessionItem, activeID int64, total int) string {
	var builder strings.Builder
	builder.WriteString("<b>会话列表</b>\n")
	for _, item := range items {
		builder.WriteString(fmt.Sprintf("%d. %s", item.ID, html.EscapeString(item.Title)))
		if item.ID == activeID {
			builder.WriteString(" （active）")
		}
		builder.WriteString("\n")
	}
	builder.WriteString("\n使用 /switch &lt;id&gt; 切换，/new [标题] 新建。")
	if total > len(items) {
		builder.WriteString(fmt.Sprintf("\n仅显示最近 %d 条，共 %d 条。", len(items), total))
	}
	return builder.String()
}

// SessionCreated 表示会话已创建并设为 active。
func SessionCreated(id int64, title string) string {
	return fmt.Sprintf("已新建会话 %d：<b>%s</b>，并切换为当前会话。", id, html.EscapeString(title))
}

// SessionSwitched 表示会话已切换。
func SessionSwitched(id int64, title string) string {
	return fmt.Sprintf("已切换到会话 %d：<b>%s</b>。", id, html.EscapeString(title))
}

// SessionRenamed 表示会话已重命名。
func SessionRenamed(id int64, title string) string {
	return fmt.Sprintf("会话 %d 已重命名为 <b>%s</b>。", id, html.EscapeString(title))
}

// SessionDeleted 表示会话已删除。
func SessionDeleted(id int64) string {
	return fmt.Sprintf("已删除会话 %d。", id)
}

// SessionDeleteConfirm 要求确认删除。
func SessionDeleteConfirm(id int64, title string) string {
	return fmt.Sprintf("确认删除会话 %d：<b>%s</b>？\n发送 <code>/delete %d confirm</code> 确认。",
		id, html.EscapeString(title), id)
}

// SessionNotFound 表示会话不存在或不属于当前用户。
func SessionNotFound() string {
	return "找不到该会话，或它不属于当前聊天。发送 /sessions 查看可用会话。"
}

// SessionLimitReached 表示私聊会话数量已达上限。
func SessionLimitReached(limit int) string {
	return fmt.Sprintf("每个私聊最多保留 %d 条会话。请先使用 /delete &lt;id&gt; confirm 删除旧会话。", limit)
}

// SessionLastOne 表示不能删除最后一个会话。
func SessionLastOne() string {
	return "至少需要保留一条私聊会话，不能删除最后一个。"
}

// SessionUsage 是会话子命令的参数提示。
func SessionUsage(command string) string {
	switch command {
	case "/switch":
		return "用法：/switch &lt;id&gt;。发送 /sessions 查看会话 ID。"
	case "/rename":
		return "用法：/rename &lt;id&gt; &lt;标题&gt;。"
	case "/delete":
		return "用法：/delete &lt;id&gt; 或 /delete &lt;id&gt; confirm。"
	default:
		return "用法：/new [标题]。"
	}
}

// SessionTitleTooLong 表示标题超长。
func SessionTitleTooLong(limit int) string {
	return fmt.Sprintf("会话标题最多 %d 个字符。", limit)
}

// PrivateConfigHint 提示群组中不能配置 Key 或管理会话。
func PrivateConfigHint() string {
	return "请在私聊中配置 Key 和管理会话。"
}

// NeedKeyInGroup 提示群成员先私聊配置 Key。
func NeedKeyInGroup() string {
	return "请先私聊我并使用 /key 配置 OpenAI API Key，然后在群里分析。"
}

// GroupHelp 是群组中的 /help 说明。
func GroupHelp(botUsername string) string {
	name := strings.TrimSpace(botUsername)
	if name == "" {
		name = "本 Bot"
	} else {
		name = "@" + name
	}
	return fmt.Sprintf(`<b>群组使用说明</b>
在群里使用 <code>/analyze%s BTC</code>，或回复我的消息。
若已在 BotFather 关闭 privacy mode（或把我设为群管理员），也可以直接 <code>%s BTC</code>。

命令：
/analyze &lt;币种&gt; - 分析币种，群内请带 %s
/status - 查看当前会话状态
/reset - 清空你在本群的会话
/help - 本说明

每位成员在本群的上下文互相隔离；Key、模型、模式、策略、周期和指标请在私聊配置。
%s`, html.EscapeString(name), html.EscapeString(name), html.EscapeString(name), disclaimer)
}

// GroupStatus 展示群组中当前成员的会话状态。
func GroupStatus(instance, model, mode, strategy, timeframe string, timeframeDefault bool) string {
	if model == "" {
		model = "未配置"
	}
	timeframeLabel := timeframe
	if timeframeDefault {
		timeframeLabel += "（默认）"
	}
	return fmt.Sprintf(`<b>本群会话状态</b>
实例：<code>%s</code>
模型：<code>%s</code>
模式：<code>%s</code>
策略：<code>%s</code>
周期：<code>%s</code>

每位成员在本群的上下文互相隔离；Key、模型、模式、策略、周期和指标请在私聊配置。
%s`,
		html.EscapeString(instance),
		html.EscapeString(model),
		html.EscapeString(mode),
		html.EscapeString(strategy),
		html.EscapeString(timeframeLabel),
		disclaimer,
	)
}

// FollowUpNeedAnalysis 提示先做一次分析。
func FollowUpNeedAnalysis() string {
	return "当前没有可追问的分析。请先发送币种代码，例如 <code>BTC</code>。"
}

// RateLimited 提示限流。
func RateLimited(retryAfter time.Duration) string {
	seconds := int(retryAfter.Seconds())
	if seconds < 1 {
		seconds = 1
	}
	return fmt.Sprintf("请求过于频繁，请 %d 秒后再试。", seconds)
}

// Thinking 提示正在处理追问。
func Thinking() string {
	return "正在整理上下文并回答…"
}

// NeedSymbol 提示用户发送币种代码。
func NeedSymbol() string {
	return "请发送币种代码，例如 <code>BTC</code> 或 <code>ETH</code>。"
}

// NeedModel 提示先选择模型。
func NeedModel() string {
	return "请先使用 /model 选择一个可用模型。"
}

// Analyzing 表示正在拉取行情并分析。
func Analyzing(symbol, timeframe, mode string) string {
	return fmt.Sprintf("正在分析 <code>%s</code>（%s · %s）…",
		html.EscapeString(symbol), html.EscapeString(timeframe), modeLabel(mode))
}

// AnalysisError 返回用户可读的分析失败原因。
func AnalysisError(reason string) string {
	return "分析失败：" + html.EscapeString(reason)
}

func modeLabel(mode string) string {
	if mode == "short" {
		return "短线"
	}
	return "波段"
}

// Unsupported 提示当前不支持的内容。
func Unsupported() string {
	return "目前只支持币种代码（如 BTC、ETH），暂不支持股票或其他内容。\n\n" + disclaimer
}

// PrivateOnly 提示仅支持私聊。
func PrivateOnly() string {
	return "当前仅支持 Telegram 私聊，请私聊我使用。"
}

// TextOnly 提示仅支持文本。
func TextOnly() string {
	return "目前只支持文本消息。"
}

// Busy 表示当前 chat 队列已满。
func Busy() string {
	return "上一条消息还在处理中，请稍后再试。"
}

// UnknownCommand 提示未知命令。
func UnknownCommand() string {
	return "未知命令。发送 /help 查看可用命令。"
}

func formatUptime(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%d 秒", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%d 分钟", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%d 小时 %d 分钟", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%d 天 %d 小时", int(d.Hours())/24, int(d.Hours())%24)
}
