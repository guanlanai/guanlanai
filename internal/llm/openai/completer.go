package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strings"
	"time"

	openai "github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/packages/param"
	"github.com/openai/openai-go/packages/ssestream"
	"github.com/openai/openai-go/shared"

	"github.com/guanlanai/guanlanai/internal/llm"
	"github.com/guanlanai/guanlanai/internal/netguard"
	"github.com/guanlanai/guanlanai/internal/resilience"
)

// Completer 使用 OpenAI 官方 SDK 调 Chat Completions。
type Completer struct {
	policy     netguard.Policy
	httpClient *http.Client
	timeout    time.Duration
	breaker    *resilience.Breaker
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
		breaker:    resilience.New(5, 30*time.Second),
	}
}

func (c *Completer) Complete(ctx context.Context, req llm.CompletionRequest) (*llm.CompletionResponse, error) {
	if err := c.check(req); err != nil {
		return nil, err
	}

	response, err := c.doComplete(ctx, req)
	if err != nil && req.JSONSchema != nil && errors.Is(err, llm.ErrUnsupportedFeature) {
		// 代理不支持 json_schema 时降级到 json_object，本地校验仍然兜底。
		fallback := req
		fallback.JSONSchema = nil
		fallback.JSONMode = true
		return c.doComplete(ctx, fallback)
	}
	return response, err
}

// Stream 发起流式 Chat Completions 调用。
func (c *Completer) Stream(ctx context.Context, req llm.CompletionRequest) (llm.Stream, error) {
	if err := c.check(req); err != nil {
		return nil, err
	}
	baseURL, err := c.policy.Validate(ctx, req.BaseURL)
	if err != nil {
		return nil, err
	}
	req.BaseURL = baseURL

	params, err := c.buildParams(req)
	if err != nil {
		return nil, err
	}
	params.StreamOptions = openai.ChatCompletionStreamOptionsParam{
		IncludeUsage: param.NewOpt(true),
	}

	client := c.client(req)
	stream := client.Chat.Completions.NewStreaming(ctx, params)
	return &chatStream{stream: stream, breaker: c.breaker}, nil
}

func (c *Completer) check(req llm.CompletionRequest) error {
	if strings.TrimSpace(req.APIKey) == "" {
		return llm.ErrInvalidKey
	}
	if strings.TrimSpace(req.Model) == "" {
		return fmt.Errorf("模型不能为空")
	}
	if err := c.breaker.Allow(); err != nil {
		return fmt.Errorf("%w: %w", llm.ErrValidationUnavailable, err)
	}
	return nil
}

func (c *Completer) client(req llm.CompletionRequest) openai.Client {
	return openai.NewClient(
		option.WithAPIKey(req.APIKey),
		option.WithBaseURL(req.BaseURL),
		option.WithHTTPClient(c.httpClient),
		option.WithMaxRetries(0),
		option.WithRequestTimeout(c.timeout),
	)
}

func (c *Completer) doComplete(ctx context.Context, req llm.CompletionRequest) (*llm.CompletionResponse, error) {
	baseURL, err := c.policy.Validate(ctx, req.BaseURL)
	if err != nil {
		return nil, err
	}
	req.BaseURL = baseURL

	params, err := c.buildParams(req)
	if err != nil {
		return nil, err
	}

	client := c.client(req)
	response, err := client.Chat.Completions.New(ctx, params)
	if err != nil {
		if isUnsupportedFeature(err) {
			return nil, fmt.Errorf("%w: %s", llm.ErrUnsupportedFeature, errorMessage(err))
		}
		if isTransientLLMError(err) {
			c.breaker.Record(false)
		} else {
			c.breaker.Record(true)
		}
		return nil, mapError(err)
	}
	c.breaker.Record(true)
	if len(response.Choices) == 0 {
		return nil, errors.New("模型没有返回候选结果")
	}

	choice := response.Choices[0]
	content := strings.TrimSpace(choice.Message.Content)
	toolCalls := make([]llm.ToolCall, 0, len(choice.Message.ToolCalls))
	for _, call := range choice.Message.ToolCalls {
		toolCalls = append(toolCalls, llm.ToolCall{
			ID:        call.ID,
			Name:      call.Function.Name,
			Arguments: call.Function.Arguments,
		})
	}
	if content == "" && len(toolCalls) == 0 {
		return nil, fmt.Errorf("%w: finish_reason=%s completion_tokens=%d reasoning_tokens=%d",
			llm.ErrEmptyResponse,
			choice.FinishReason,
			response.Usage.CompletionTokens,
			response.Usage.CompletionTokensDetails.ReasoningTokens,
		)
	}

	return &llm.CompletionResponse{
		Content:          content,
		Model:            response.Model,
		PromptTokens:     int(response.Usage.PromptTokens),
		CompletionTokens: int(response.Usage.CompletionTokens),
		ToolCalls:        toolCalls,
		FinishReason:     choice.FinishReason,
	}, nil
}

func (c *Completer) buildParams(req llm.CompletionRequest) (openai.ChatCompletionNewParams, error) {
	messages, err := buildMessages(req)
	if err != nil {
		return openai.ChatCompletionNewParams{}, err
	}
	tools, err := buildTools(req.Tools)
	if err != nil {
		return openai.ChatCompletionNewParams{}, err
	}

	maxTokens := req.MaxOutputTokens
	if maxTokens <= 0 {
		maxTokens = 1200
	}
	params := openai.ChatCompletionNewParams{
		Model:               shared.ChatModel(req.Model),
		Messages:            messages,
		MaxCompletionTokens: param.NewOpt(int64(maxTokens)),
	}
	if len(tools) > 0 {
		params.Tools = tools
	}
	// gpt-5 / o 系列推理模型通常不接受自定义 temperature。
	if supportsTemperature(req.Model) {
		params.Temperature = param.NewOpt(req.Temperature)
	}
	if req.JSONSchema != nil {
		var schema map[string]any
		if err := json.Unmarshal(req.JSONSchema.Schema, &schema); err != nil {
			return openai.ChatCompletionNewParams{}, fmt.Errorf("解析 JSON Schema: %w", err)
		}
		jsonSchema := shared.ResponseFormatJSONSchemaJSONSchemaParam{
			Name:   req.JSONSchema.Name,
			Schema: schema,
		}
		if req.JSONSchema.Description != "" {
			jsonSchema.Description = param.NewOpt(req.JSONSchema.Description)
		}
		if req.JSONSchema.Strict {
			jsonSchema.Strict = param.NewOpt(true)
		}
		format := shared.ResponseFormatJSONSchemaParam{JSONSchema: jsonSchema}
		params.ResponseFormat = openai.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONSchema: &format,
		}
	} else if req.JSONMode {
		format := shared.NewResponseFormatJSONObjectParam()
		params.ResponseFormat = openai.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONObject: &format,
		}
	}
	return params, nil
}

func buildMessages(req llm.CompletionRequest) ([]openai.ChatCompletionMessageParamUnion, error) {
	if len(req.Messages) == 0 {
		return []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage(req.System),
			openai.UserMessage(req.User),
		}, nil
	}

	messages := make([]openai.ChatCompletionMessageParamUnion, 0, len(req.Messages)+1)
	if req.System != "" && (len(req.Messages) == 0 || req.Messages[0].Role != llm.RoleSystem) {
		messages = append(messages, openai.SystemMessage(req.System))
	}
	for _, message := range req.Messages {
		switch message.Role {
		case llm.RoleSystem:
			messages = append(messages, openai.SystemMessage(message.Content))
		case llm.RoleUser:
			messages = append(messages, openai.UserMessage(message.Content))
		case llm.RoleAssistant:
			messages = append(messages, assistantMessage(message))
		case llm.RoleTool:
			messages = append(messages, openai.ToolMessage(message.Content, message.ToolCallID))
		default:
			return nil, fmt.Errorf("不支持的消息角色 %q", message.Role)
		}
	}
	return messages, nil
}

func assistantMessage(message llm.Message) openai.ChatCompletionMessageParamUnion {
	assistant := openai.ChatCompletionAssistantMessageParam{}
	if message.Content != "" {
		assistant.Content.OfString = param.NewOpt(message.Content)
	}
	for _, call := range message.ToolCalls {
		assistant.ToolCalls = append(assistant.ToolCalls, openai.ChatCompletionMessageToolCallParam{
			ID: call.ID,
			Function: openai.ChatCompletionMessageToolCallFunctionParam{
				Name:      call.Name,
				Arguments: call.Arguments,
			},
		})
	}
	return openai.ChatCompletionMessageParamUnion{OfAssistant: &assistant}
}

func buildTools(tools []llm.Tool) ([]openai.ChatCompletionToolParam, error) {
	if len(tools) == 0 {
		return nil, nil
	}
	result := make([]openai.ChatCompletionToolParam, 0, len(tools))
	for _, tool := range tools {
		parameters := shared.FunctionParameters{}
		if len(tool.Parameters) > 0 {
			var schema map[string]any
			if err := json.Unmarshal(tool.Parameters, &schema); err != nil {
				return nil, fmt.Errorf("解析工具 %s 的参数 schema: %w", tool.Name, err)
			}
			parameters = shared.FunctionParameters(schema)
		}
		definition := shared.FunctionDefinitionParam{
			Name:       tool.Name,
			Parameters: parameters,
		}
		if tool.Description != "" {
			definition.Description = param.NewOpt(tool.Description)
		}
		result = append(result, openai.ChatCompletionToolParam{Function: definition})
	}
	return result, nil
}

type chatStream struct {
	stream    *ssestream.Stream[openai.ChatCompletionChunk]
	breaker   *resilience.Breaker
	content   strings.Builder
	model     string
	prompt    int
	completed int
	finish    string
	toolCalls map[int64]*llm.ToolCall
	done      bool
}

func (s *chatStream) Recv() (string, error) {
	if s.done {
		return "", io.EOF
	}
	for s.stream.Next() {
		chunk := s.stream.Current()
		if chunk.Model != "" {
			s.model = chunk.Model
		}
		if chunk.Usage.PromptTokens > 0 {
			s.prompt = int(chunk.Usage.PromptTokens)
			s.completed = int(chunk.Usage.CompletionTokens)
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		choice := chunk.Choices[0]
		if choice.FinishReason != "" {
			s.finish = choice.FinishReason
		}
		for _, call := range choice.Delta.ToolCalls {
			if s.toolCalls == nil {
				s.toolCalls = make(map[int64]*llm.ToolCall)
			}
			current := s.toolCalls[call.Index]
			if current == nil {
				current = &llm.ToolCall{}
				s.toolCalls[call.Index] = current
			}
			if call.ID != "" {
				current.ID = call.ID
			}
			if call.Function.Name != "" {
				current.Name = call.Function.Name
			}
			current.Arguments += call.Function.Arguments
		}
		if choice.Delta.Content != "" {
			s.content.WriteString(choice.Delta.Content)
			return choice.Delta.Content, nil
		}
	}
	if err := s.stream.Err(); err != nil {
		s.done = true
		if !isUnsupportedFeature(err) {
			s.record(!isTransientLLMError(err))
		}
		if isUnsupportedFeature(err) {
			return "", fmt.Errorf("%w: %s", llm.ErrUnsupportedFeature, errorMessage(err))
		}
		return "", mapError(err)
	}
	s.done = true
	s.record(true)
	if s.content.Len() == 0 && len(s.toolCalls) == 0 {
		return "", fmt.Errorf("%w: finish_reason=%s", llm.ErrEmptyResponse, s.finish)
	}
	return "", io.EOF
}

func (s *chatStream) record(success bool) {
	if s.breaker == nil {
		return
	}
	s.breaker.Record(success)
}

func (s *chatStream) Response() *llm.CompletionResponse {
	calls := make([]llm.ToolCall, 0, len(s.toolCalls))
	indexes := make([]int64, 0, len(s.toolCalls))
	for index := range s.toolCalls {
		indexes = append(indexes, index)
	}
	sort.Slice(indexes, func(i, j int) bool { return indexes[i] < indexes[j] })
	for _, index := range indexes {
		calls = append(calls, *s.toolCalls[index])
	}
	return &llm.CompletionResponse{
		Content:          s.content.String(),
		Model:            s.model,
		PromptTokens:     s.prompt,
		CompletionTokens: s.completed,
		ToolCalls:        calls,
		FinishReason:     s.finish,
	}
}

func (s *chatStream) Close() error {
	return s.stream.Close()
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

func isTransientLLMError(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, llm.ErrValidationUnavailable) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	var apiErr *openai.Error
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode == http.StatusTooManyRequests || apiErr.StatusCode >= 500
	}
	return false
}

func isUnsupportedFeature(err error) bool {
	var apiErr *openai.Error
	if !errors.As(err, &apiErr) {
		return false
	}
	switch apiErr.StatusCode {
	case http.StatusBadRequest, http.StatusNotFound, http.StatusUnprocessableEntity:
	default:
		return false
	}
	message := strings.ToLower(apiErr.Message)
	for _, token := range []string{
		"tool", "function call", "function calling", "response_format",
		"json_schema", "json schema", "structured output",
	} {
		if strings.Contains(message, token) {
			return true
		}
	}
	return false
}

func errorMessage(err error) string {
	var apiErr *openai.Error
	if errors.As(err, &apiErr) {
		return apiErr.Message
	}
	return err.Error()
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
