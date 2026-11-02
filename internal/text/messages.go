package text

import (
	"fmt"
	"html"
	"strings"
	"time"
)

const disclaimer = "⚠️ 仅供参考，不构成投资建议。"

// Welcome 是 /start 的欢迎语。
func Welcome() string {
	return `<b>观澜 · GuanLanAI</b>
Telegram 加密货币交易分析助手（Phase 3）

我可以根据币种代码给出结构化交易观点。当前已接入 Telegram、用户 Key、Binance 行情和 LLM 分析。

常用命令：
/start - 欢迎与说明
/help - 使用帮助
/key - 配置 OpenAI API Key
/baseurl - 配置 OpenAI 兼容代理
/model - 选择模型
/mode - 切换短线/波段
/status - 运行状态
/reset - 清空当前会话
/analyze BTC - 分析币种

首次使用请先发送 /key 配置 API Key。

当前仅支持私聊和加密货币，暂不支持股票，也不提供真实下单。
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
/key - 配置 OpenAI API Key
/key delete - 删除已保存的 Key
/baseurl - 配置 OpenAI 兼容代理
/baseurl reset - 恢复官方 endpoint
/model - 查看或切换模型
/mode - 查看或切换短线/波段

Key 配置流程：/key → 下一条消息发送 Key → Bot 删除该消息并加密保存。
自定义代理必须是公网 HTTPS 地址，且包含 /v1 等路径。

当前仅支持私聊、加密货币和技术面观点；股票、新闻、真实下单暂不支持。
` + disclaimer
}

// Status 展示当前实例和用户配置状态。
func Status(instance, version, env, model, mode, key, endpoint string, uptime time.Duration) string {
	if model == "" {
		model = "未配置"
	}
	return fmt.Sprintf(`<b>运行状态</b>
实例：<code>%s</code>
环境：<code>%s</code>
版本：<code>%s</code>
运行时长：%s
模型：<code>%s</code>
模式：<code>%s</code>
API Key：<code>%s</code>
Endpoint：<code>%s</code>

Telegram：已连接
行情：已接入
会话：Phase 4 接入

%s`,
		html.EscapeString(instance),
		html.EscapeString(env),
		html.EscapeString(version),
		formatUptime(uptime),
		html.EscapeString(model),
		html.EscapeString(mode),
		html.EscapeString(key),
		html.EscapeString(endpoint),
		disclaimer,
	)
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

// Reset 是 /reset 的占位回复。
func Reset() string {
	return "已清空当前会话（Phase 1 占位；Phase 4 接入真实上下文）。"
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
func Analyzing(symbol, mode string) string {
	return fmt.Sprintf("正在分析 <code>%s</code>（%s）…", html.EscapeString(symbol), modeLabel(mode))
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
