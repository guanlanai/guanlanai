package openai

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	openai "github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/packages/param"
	"github.com/openai/openai-go/shared"

	"github.com/guanlanai/guanlanai/internal/llm"
	"github.com/guanlanai/guanlanai/internal/netguard"
)

// Completer 使用 OpenAI 官方 SDK 调 Chat Completions。
type Completer struct {
	policy     netguard.Policy
	httpClient *http.Client
	timeout    time.Duration
}

// NewCompleter 创建 Chat Completions 客户端。
func NewCompleter(policy netguard.Policy, timeout time.Duration) *Completer {
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	return &Completer{
		policy:     policy,
		httpClient: netguard.NewHTTPClient(policy, timeout),
		timeout:    timeout,
	}
}

func (c *Completer) Complete(ctx context.Context, req llm.CompletionRequest) (*llm.CompletionResponse, error) {
	if strings.TrimSpace(req.APIKey) == "" {
		return nil, llm.ErrInvalidKey
	}
	baseURL, err := c.policy.Validate(ctx, req.BaseURL)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.Model) == "" {
		return nil, fmt.Errorf("模型不能为空")
	}

	client := openai.NewClient(
		option.WithAPIKey(req.APIKey),
		option.WithBaseURL(baseURL),
		option.WithHTTPClient(c.httpClient),
		option.WithMaxRetries(0),
		option.WithRequestTimeout(c.timeout),
	)

	maxTokens := req.MaxOutputTokens
	if maxTokens <= 0 {
		maxTokens = 1200
	}
	params := openai.ChatCompletionNewParams{
		Model: shared.ChatModel(req.Model),
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage(req.System),
			openai.UserMessage(req.User),
		},
		MaxCompletionTokens: param.NewOpt(int64(maxTokens)),
	}
	// gpt-5 / o 系列推理模型通常不接受自定义 temperature。
	if supportsTemperature(req.Model) {
		params.Temperature = param.NewOpt(req.Temperature)
	}
	if req.JSONMode {
		format := shared.NewResponseFormatJSONObjectParam()
		params.ResponseFormat = openai.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONObject: &format,
		}
	}

	response, err := client.Chat.Completions.New(ctx, params)
	if err != nil {
		return nil, mapError(err)
	}
	if len(response.Choices) == 0 {
		return nil, errors.New("模型没有返回候选结果")
	}
	content := strings.TrimSpace(response.Choices[0].Message.Content)
	if content == "" {
		return nil, errors.New("模型返回空内容")
	}

	return &llm.CompletionResponse{
		Content:          content,
		Model:            response.Model,
		PromptTokens:     int(response.Usage.PromptTokens),
		CompletionTokens: int(response.Usage.CompletionTokens),
	}, nil
}

func supportsTemperature(model string) bool {
	lower := strings.ToLower(model)
	for _, prefix := range []string{"gpt-5", "o1", "o3", "o4"} {
		if strings.HasPrefix(lower, prefix) {
			return false
		}
	}
	return true
}

func mapError(err error) error {
	var apiErr *openai.Error
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case http.StatusUnauthorized, http.StatusForbidden:
			return fmt.Errorf("%w: %s", llm.ErrInvalidKey, apiErr.Message)
		case http.StatusTooManyRequests:
			return fmt.Errorf("%w: 上游限流", llm.ErrValidationUnavailable)
		default:
			return fmt.Errorf("模型调用失败: HTTP %d %s", apiErr.StatusCode, apiErr.Message)
		}
	}
	return fmt.Errorf("模型调用失败: %w", err)
}
