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
	"github.com/guanlanai/guanlanai/internal/user"
)

var ErrNoModel = errors.New("未配置模型")

// Result 是一次完整分析的结果。
type Result struct {
	Report  *analysis.Report
	Opinion *Opinion
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
}

// New 创建分析服务。
func New(
	resolver market.Resolver,
	provider market.Provider,
	completer llm.Completer,
	defaultModel string,
	maxOutput int,
	logger *slog.Logger,
) *Service {
	if maxOutput <= 0 {
		maxOutput = 1200
	}
	return &Service{
		resolver:     resolver,
		provider:     provider,
		completer:    completer,
		defaultModel: defaultModel,
		maxOutput:    maxOutput,
		logger:       logger,
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

	completion, err := s.complete(ctx, baseURL, apiKey, model, userPrompt)
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
		completion, err = s.complete(ctx, baseURL, apiKey, model, correction)
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

func (s *Service) complete(ctx context.Context, baseURL, apiKey, model, userPrompt string) (*llm.CompletionResponse, error) {
	return s.completer.Complete(ctx, llm.CompletionRequest{
		BaseURL:         baseURL,
		APIKey:          apiKey,
		Model:           model,
		System:          SystemPrompt,
		User:            userPrompt,
		MaxOutputTokens: s.maxOutput,
		Temperature:     0.2,
		JSONMode:        true,
	})
}

func buildUserPrompt(report *analysis.Report) (string, error) {
	data, err := json.Marshal(report)
	if err != nil {
		return "", fmt.Errorf("序列化技术面报告: %w", err)
	}
	return "请基于以下行情和技术指标给出交易观点：\n" + string(data), nil
}
