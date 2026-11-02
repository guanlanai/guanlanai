package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/guanlanai/guanlanai/internal/analysis"
	"github.com/guanlanai/guanlanai/internal/llm"
	"github.com/guanlanai/guanlanai/internal/market"
	"github.com/guanlanai/guanlanai/internal/session"
	"github.com/guanlanai/guanlanai/internal/user"
)

var ErrNoModel = errors.New("未配置模型")

// Result 是一次完整分析的结果。
type Result struct {
	Report  *analysis.Report
	Opinion *Opinion
	Usage   *llm.CompletionResponse
}

// ChatResult 是一次追问的结果。
type ChatResult struct {
	Content string
	Usage   *llm.CompletionResponse
}

// Service 编排行情、技术指标和 LLM 观点生成。
type Service struct {
	resolver     market.Resolver
	provider     market.Provider
	completer    llm.Completer
	defaultModel string
	maxOutput    int
	logger       *slog.Logger
	sem          chan struct{}
}

// New 创建分析服务。
func New(
	resolver market.Resolver,
	provider market.Provider,
	completer llm.Completer,
	defaultModel string,
	maxOutput int,
	maxConcurrency int,
	logger *slog.Logger,
) *Service {
	if maxOutput <= 0 {
		maxOutput = 2000
	}
	if maxConcurrency <= 0 {
		maxConcurrency = 8
	}
	return &Service{
		resolver:     resolver,
		provider:     provider,
		completer:    completer,
		defaultModel: defaultModel,
		maxOutput:    maxOutput,
		logger:       logger,
		sem:          make(chan struct{}, maxConcurrency),
	}
}

// Analyze 对用户输入的币种生成结构化观点。
func (s *Service) Analyze(ctx context.Context, usr *user.User, apiKey, query, mode string) (*Result, error) {
	if usr == nil {
		return nil, errors.New("用户配置为空")
	}
	if strings.TrimSpace(apiKey) == "" {
		return nil, llm.ErrInvalidKey
	}
	if mode != "short" && mode != "swing" {
		mode = "swing"
	}
	model := strings.TrimSpace(usr.Model)
	if model == "" {
		model = s.defaultModel
	}
	if model == "" {
		return nil, ErrNoModel
	}
	baseURL := strings.TrimSpace(usr.APIBaseURL)
	if baseURL == "" {
		return nil, errors.New("未配置 OpenAI endpoint")
	}

	ctx, cancel := context.WithTimeout(ctx, 75*time.Second)
	defer cancel()
	if err := s.acquire(ctx); err != nil {
		return nil, err
	}
	defer s.release()

	started := time.Now()
	symbol, err := s.resolver.Resolve(ctx, query)
	if err != nil {
		return nil, err
	}

	snapshot, err := s.provider.Snapshot(ctx, symbol, market.TimeframesForMode(mode))
	if err != nil {
		return nil, err
	}
	report, err := analysis.BuildReport(snapshot, mode)
	if err != nil {
		return nil, err
	}

	userPrompt, err := buildUserPrompt(report)
	if err != nil {
		return nil, err
	}

	completion, err := s.complete(ctx, baseURL, apiKey, model, userPrompt, true, s.maxOutput)
	if err != nil {
		return nil, err
	}
	opinion, err := ParseOpinion(completion.Content, report)
	if err != nil {
		s.logger.Warn("模型输出校验失败，尝试修复一次",
			"symbol", report.Symbol,
			"model", model,
			"error", err,
		)
		correction := userPrompt + "\n\n上一次输出无法通过校验：" + err.Error() + "。请只返回修正后的 JSON。"
		completion, err = s.complete(ctx, baseURL, apiKey, model, correction, true, s.maxOutput)
		if err != nil {
			return nil, err
		}
		opinion, err = ParseOpinion(completion.Content, report)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidOutput, err)
		}
	}

	s.logger.Info("分析完成",
		"symbol", report.Symbol,
		"mode", mode,
		"model", model,
		"duration_ms", time.Since(started).Milliseconds(),
		"prompt_tokens", completion.PromptTokens,
		"completion_tokens", completion.CompletionTokens,
	)

	return &Result{Report: report, Opinion: opinion, Usage: completion}, nil
}

func (s *Service) complete(ctx context.Context, baseURL, apiKey, model, userPrompt string, jsonMode bool, maxOutput int) (*llm.CompletionResponse, error) {
	system := SystemPrompt
	if !jsonMode {
		system = ChatSystemPrompt
	}
	return s.completeWithSystem(ctx, baseURL, apiKey, model, system, userPrompt, jsonMode, maxOutput)
}

func (s *Service) completeWithSystem(ctx context.Context, baseURL, apiKey, model, system, userPrompt string, jsonMode bool, maxOutput int) (*llm.CompletionResponse, error) {
	return s.completer.Complete(ctx, llm.CompletionRequest{
		BaseURL:         baseURL,
		APIKey:          apiKey,
		Model:           model,
		System:          system,
		User:            userPrompt,
		MaxOutputTokens: maxOutput,
		Temperature:     0.3,
		JSONMode:        jsonMode,
	})
}

// Chat 基于最近一次分析回答追问。
func (s *Service) Chat(ctx context.Context, usr *user.User, apiKey, question, contextText string) (*ChatResult, error) {
	if strings.TrimSpace(apiKey) == "" {
		return nil, llm.ErrInvalidKey
	}
	model, baseURL, err := s.modelAndBase(usr)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if err := s.acquire(ctx); err != nil {
		return nil, err
	}
	defer s.release()

	prompt := "以下是最新的分析上下文：\n" + contextText + "\n\n用户追问：" + question
	completion, err := s.completeWithSystem(ctx, baseURL, apiKey, model, ChatSystemPrompt, prompt, false, 1600)
	if err != nil {
		return nil, err
	}
	content := strings.TrimSpace(completion.Content)
	if content == "" {
		return nil, errors.New("模型返回空回答")
	}
	return &ChatResult{Content: content, Usage: completion}, nil
}

// Summarize 压缩较早的会话消息，保留关键上下文。
func (s *Service) Summarize(ctx context.Context, usr *user.User, apiKey, existingSummary string, messages []session.Message) (*ChatResult, error) {
	if len(messages) == 0 {
		return &ChatResult{Content: existingSummary}, nil
	}
	if strings.TrimSpace(apiKey) == "" {
		return nil, llm.ErrInvalidKey
	}
	model, baseURL, err := s.modelAndBase(usr)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := s.acquire(ctx); err != nil {
		return nil, err
	}
	defer s.release()

	var builder strings.Builder
	if existingSummary != "" {
		builder.WriteString("已有摘要：\n")
		builder.WriteString(existingSummary)
		builder.WriteString("\n\n")
	}
	builder.WriteString("需要压缩的对话：\n")
	for _, message := range messages {
		builder.WriteString(message.Role)
		builder.WriteString(": ")
		builder.WriteString(message.Content)
		builder.WriteString("\n")
	}

	completion, err := s.completeWithSystem(ctx, baseURL, apiKey, model, SummarizeSystemPrompt, builder.String(), false, 1000)
	if err != nil {
		return nil, err
	}
	content := strings.TrimSpace(completion.Content)
	if content == "" {
		return nil, errors.New("模型返回空摘要")
	}
	return &ChatResult{Content: content, Usage: completion}, nil
}

func (s *Service) acquire(ctx context.Context) error {
	select {
	case s.sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Service) release() {
	<-s.sem
}

func (s *Service) modelAndBase(usr *user.User) (string, string, error) {
	if usr == nil {
		return "", "", errors.New("用户配置为空")
	}
	model := strings.TrimSpace(usr.Model)
	if model == "" {
		model = s.defaultModel
	}
	if model == "" {
		return "", "", ErrNoModel
	}
	baseURL := strings.TrimSpace(usr.APIBaseURL)
	if baseURL == "" {
		return "", "", errors.New("未配置 OpenAI endpoint")
	}
	return model, baseURL, nil
}

func buildUserPrompt(report *analysis.Report) (string, error) {
	data, err := json.Marshal(report)
	if err != nil {
		return "", fmt.Errorf("序列化技术面报告: %w", err)
	}
	return "请基于以下行情和技术指标给出交易观点：\n" + string(data), nil
}
