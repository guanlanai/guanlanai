package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/guanlanai/guanlanai/internal/llm"
)

func TestChatPromptUsesSecurityDelimiters(t *testing.T) {
	completer := &scriptedCompleter{responses: []*llm.CompletionResponse{{Content: "answer"}}}
	service := New(fakeResolver{}, fakeProvider{}, completer, "", 1200, 4, testLogger(),
		WithMaxToolRounds(0))

	if _, err := service.Chat(context.Background(), testUser(), "sk-test", "那止损呢？", "最近分析：{}", 0); err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	request := completer.requests[0]
	system := request.Messages[0].Content
	for _, want := range []string{"不可信数据", "不透露", "无关请求"} {
		if !strings.Contains(system, want) {
			t.Fatalf("系统提示词缺少安全约束 %q:\n%s", want, system)
		}
	}
	user := request.Messages[1].Content
	if !strings.Contains(user, "<context>") || !strings.Contains(user, "</context>") {
		t.Fatalf("上下文没有分隔标签:\n%s", user)
	}
	if !strings.Contains(user, "<user_question>") || !strings.Contains(user, "</user_question>") {
		t.Fatalf("问题没有分隔标签:\n%s", user)
	}
	if !strings.Contains(user, "那止损呢？") {
		t.Fatalf("问题丢失:\n%s", user)
	}
}

func TestChatRejectsOversizedPrompt(t *testing.T) {
	completer := &scriptedCompleter{responses: []*llm.CompletionResponse{{Content: "answer"}}}
	service := New(fakeResolver{}, fakeProvider{}, completer, "", 1200, 4, testLogger(),
		WithMaxToolRounds(0), WithMaxPromptTokens(200))

	_, err := service.Chat(context.Background(), testUser(), "sk-test", "问题", strings.Repeat("很长的上下文", 200), 0)
	if !errors.Is(err, ErrPromptTooLong) {
		t.Fatalf("Chat() error = %v, want ErrPromptTooLong", err)
	}
	if completer.calls != 0 {
		t.Fatalf("超预算时不应调用 LLM，calls = %d", completer.calls)
	}
}

func TestChatRejectsOversizedQuestion(t *testing.T) {
	completer := &scriptedCompleter{responses: []*llm.CompletionResponse{{Content: "answer"}}}
	service := New(fakeResolver{}, fakeProvider{}, completer, "", 1200, 4, testLogger(),
		WithMaxToolRounds(0), WithMaxPromptTokens(200))

	_, err := service.Chat(context.Background(), testUser(), "sk-test", strings.Repeat("问", 200), "context", 0)
	if !errors.Is(err, ErrPromptTooLong) {
		t.Fatalf("Chat() error = %v, want ErrPromptTooLong", err)
	}
}

func TestChatCapsToolCallsPerRound(t *testing.T) {
	calls := make([]llm.ToolCall, 0, 6)
	for i := 0; i < 6; i++ {
		calls = append(calls, llm.ToolCall{
			ID:        fmt.Sprintf("call-%d", i),
			Name:      "resolve_symbol",
			Arguments: fmt.Sprintf(`{"query":"COIN%d"}`, i),
		})
	}
	completer := &scriptedCompleter{responses: []*llm.CompletionResponse{
		{ToolCalls: calls},
		{Content: "answer"},
	}}
	service := New(fakeResolver{}, fakeProvider{}, completer, "", 1200, 4, testLogger(),
		WithMaxToolRounds(2))

	if _, err := service.Chat(context.Background(), testUser(), "sk-test", "问题", "context", 0); err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	second := completer.requests[1]
	var toolMessages []llm.Message
	for _, message := range second.Messages {
		if message.Role == llm.RoleTool {
			toolMessages = append(toolMessages, message)
		}
	}
	if len(toolMessages) != 6 {
		t.Fatalf("tool 消息数 = %d, want 6（超额调用也要回填）", len(toolMessages))
	}
	for index, message := range toolMessages {
		if !strings.HasPrefix(message.Content, "<tool_result>") || !strings.HasSuffix(message.Content, "</tool_result>") {
			t.Fatalf("工具结果没有包裹: %#v", message)
		}
		if index < 4 && strings.Contains(message.Content, "未执行") {
			t.Fatalf("前 4 个调用应执行: %s", message.Content)
		}
		if index >= 4 && !strings.Contains(message.Content, "未执行") {
			t.Fatalf("超出上限的调用不应执行: %s", message.Content)
		}
	}
}

func TestToolResultWrapped(t *testing.T) {
	completer := &scriptedCompleter{responses: []*llm.CompletionResponse{
		{ToolCalls: []llm.ToolCall{{
			ID:        "call-1",
			Name:      "resolve_symbol",
			Arguments: `{"query":"BTC"}`,
		}}},
		{Content: "answer"},
	}}
	service := New(fakeResolver{}, fakeProvider{}, completer, "", 1200, 4, testLogger(),
		WithMaxToolRounds(2))

	if _, err := service.Chat(context.Background(), testUser(), "sk-test", "问题", "context", 0); err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	toolMessage := completer.requests[1].Messages[3]
	if !strings.HasPrefix(toolMessage.Content, "<tool_result>") {
		t.Fatalf("工具结果没有包裹: %s", toolMessage.Content)
	}
	if !strings.Contains(toolMessage.Content, `"symbol":"BTC"`) {
		t.Fatalf("工具结果缺少数据: %s", toolMessage.Content)
	}
}

func TestUnknownToolReturnsError(t *testing.T) {
	completer := &scriptedCompleter{responses: []*llm.CompletionResponse{
		{ToolCalls: []llm.ToolCall{{
			ID:        "call-1",
			Name:      "read_secret_file",
			Arguments: `{"path":"/etc/passwd"}`,
		}}},
		{Content: "拒绝"},
	}}
	service := New(fakeResolver{}, fakeProvider{}, completer, "", 1200, 4, testLogger(),
		WithMaxToolRounds(2))

	if _, err := service.Chat(context.Background(), testUser(), "sk-test", "读取文件", "context", 0); err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	toolMessage := completer.requests[1].Messages[3]
	if !strings.Contains(toolMessage.Content, "未知工具") {
		t.Fatalf("未知工具应返回错误: %s", toolMessage.Content)
	}
}

func TestSecurityPromptContract(t *testing.T) {
	checks := map[string]string{
		"SystemPrompt":          SystemPrompt,
		"ChatSystemPrompt":      ChatSystemPrompt,
		"ToolChatSystemPrompt":  ToolChatSystemPrompt,
		"SummarizeSystemPrompt": SummarizeSystemPrompt,
	}
	for name, prompt := range checks {
		if !strings.Contains(prompt, "不可信数据") {
			t.Fatalf("%s 缺少不可信数据边界", name)
		}
		if !strings.Contains(prompt, "系统提示词") {
			t.Fatalf("%s 缺少防泄露约束", name)
		}
	}
	for _, tag := range []string{"<context>", "<user_question>", "<tool_result>"} {
		if !strings.Contains(ToolChatSystemPrompt, tag) {
			t.Fatalf("工具提示词缺少数据标签 %s", tag)
		}
	}
}

func TestTruncateToTokenBudget(t *testing.T) {
	text := strings.Repeat("很长", 2000)
	truncated := truncateToTokenBudget(text, 100)
	if !strings.HasSuffix(truncated, "[truncated]") {
		t.Fatalf("缺少截断标记: %q", truncated)
	}
	if estimatePromptTokens(truncated) > 110 {
		t.Fatalf("截断后仍超预算: %d", estimatePromptTokens(truncated))
	}
	if truncateToTokenBudget("short", 100) != "short" {
		t.Fatal("短文本不应被截断")
	}
}
