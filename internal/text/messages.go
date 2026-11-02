package text

import (
	"fmt"
	"html"
	"time"
)

const disclaimer = "⚠️ 仅供参考，不构成投资建议。"

// Welcome 是 /start 的欢迎语。
func Welcome() string {
	return `<b>观澜 · GuanLanAI</b>
Telegram 加密货币交易分析助手（Phase 1）

我可以根据币种代码给出结构化交易观点。当前版本只接入 Telegram 链路，行情与 LLM 分析会在后续阶段接入。

可用命令：
/start - 欢迎与说明
/help - 使用帮助
/status - 运行状态
/reset - 清空当前会话
/analyze BTC - 分析币种（Phase 3 接入）

直接发送 <code>BTC</code> 或 <code>ETH</code> 也可以。

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
/status - 查看实例、版本和接入进度
/reset - 清空当前会话
/key - 配置 OpenAI API Key（Phase 2 接入）
/baseurl - 配置 OpenAI 兼容代理（Phase 2 接入）
/model - 切换模型（Phase 2 接入）
/mode - 切换短线/波段（Phase 4 接入）

当前仅支持私聊、加密货币和技术面观点；股票、新闻、真实下单暂不支持。
` + disclaimer
}

// Status 展示当前实例状态。
func Status(instance, version, env, model, mode string, uptime time.Duration) string {
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

Telegram：已连接
OpenAI Key：Phase 2 接入
行情：Phase 3 接入
会话：Phase 4 接入

%s`,
		html.EscapeString(instance),
		html.EscapeString(env),
		html.EscapeString(version),
		formatUptime(uptime),
		html.EscapeString(model),
		html.EscapeString(mode),
		disclaimer,
	)
}

// Reset 是 /reset 的占位回复。
func Reset() string {
	return "已清空当前会话（Phase 1 占位；Phase 4 接入真实上下文）。"
}

// Phase1 表示 Telegram 链路已通，但分析尚未接入。
func Phase1(symbol string) string {
	return fmt.Sprintf("已收到 <code>%s</code>。Telegram 链路正常；行情与交易观点将在 Phase 3 接入。\n\n%s",
		html.EscapeString(symbol), disclaimer)
}

// NeedSymbol 提示用户发送币种代码。
func NeedSymbol() string {
	return "请发送币种代码，例如 <code>BTC</code> 或 <code>ETH</code>。"
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
