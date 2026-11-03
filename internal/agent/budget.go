package agent

import (
	"fmt"
	"strings"

	"github.com/guanlanai/guanlanai/internal/llm"
)

const (
	// maxToolCallsPerRound 限制模型单次响应里实际执行的工具数量。
	maxToolCallsPerRound = 4
	// maxToolCallsPerTurn 限制一次追问里实际执行的工具总数。
	maxToolCallsPerTurn = 8
	// maxToolResultTokens 限制单个工具结果的大小。
	maxToolResultTokens = 1200
	// maxToolResultTokensTotal 限制一次追问里所有工具结果的总大小。
	maxToolResultTokensTotal = 2400
)

// estimatePromptTokens 是偏保守的 token 估算：
// ASCII 约 4 字符/token，非 ASCII（中文等）约 1 字符/token。
func estimatePromptTokens(text string) int {
	if text == "" {
		return 0
	}
	ascii := 0
	other := 0
	for _, r := range text {
		if r < 128 {
			ascii++
		} else {
			other++
		}
	}
	return other + ascii/4 + 1
}

func messageTokens(message llm.Message) int {
	total := estimatePromptTokens(message.Content) + estimatePromptTokens(message.Name)
	for _, call := range message.ToolCalls {
		total += estimatePromptTokens(call.Name)
		total += estimatePromptTokens(call.Arguments)
		total += 8
	}
	return total
}

func toolDefinitionTokens(tools []llm.Tool) int {
	total := 0
	for _, tool := range tools {
		total += estimatePromptTokens(tool.Name)
		total += estimatePromptTokens(tool.Description)
		total += estimatePromptTokens(string(tool.Parameters))
	}
	return total
}

// checkPromptBudget 在调用 LLM 前做一次硬预算检查。
// 这里不尝试静默截断，避免把关键上下文截掉后产生误导性观点。
func (s *Service) checkPromptBudget(messages []llm.Message, tools []llm.Tool) error {
	total := toolDefinitionTokens(tools)
	for _, message := range messages {
		total += messageTokens(message)
	}
	if s.maxPrompt > 0 && total > s.maxPrompt {
		return fmt.Errorf("%w: 估计 %d tokens，上限 %d", ErrPromptTooLong, total, s.maxPrompt)
	}
	return nil
}

// truncateToTokenBudget 按估算 token 截断单个工具结果，避免一次工具调用撑爆上下文。
func truncateToTokenBudget(text string, max int) string {
	if max <= 0 || estimatePromptTokens(text) <= max {
		return text
	}
	var builder strings.Builder
	ascii := 0
	other := 0
	for _, r := range text {
		if r < 128 {
			ascii++
		} else {
			other++
		}
		if other+ascii/4 > max {
			break
		}
		builder.WriteRune(r)
	}
	return builder.String() + "…[truncated]"
}
