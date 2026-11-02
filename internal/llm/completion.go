package llm

import "context"

// CompletionRequest 是一次 Chat Completions 调用。
type CompletionRequest struct {
	BaseURL         string
	APIKey          string
	Model           string
	System          string
	User            string
	MaxOutputTokens int
	Temperature     float64
	JSONMode        bool
}

// CompletionResponse 是模型返回的文本和用量。
type CompletionResponse struct {
	Content          string
	Model            string
	PromptTokens     int
	CompletionTokens int
}

// Completer 是 Chat Completions 抽象，方便测试和替换厂商。
type Completer interface {
	Complete(ctx context.Context, req CompletionRequest) (*CompletionResponse, error)
}
