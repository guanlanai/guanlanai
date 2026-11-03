package openai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/guanlanai/guanlanai/internal/llm"
	"github.com/guanlanai/guanlanai/internal/netguard"
)

func TestCompleteFallsBackFromJSONSchema(t *testing.T) {
	var requests atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		count := requests.Add(1)
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("解析请求失败: %v", err)
		}
		format, _ := payload["response_format"].(map[string]any)
		if count == 1 {
			if format["type"] != "json_schema" {
				t.Fatalf("第一次应使用 json_schema: %#v", format)
			}
			jsonSchema, _ := format["json_schema"].(map[string]any)
			if jsonSchema["name"] != "trade_opinion" {
				t.Fatalf("schema name = %#v", jsonSchema["name"])
			}
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"response_format json_schema is not supported","type":"invalid_request_error"}}`))
			return
		}
		if format["type"] != "json_object" {
			t.Fatalf("降级后应使用 json_object: %#v", format)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"chatcmpl-test","object":"chat.completion","created":1,"model":"gpt-test",
			"choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"{\"ok\":true}"}}],
			"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}
		}`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	completer := NewCompleter(netguard.Policy{AllowHTTP: true, AllowPrivate: true}, 5*time.Second)
	response, err := completer.Complete(context.Background(), llm.CompletionRequest{
		BaseURL:         server.URL + "/v1",
		APIKey:          "sk-test",
		Model:           "gpt-test",
		System:          "system",
		User:            "user",
		MaxOutputTokens: 100,
		JSONSchema: &llm.JSONSchema{
			Name:   "trade_opinion",
			Schema: json.RawMessage(`{"type":"object","properties":{"ok":{"type":"boolean"}}}`),
		},
	})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if response.Content != `{"ok":true}` {
		t.Fatalf("Content = %q", response.Content)
	}
	if requests.Load() != 2 {
		t.Fatalf("requests = %d, want 2", requests.Load())
	}
}

func TestCompleteSendsToolsAndParsesToolCalls(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("解析请求失败: %v", err)
		}
		tools, _ := payload["tools"].([]any)
		if len(tools) != 1 {
			t.Fatalf("tools = %#v", payload["tools"])
		}
		tool, _ := tools[0].(map[string]any)
		function, _ := tool["function"].(map[string]any)
		if function["name"] != "get_market_snapshot" {
			t.Fatalf("tool function = %#v", function)
		}
		messages, _ := payload["messages"].([]any)
		if len(messages) != 4 {
			t.Fatalf("messages = %#v", messages)
		}
		assistant, _ := messages[2].(map[string]any)
		if assistant["role"] != "assistant" {
			t.Fatalf("assistant 消息 = %#v", assistant)
		}
		toolMessage, _ := messages[3].(map[string]any)
		if toolMessage["role"] != "tool" || toolMessage["tool_call_id"] != "call_0" {
			t.Fatalf("tool 消息 = %#v", toolMessage)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"chatcmpl-test","object":"chat.completion","created":1,"model":"gpt-test",
			"choices":[{
				"index":0,"finish_reason":"tool_calls",
				"message":{
					"role":"assistant","content":null,
					"tool_calls":[{"id":"call_1","type":"function","function":{"name":"get_market_snapshot","arguments":"{\"symbol\":\"BTC\"}"}}]
				}
			}],
			"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}
		}`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	completer := NewCompleter(netguard.Policy{AllowHTTP: true, AllowPrivate: true}, 5*time.Second)
	response, err := completer.Complete(context.Background(), llm.CompletionRequest{
		BaseURL: server.URL + "/v1",
		APIKey:  "sk-test",
		Model:   "gpt-test",
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: "system"},
			{Role: llm.RoleUser, Content: "user"},
			{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{
				ID: "call_0", Name: "resolve_symbol", Arguments: `{"query":"BTC"}`,
			}}},
			{Role: llm.RoleTool, ToolCallID: "call_0", Content: `{"symbol":"BTC"}`},
		},
		Tools: []llm.Tool{{
			Name:        "get_market_snapshot",
			Description: "snapshot",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"symbol":{"type":"string"}},"required":["symbol"]}`),
		}},
		MaxOutputTokens: 100,
	})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if len(response.ToolCalls) != 1 || response.ToolCalls[0].Name != "get_market_snapshot" {
		t.Fatalf("tool calls = %#v", response.ToolCalls)
	}
	if response.ToolCalls[0].Arguments != `{"symbol":"BTC"}` {
		t.Fatalf("arguments = %q", response.ToolCalls[0].Arguments)
	}
}

func TestStreamAccumulatesToolCalls(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"id\":\"1\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"gpt-test\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"resolve_symbol\",\"arguments\":\"\"}}]},\"finish_reason\":\"\"}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"id\":\"1\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"gpt-test\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"{\\\"query\\\":\\\"BT\"}}]},\"finish_reason\":\"\"}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"id\":\"1\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"gpt-test\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"C\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	completer := NewCompleter(netguard.Policy{AllowHTTP: true, AllowPrivate: true}, 5*time.Second)
	stream, err := completer.Stream(context.Background(), llm.CompletionRequest{
		BaseURL:         server.URL + "/v1",
		APIKey:          "sk-test",
		Model:           "gpt-test",
		System:          "system",
		User:            "user",
		MaxOutputTokens: 100,
	})
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	for {
		_, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Recv() error = %v", err)
		}
	}
	response := stream.Response()
	if len(response.ToolCalls) != 1 {
		t.Fatalf("tool calls = %#v", response.ToolCalls)
	}
	if response.ToolCalls[0].Name != "resolve_symbol" || response.ToolCalls[0].Arguments != `{"query":"BTC"}` {
		t.Fatalf("tool call = %#v", response.ToolCalls[0])
	}
}

func TestStreamContentAndUsage(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("解析请求失败: %v", err)
		}
		if payload["stream"] != true {
			t.Fatalf("stream = %#v", payload["stream"])
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"id\":\"1\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"gpt-test\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"你\"},\"finish_reason\":\"\"}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"id\":\"1\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"gpt-test\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"好\"},\"finish_reason\":\"\"}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"id\":\"1\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"gpt-test\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":2,\"total_tokens\":5}}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	completer := NewCompleter(netguard.Policy{AllowHTTP: true, AllowPrivate: true}, 5*time.Second)
	stream, err := completer.Stream(context.Background(), llm.CompletionRequest{
		BaseURL:         server.URL + "/v1",
		APIKey:          "sk-test",
		Model:           "gpt-test",
		System:          "system",
		User:            "user",
		MaxOutputTokens: 100,
	})
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	var content strings.Builder
	for {
		delta, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Recv() error = %v", err)
		}
		content.WriteString(delta)
	}
	if content.String() != "你好" {
		t.Fatalf("content = %q", content.String())
	}
	response := stream.Response()
	if response.PromptTokens != 3 || response.CompletionTokens != 2 {
		t.Fatalf("usage = %#v", response)
	}
}
