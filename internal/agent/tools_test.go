package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/guanlanai/guanlanai/internal/llm"
)

type scriptedCompleter struct {
	responses []*llm.CompletionResponse
	errs      []error
	requests  []llm.CompletionRequest
	calls     int
}

func (f *scriptedCompleter) Complete(_ context.Context, req llm.CompletionRequest) (*llm.CompletionResponse, error) {
	f.calls++
	f.requests = append(f.requests, req)
	index := f.calls - 1
	if index < len(f.errs) && f.errs[index] != nil {
		return nil, f.errs[index]
	}
	if index >= len(f.responses) {
		index = len(f.responses) - 1
	}
	if index < 0 {
		return &llm.CompletionResponse{}, nil
	}
	return f.responses[index], nil
}

func TestChatExecutesReadOnlyTool(t *testing.T) {
	completer := &scriptedCompleter{responses: []*llm.CompletionResponse{
		{ToolCalls: []llm.ToolCall{{
			ID:        "call-1",
			Name:      "get_market_snapshot",
			Arguments: `{"symbol":"BTC","timeframes":["1D"]}`,
		}}},
		{Content: "当前价格来自工具结果。"},
	}}
	provider := &countingProvider{}
	service := New(fakeResolver{}, provider, completer, "", 1200, 4, testLogger(), WithMaxToolRounds(3))

	result, err := service.Chat(context.Background(), testUser(), "sk-test", "现在什么价？", "context", 11)
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if result.Content == "" {
		t.Fatal("回答为空")
	}
	if provider.calls != 1 {
		t.Fatalf("provider calls = %d, want 1", provider.calls)
	}
	if completer.calls != 2 {
		t.Fatalf("LLM calls = %d, want 2", completer.calls)
	}

	second := completer.requests[1]
	if len(second.Tools) != 3 {
		t.Fatalf("第二轮应继续携带工具: %#v", second.Tools)
	}
	if len(second.Messages) != 4 {
		t.Fatalf("消息数 = %d, want 4", len(second.Messages))
	}
	if second.Messages[2].Role != llm.RoleAssistant || len(second.Messages[2].ToolCalls) != 1 {
		t.Fatalf("assistant 工具调用消息 = %#v", second.Messages[2])
	}
	toolMessage := second.Messages[3]
	if toolMessage.Role != llm.RoleTool || toolMessage.ToolCallID != "call-1" {
		t.Fatalf("tool 消息 = %#v", toolMessage)
	}
	if !strings.Contains(toolMessage.Content, `"price":399`) {
		t.Fatalf("工具结果缺少行情: %s", toolMessage.Content)
	}
}

func TestChatCachesIdenticalToolCallsWithinTurn(t *testing.T) {
	call := llm.ToolCall{
		ID:        "call-1",
		Name:      "get_market_snapshot",
		Arguments: `{"symbol":"BTC","timeframes":["1D"]}`,
	}
	second := call
	second.ID = "call-2"
	completer := &scriptedCompleter{responses: []*llm.CompletionResponse{
		{ToolCalls: []llm.ToolCall{call, second}},
		{Content: "完成"},
	}}
	provider := &countingProvider{}
	service := New(fakeResolver{}, provider, completer, "", 1200, 4, testLogger(), WithMaxToolRounds(2))

	if _, err := service.Chat(context.Background(), testUser(), "sk-test", "价格？", "context", 0); err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if provider.calls != 1 {
		t.Fatalf("相同参数应命中 turn 内缓存，provider calls = %d", provider.calls)
	}
}

func TestChatFallsBackWhenToolsUnsupported(t *testing.T) {
	completer := &scriptedCompleter{
		responses: []*llm.CompletionResponse{
			{},
			{Content: "直接回答"},
		},
		errs: []error{llm.ErrUnsupportedFeature, nil},
	}
	service := New(fakeResolver{}, fakeProvider{}, completer, "", 1200, 4, testLogger(), WithMaxToolRounds(3))

	result, err := service.Chat(context.Background(), testUser(), "sk-test", "问题", "context", 0)
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if result.Content != "直接回答" {
		t.Fatalf("回答 = %q", result.Content)
	}
	if len(completer.requests[1].Tools) != 0 {
		t.Fatalf("降级后不应再携带工具: %#v", completer.requests[1].Tools)
	}
}

func TestChatStopsAfterMaxToolRounds(t *testing.T) {
	completer := &scriptedCompleter{responses: []*llm.CompletionResponse{
		{ToolCalls: []llm.ToolCall{{
			ID:        "call-1",
			Name:      "resolve_symbol",
			Arguments: `{"query":"BTC"}`,
		}}},
		{Content: "收口回答"},
	}}
	service := New(fakeResolver{}, fakeProvider{}, completer, "", 1200, 4, testLogger(), WithMaxToolRounds(1))

	result, err := service.Chat(context.Background(), testUser(), "sk-test", "问题", "context", 0)
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if result.Content != "收口回答" {
		t.Fatalf("回答 = %q", result.Content)
	}
	if len(completer.requests[1].Tools) != 0 {
		t.Fatalf("收口调用不应携带工具: %#v", completer.requests[1].Tools)
	}
	last := completer.requests[1].Messages[len(completer.requests[1].Messages)-1]
	if !strings.Contains(last.Content, "工具调用上限") {
		t.Fatalf("缺少收口提示: %#v", last)
	}
}

func TestChatReturnsToolErrorToModel(t *testing.T) {
	completer := &scriptedCompleter{responses: []*llm.CompletionResponse{
		{ToolCalls: []llm.ToolCall{{
			ID:        "call-1",
			Name:      "get_technical_analysis",
			Arguments: `{"symbol":"NOPE","timeframe":"1D"}`,
		}}},
		{Content: "标的不存在"},
	}}
	service := New(fakeResolver{}, fakeProvider{}, completer, "", 1200, 4, testLogger(), WithMaxToolRounds(2))

	if _, err := service.Chat(context.Background(), testUser(), "sk-test", "分析一下", "context", 0); err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	toolMessage := completer.requests[1].Messages[3]
	if !strings.Contains(toolMessage.Content, `"error"`) {
		t.Fatalf("工具错误应回传 JSON: %s", toolMessage.Content)
	}
}

func TestChatNoToolsWhenDisabled(t *testing.T) {
	completer := &scriptedCompleter{responses: []*llm.CompletionResponse{{Content: "answer"}}}
	service := New(fakeResolver{}, fakeProvider{}, completer, "", 1200, 4, testLogger(), WithMaxToolRounds(0))

	if _, err := service.Chat(context.Background(), testUser(), "sk-test", "问题", "context", 0); err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if len(completer.requests) != 1 {
		t.Fatalf("calls = %d, want 1", len(completer.requests))
	}
	if len(completer.requests[0].Tools) != 0 {
		t.Fatalf("关闭工具后不应携带工具: %#v", completer.requests[0].Tools)
	}
}

func TestChatPropagatesToolUnsupportedOnlyOnce(t *testing.T) {
	completer := &scriptedCompleter{
		responses: []*llm.CompletionResponse{{}, {}},
		errs:      []error{errors.New("boom"), errors.New("boom")},
	}
	service := New(fakeResolver{}, fakeProvider{}, completer, "", 1200, 4, testLogger(), WithMaxToolRounds(3))

	if _, err := service.Chat(context.Background(), testUser(), "sk-test", "问题", "context", 0); err == nil {
		t.Fatal("普通错误不应被吞掉")
	}
	if completer.calls != 1 {
		t.Fatalf("普通错误不应重试，calls = %d", completer.calls)
	}
}
