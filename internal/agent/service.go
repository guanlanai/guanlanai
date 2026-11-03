package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/guanlanai/guanlanai/internal/analysis"
	"github.com/guanlanai/guanlanai/internal/llm"
	"github.com/guanlanai/guanlanai/internal/logging"
	"github.com/guanlanai/guanlanai/internal/market"
	"github.com/guanlanai/guanlanai/internal/session"
	"github.com/guanlanai/guanlanai/internal/user"
)

var (
	// ErrNoModel 表示用户和系统都没有配置模型。
	ErrNoModel = errors.New("未配置模型")
	// ErrPromptTooLong 表示输入上下文超出预算，需要用户缩短问题或重置会话。
	ErrPromptTooLong = errors.New("输入超出上下文预算")
)

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
	snapshots    *market.SnapshotCache

	maxToolRounds int
	maxPrompt     int
	streaming     bool
}

// Option 调整 Agent 服务的可选行为。
type Option func(*Service)

// WithMaxToolRounds 设置追问时允许的工具调用轮数；0 表示关闭工具。
func WithMaxToolRounds(rounds int) Option {
	return func(s *Service) {
		if rounds < 0 {
			rounds = 0
		}
		s.maxToolRounds = rounds
	}
}

// WithStreaming 控制追问是否使用流式输出。
func WithStreaming(enabled bool) Option {
	return func(s *Service) {
		s.streaming = enabled
	}
}

// WithMaxPromptTokens 设置单次 LLM 调用的输入预算。
func WithMaxPromptTokens(tokens int) Option {
	return func(s *Service) {
		if tokens > 0 {
			s.maxPrompt = tokens
		}
	}
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
	opts ...Option,
) *Service {
	if maxOutput <= 0 {
		maxOutput = 2000
	}
	if maxConcurrency <= 0 {
		maxConcurrency = 8
	}
	service := &Service{
		resolver:      resolver,
		provider:      provider,
		completer:     completer,
		defaultModel:  defaultModel,
		maxOutput:     maxOutput,
		logger:        logger,
		sem:           make(chan struct{}, maxConcurrency),
		snapshots:     market.NewSnapshotCache(200),
		maxToolRounds: 3,
		maxPrompt:     6000,
		streaming:     true,
	}
	for _, opt := range opts {
		opt(service)
	}
	return service
}

// Analyze 对用户输入的币种生成结构化观点。
func (s *Service) Analyze(ctx context.Context, usr *user.User, apiKey, query, mode string, strategy analysis.Strategy, timeframe market.Timeframe, indicators []analysis.Indicator, sessionID int64) (*Result, error) {
	if usr == nil {
		return nil, errors.New("用户配置为空")
	}
	if strings.TrimSpace(apiKey) == "" {
		return nil, llm.ErrInvalidKey
	}
	if mode != "short" && mode != "swing" {
		mode = "swing"
	}
	if !timeframe.Valid() {
		timeframe = market.DefaultTimeframe
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

	timeframes := analysis.Timeframes(strategy, timeframe)
	snapshot, ok := s.snapshots.GetMany(sessionID, symbol, timeframes)
	if !ok {
		snapshot, err = s.provider.Snapshot(ctx, symbol, timeframes)
		if err != nil {
			return nil, err
		}
		s.snapshots.Put(sessionID, snapshot)
	}
	report, err := analysis.BuildStrategyReport(
		snapshot, mode, strategy, analysis.ScreenSpecs(strategy, timeframe), indicators,
	)
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
			"trace_id", logging.TraceID(ctx),
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
		"trace_id", logging.TraceID(ctx),
		"symbol", report.Symbol,
		"mode", mode,
		"strategy", string(strategy),
		"timeframe", analysis.TimeframeLabel(strategy, timeframe),
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
	if err := s.checkPromptBudget([]llm.Message{
		{Role: llm.RoleSystem, Content: system},
		{Role: llm.RoleUser, Content: userPrompt},
	}, nil); err != nil {
		return nil, err
	}
	request := llm.CompletionRequest{
		BaseURL:         baseURL,
		APIKey:          apiKey,
		Model:           model,
		System:          system,
		User:            userPrompt,
		MaxOutputTokens: maxOutput,
		Temperature:     0.3,
		JSONMode:        jsonMode,
	}
	if jsonMode {
		request.JSONSchema = &llm.JSONSchema{
			Name:        "trade_opinion",
			Description: "结构化加密货币交易观点",
			Schema:      json.RawMessage(OpinionJSONSchema),
		}
	}
	return s.completer.Complete(ctx, request)
}

// Chat 基于最近一次分析回答追问；需要时最多进行 maxToolRounds 轮只读工具调用。
func (s *Service) Chat(ctx context.Context, usr *user.User, apiKey, question, contextText string, sessionID int64) (*ChatResult, error) {
	return s.chat(ctx, usr, apiKey, question, contextText, sessionID, nil)
}

// ChatStream 与 Chat 相同，但会通过 onDelta 增量返回文本。
// 工具调用轮次不会产生可见文本；onDelta 为 nil 时退化为普通 Chat。
func (s *Service) ChatStream(ctx context.Context, usr *user.User, apiKey, question, contextText string, sessionID int64, onDelta func(string) error) (*ChatResult, error) {
	return s.chat(ctx, usr, apiKey, question, contextText, sessionID, onDelta)
}

func (s *Service) chat(ctx context.Context, usr *user.User, apiKey, question, contextText string, sessionID int64, onDelta func(string) error) (*ChatResult, error) {
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

	mode := "swing"
	if usr != nil && (usr.Mode == "short" || usr.Mode == "swing") {
		mode = usr.Mode
	}

	rounds := s.maxToolRounds
	if rounds < 0 {
		rounds = 0
	}
	system := ChatSystemPrompt
	if rounds > 0 {
		system = ToolChatSystemPrompt
	}

	prompt := "<context>\n" + contextText + "\n</context>\n\n<user_question>\n" + question + "\n</user_question>"
	messages := []llm.Message{
		{Role: llm.RoleSystem, Content: system},
		{Role: llm.RoleUser, Content: prompt},
	}

	tools := s.toolDefinitions()
	if rounds == 0 {
		tools = nil
	}
	cache := make(map[string]string)
	total := llm.CompletionResponse{Model: model}
	toolsSupported := len(tools) > 0
	toolCallsExecuted := 0
	toolResultTokens := 0

	if s.maxPrompt > 0 && estimatePromptTokens(question) > s.maxPrompt/2 {
		return nil, fmt.Errorf("%w: 追问过长", ErrPromptTooLong)
	}
	if err := s.checkPromptBudget(messages, tools); err != nil {
		// 工具定义会占用预算；上下文本身没超时先尝试关闭工具。
		if len(tools) == 0 {
			return nil, err
		}
		tools = nil
		toolsSupported = false
		if err := s.checkPromptBudget(messages, nil); err != nil {
			return nil, err
		}
	}

	for round := 0; round < rounds; round++ {
		if err := s.checkPromptBudget(messages, tools); err != nil {
			return nil, err
		}
		response, err := s.completeOrStream(ctx, llm.CompletionRequest{
			BaseURL:         baseURL,
			APIKey:          apiKey,
			Model:           model,
			Messages:        messages,
			Tools:           tools,
			MaxOutputTokens: s.maxOutput,
			Temperature:     0.3,
		}, onDelta)
		if err != nil {
			if toolsSupported && errors.Is(err, llm.ErrUnsupportedFeature) {
				toolsSupported = false
				tools = nil
				round--
				continue
			}
			return nil, err
		}
		accumulateUsage(&total, response)
		if len(response.ToolCalls) == 0 {
			return chatResult(response, &total)
		}
		for index := range response.ToolCalls {
			if response.ToolCalls[index].ID == "" {
				response.ToolCalls[index].ID = fmt.Sprintf("call_%d_%d", round, index)
			}
		}

		messages = append(messages, llm.Message{
			Role:      llm.RoleAssistant,
			Content:   response.Content,
			ToolCalls: response.ToolCalls,
		})
		for index, call := range response.ToolCalls {
			var result string
			switch {
			case index >= maxToolCallsPerRound:
				result = toolError(fmt.Errorf("单轮工具调用超过 %d 个，本次调用未执行", maxToolCallsPerRound))
				s.logger.Warn("单轮工具调用超过上限",
					"trace_id", logging.TraceID(ctx),
					"tool", call.Name,
					"index", index,
				)
			case toolCallsExecuted >= maxToolCallsPerTurn:
				result = toolError(fmt.Errorf("单次追问工具调用超过 %d 个，本次调用未执行", maxToolCallsPerTurn))
			default:
				s.logger.Info("执行只读工具",
					"trace_id", logging.TraceID(ctx),
					"tool", call.Name,
					"arguments", truncateRunes(call.Arguments, 300),
				)
				result = s.executeTool(ctx, mode, sessionID, cache, call)
				toolCallsExecuted++
			}

			result = truncateToTokenBudget(result, maxToolResultTokens)
			if toolResultTokens+estimatePromptTokens(result) > maxToolResultTokensTotal {
				result = toolError(fmt.Errorf("本次追问的工具结果预算已用完，请基于已有信息直接回答"))
			} else {
				toolResultTokens += estimatePromptTokens(result)
			}
			messages = append(messages, llm.Message{
				Role:       llm.RoleTool,
				Name:       call.Name,
				ToolCallID: call.ID,
				Content:    "<tool_result>\n" + result + "\n</tool_result>",
			})
		}
	}

	// 达到工具轮数上限后，再给模型一次不带工具的收口机会。
	if len(messages) > 0 {
		if rounds > 0 {
			messages = append(messages, llm.Message{
				Role:    llm.RoleUser,
				Content: "已达到工具调用上限。请只基于上面的信息直接回答，不要再调用工具。",
			})
		}
		if err := s.checkPromptBudget(messages, nil); err != nil {
			return nil, err
		}
		response, err := s.completeOrStream(ctx, llm.CompletionRequest{
			BaseURL:         baseURL,
			APIKey:          apiKey,
			Model:           model,
			Messages:        messages,
			MaxOutputTokens: s.maxOutput,
			Temperature:     0.3,
		}, onDelta)
		if err != nil {
			return nil, err
		}
		accumulateUsage(&total, response)
		return chatResult(response, &total)
	}
	return nil, fmt.Errorf("%w: 工具调用轮数耗尽", llm.ErrEmptyResponse)
}

func (s *Service) completeOrStream(ctx context.Context, req llm.CompletionRequest, onDelta func(string) error) (*llm.CompletionResponse, error) {
	if onDelta == nil || !s.streaming {
		return s.completer.Complete(ctx, req)
	}
	streamer, ok := s.completer.(llm.Streamer)
	if !ok {
		return s.completer.Complete(ctx, req)
	}
	stream, err := streamer.Stream(ctx, req)
	if err != nil {
		if errors.Is(err, llm.ErrUnsupportedFeature) {
			return s.completer.Complete(ctx, req)
		}
		return nil, err
	}
	defer stream.Close()

	received := false
	for {
		delta, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return stream.Response(), nil
		}
		if err != nil {
			// 还没有任何可见内容时，流式失败可以安全地退回普通补全。
			if !received {
				return s.completer.Complete(ctx, req)
			}
			return nil, err
		}
		if delta != "" {
			received = true
			if err := onDelta(delta); err != nil {
				return nil, err
			}
		}
	}
}

func chatResult(response *llm.CompletionResponse, total *llm.CompletionResponse) (*ChatResult, error) {
	content := strings.TrimSpace(response.Content)
	if content == "" {
		return nil, fmt.Errorf("%w: 模型返回空回答", llm.ErrEmptyResponse)
	}
	return &ChatResult{Content: content, Usage: total}, nil
}

func accumulateUsage(total *llm.CompletionResponse, response *llm.CompletionResponse) {
	if response == nil {
		return
	}
	total.PromptTokens += response.PromptTokens
	total.CompletionTokens += response.CompletionTokens
	if response.Model != "" {
		total.Model = response.Model
	}
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
		builder.WriteString("<existing_summary>\n")
		builder.WriteString(existingSummary)
		builder.WriteString("\n</existing_summary>\n\n")
	}
	builder.WriteString("<conversation>\n")
	for _, message := range messages {
		builder.WriteString(message.Role)
		builder.WriteString(": ")
		builder.WriteString(message.Content)
		builder.WriteString("\n")
	}
	builder.WriteString("</conversation>")

	completion, err := s.completeWithSystem(ctx, baseURL, apiKey, model, SummarizeSystemPrompt, builder.String(), false, 1000)
	if err != nil {
		return nil, err
	}
	content := strings.TrimSpace(completion.Content)
	if content == "" {
		return nil, fmt.Errorf("%w: 模型返回空摘要", llm.ErrEmptyResponse)
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
	return "<market_data>\n" + string(data) + "\n</market_data>\n请基于以上行情和技术指标给出交易观点。", nil
}
