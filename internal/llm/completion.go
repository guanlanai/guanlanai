package llm

import (
	"context"
	"encoding/json"
	"errors"
)

// ErrUnsupportedFeature 表示当前 endpoint 不支持请求的可选能力（工具调用、JSON Schema 等）。
// 上层可以据此降级，而不是把它当成上游故障。
var ErrUnsupportedFeature = errors.New("上游不支持该能力")

// Role 是 Chat Completions 的消息角色。
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Message 是一条对话消息。
// assistant 消息可以携带 ToolCalls；tool 消息通过 ToolCallID 关联到具体调用。
type Message struct {
	Role       Role
	Content    string
	Name       string
	ToolCallID string
	ToolCalls  []ToolCall
}

// ToolCall 是模型请求调用的一次工具。
type ToolCall struct {
	ID        string
	Name      string
	Arguments string
}

// Tool 是暴露给模型的只读工具定义。
// Parameters 是 JSON Schema 对象，原样传给上游。
type Tool struct {
	Name        string
	Description string
	Parameters  json.RawMessage
}

// JSONSchema 要求模型按给定 schema 返回 JSON。
type JSONSchema struct {
	Name        string
	Description string
	Schema      json.RawMessage
	Strict      bool
}

// CompletionRequest 是一次 Chat Completions 调用。
// Messages 优先；为空时回退到 System + User，兼容早期调用方。
type CompletionRequest struct {
	BaseURL         string
	APIKey          string
	Model           string
	System          string
	User            string
	Messages        []Message
	Tools           []Tool
	MaxOutputTokens int
	Temperature     float64
	JSONMode        bool
	JSONSchema      *JSONSchema
}

// CompletionResponse 是模型返回的文本、工具调用和用量。
type CompletionResponse struct {
	Content          string
	Model            string
	PromptTokens     int
	CompletionTokens int
	ToolCalls        []ToolCall
	FinishReason     string
}

// Completer 是 Chat Completions 抽象，方便测试和替换厂商。
type Completer interface {
	Complete(ctx context.Context, req CompletionRequest) (*CompletionResponse, error)
}

// Streamer 是支持增量输出的 Completer。
// Recv 返回内容增量，结束时返回 io.EOF；结束后通过 Response 取聚合结果（含 ToolCalls）。
type Streamer interface {
	Stream(ctx context.Context, req CompletionRequest) (Stream, error)
}

// Stream 是一次流式响应。
type Stream interface {
	Recv() (string, error)
	Response() *CompletionResponse
	Close() error
}
