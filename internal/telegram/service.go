package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/guanlanai/guanlanai/internal/agent"
	"github.com/guanlanai/guanlanai/internal/config"
	"github.com/guanlanai/guanlanai/internal/llm"
	"github.com/guanlanai/guanlanai/internal/logging"
	"github.com/guanlanai/guanlanai/internal/market"
	"github.com/guanlanai/guanlanai/internal/netguard"
	"github.com/guanlanai/guanlanai/internal/ratelimit"
	"github.com/guanlanai/guanlanai/internal/render"
	"github.com/guanlanai/guanlanai/internal/secrets"
	"github.com/guanlanai/guanlanai/internal/session"
	"github.com/guanlanai/guanlanai/internal/text"
	"github.com/guanlanai/guanlanai/internal/usage"
	"github.com/guanlanai/guanlanai/internal/user"
	"github.com/guanlanai/guanlanai/internal/version"
)

var modelNamePattern = regexp.MustCompile(`^[A-Za-z0-9._:/-]{1,100}$`)

// MessageDeleter 用于删除包含 Key 的消息，方便测试时替换。
type MessageDeleter interface {
	DeleteMessage(ctx context.Context, params *bot.DeleteMessageParams) (bool, error)
}

// Analyzer 生成行情观点、回答追问并压缩上下文。
type Analyzer interface {
	Analyze(ctx context.Context, usr *user.User, apiKey, query, mode string) (*agent.Result, error)
	Chat(ctx context.Context, usr *user.User, apiKey, question, contextText string) (*agent.ChatResult, error)
	Summarize(ctx context.Context, usr *user.User, apiKey, existingSummary string, messages []session.Message) (*agent.ChatResult, error)
}

// Service 负责 Telegram 长轮询、命令路由、用户配置和消息发送。
type Service struct {
	bot        *bot.Bot
	sender     render.Sender
	deleter    MessageDeleter
	cfg        config.Config
	logger     *slog.Logger
	dispatcher *Dispatcher
	started    time.Time

	store     user.Store
	sessions  session.Store
	usage     usage.Store
	cipher    secrets.Cipher
	validator llm.Validator
	guard     netguard.Policy
	analyzer  Analyzer
	limiter   *ratelimit.Limiter

	pendingMu sync.Mutex
	pending   map[int64]pendingState
}

type pendingKind int

const (
	pendingKey pendingKind = iota
	pendingBaseURL
)

type pendingState struct {
	kind    pendingKind
	expires time.Time
}

// New 初始化 Telegram Bot。token 无效时会在启动阶段直接失败。
func New(
	cfg config.Config,
	logger *slog.Logger,
	store user.Store,
	sessions session.Store,
	usageStore usage.Store,
	cipher secrets.Cipher,
	validator llm.Validator,
	guard netguard.Policy,
	analyzer Analyzer,
	limiter *ratelimit.Limiter,
) (*Service, error) {
	if strings.TrimSpace(cfg.TelegramToken) == "" {
		return nil, errors.New("TELEGRAM_BOT_TOKEN 不能为空")
	}

	s := &Service{
		cfg:       cfg,
		logger:    logger,
		started:   time.Now(),
		store:     store,
		sessions:  sessions,
		usage:     usageStore,
		cipher:    cipher,
		validator: validator,
		guard:     guard,
		analyzer:  analyzer,
		limiter:   limiter,
		pending:   make(map[int64]pendingState),
	}

	b, err := bot.New(cfg.TelegramToken,
		// 单 worker + 同步 handler 保证 update 按到达顺序进入 dispatcher；
		// dispatcher 再按 chat 串行、全局限并发。
		bot.WithWorkers(1),
		bot.WithNotAsyncHandlers(),
		bot.WithDefaultHandler(s.onUpdate),
		bot.WithErrorsHandler(func(err error) {
			s.logger.Error("Telegram API 错误", "error", err)
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("初始化 Telegram Bot: %w", err)
	}

	s.bot = b
	s.sender = b
	s.deleter = b
	return s, nil
}

// Start 启动长轮询，直到 ctx 取消。
func (s *Service) Start(ctx context.Context) error {
	s.dispatcher = NewDispatcher(ctx, s.handle, s.cfg.TelegramMaxConcurrency, s.logger)

	me, err := s.bot.GetMe(ctx)
	if err != nil {
		return fmt.Errorf("获取 Bot 信息: %w", err)
	}
	s.logger.Info("Telegram Bot 已连接",
		"bot_id", me.ID,
		"username", me.Username,
		"instance", s.cfg.InstanceName,
	)

	s.bot.Start(ctx)

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.dispatcher.Wait(shutdownCtx); err != nil {
		s.logger.Warn("Telegram 任务未在超时内退出", "error", err)
	}
	return nil
}

func (s *Service) onUpdate(ctx context.Context, _ *bot.Bot, update *models.Update) {
	if update.Message == nil {
		return
	}

	msg := update.Message
	if msg.Chat.Type != models.ChatTypePrivate {
		s.send(ctx, msg.Chat.ID, text.PrivateOnly())
		return
	}
	if strings.TrimSpace(msg.Text) == "" {
		s.send(ctx, msg.Chat.ID, text.TextOnly())
		return
	}

	s.logger.Info("收到 Telegram 消息",
		"update_id", update.ID,
		"chat", logging.HashID(msg.Chat.ID),
		"text_len", len([]rune(msg.Text)),
	)

	if s.dispatcher == nil || !s.dispatcher.Enqueue(msg.Chat.ID, update) {
		s.send(ctx, msg.Chat.ID, text.Busy())
	}
}

func (s *Service) handle(ctx context.Context, update *models.Update) {
	if update.Message == nil {
		return
	}
	msg := update.Message
	raw := strings.TrimSpace(msg.Text)
	command, args := parseCommand(raw)

	usr, err := s.ensureUser(ctx, msg)
	if err != nil {
		s.logger.Error("读取用户配置失败", "chat", logging.HashID(msg.Chat.ID), "error", err)
		s.send(ctx, msg.Chat.ID, text.GenericError())
		return
	}

	// 待输入状态优先处理；命令会取消待输入并继续按命令处理。
	pending, hasPending := s.takePending(msg.Chat.ID)
	if hasPending && !strings.HasPrefix(raw, "/") {
		switch pending {
		case pendingKey:
			s.handleKeyInput(ctx, msg, usr)
		case pendingBaseURL:
			s.processBaseURL(ctx, msg.Chat.ID, raw, usr)
		}
		return
	}

	s.logger.Info("处理 Telegram 消息",
		"chat", logging.HashID(msg.Chat.ID),
		"command", command,
		"text_len", len([]rune(raw)),
	)

	sess, err := s.ensureSession(ctx, usr, msg.Chat.ID)
	if err != nil {
		s.logger.Warn("读取会话失败，继续无会话处理", "chat", logging.HashID(msg.Chat.ID), "error", err)
	}

	switch command {
	case "/start":
		message := text.Welcome()
		if !usr.HasKey() {
			message += "\n\n" + text.NeedKey()
		}
		s.send(ctx, msg.Chat.ID, message)
	case "/help":
		s.send(ctx, msg.Chat.ID, text.Help())
	case "/status":
		s.handleStatus(ctx, msg.Chat.ID, usr, sess)
	case "/reset":
		s.handleReset(ctx, msg.Chat.ID, usr)
	case "/cancel":
		s.send(ctx, msg.Chat.ID, "已取消待处理操作。")
	case "/key":
		if strings.EqualFold(strings.TrimSpace(args), "delete") {
			s.handleKeyDelete(ctx, msg.Chat.ID, usr)
			return
		}
		s.setPending(msg.Chat.ID, pendingKey)
		s.send(ctx, msg.Chat.ID, text.KeyPrompt())
	case "/baseurl":
		s.handleBaseURLCommand(ctx, msg.Chat.ID, args, usr)
	case "/model":
		s.handleModelCommand(ctx, msg.Chat.ID, args, usr)
	case "/mode":
		s.handleModeCommand(ctx, msg.Chat.ID, args, usr)
	case "/analyze":
		if args == "" {
			s.send(ctx, msg.Chat.ID, text.NeedSymbol())
			return
		}
		s.handleAnalyze(ctx, msg.Chat.ID, usr, sess, args)
	case "":
		if looksLikeSymbol(raw) && !s.isFollowUp(raw, sess) {
			s.handleAnalyze(ctx, msg.Chat.ID, usr, sess, raw)
			return
		}
		s.handleFollowUp(ctx, msg.Chat.ID, usr, sess, raw)
	default:
		s.send(ctx, msg.Chat.ID, text.UnknownCommand())
	}
}

func (s *Service) handleAnalyze(ctx context.Context, chatID int64, usr *user.User, sess *session.Session, query string) {
	if !usr.HasKey() {
		s.send(ctx, chatID, text.NeedKey())
		return
	}
	if s.analyzer == nil {
		s.send(ctx, chatID, text.Unavailable("行情分析"))
		return
	}
	if s.limiter != nil {
		if ok, retry := s.limiter.AllowAnalysis(usr.TelegramUserID); !ok {
			s.send(ctx, chatID, text.RateLimited(retry))
			return
		}
	}

	if sess == nil {
		var err error
		sess, err = s.ensureSession(ctx, usr, chatID)
		if err != nil {
			s.logger.Warn("读取会话失败，分析继续但不保存上下文", "chat", logging.HashID(chatID), "error", err)
		}
	}

	key, err := s.decryptKey(usr)
	if err != nil {
		s.send(ctx, chatID, text.KeyInvalid("无法解密已保存的 Key，请重新 /key"))
		return
	}

	model := usr.Model
	if model == "" {
		model = s.cfg.DefaultModel
	}
	if model == "" {
		s.send(ctx, chatID, text.NeedModel())
		return
	}

	mode := usr.Mode
	if mode == "" {
		mode = s.cfg.DefaultMode
	}
	if mode != "short" && mode != "swing" {
		mode = "swing"
	}

	started := time.Now()
	if sess != nil {
		_ = s.sessions.AppendMessage(ctx, sess.ID, "user", "分析 "+query+"（"+mode+"）", estimateTokens(query))
	}
	s.send(ctx, chatID, text.Analyzing(query, mode))
	result, err := s.analyzer.Analyze(ctx, usr, key, query, mode)
	if err != nil {
		s.recordUsage(ctx, usr, "analysis", model, nil, time.Since(started), "error")
		s.logger.Error("分析失败",
			"chat", logging.HashID(chatID),
			"query", query,
			"mode", mode,
			"error", err,
		)
		s.send(ctx, chatID, text.AnalysisError(reason(err)))
		return
	}

	card := render.OpinionCard(result.Report, result.Opinion)
	if sess != nil {
		payload, marshalErr := json.Marshal(map[string]any{
			"report":  result.Report,
			"opinion": result.Opinion,
		})
		if marshalErr == nil {
			if _, saveErr := s.sessions.SaveAnalysis(ctx, sess.ID, result.Report.Symbol, result.Report.Market, string(payload)); saveErr != nil {
				s.logger.Warn("保存分析上下文失败", "chat", logging.HashID(chatID), "error", saveErr)
			}
		}
		_ = s.sessions.AppendMessage(ctx, sess.ID, "assistant", card, estimateTokens(card))
	}
	s.recordUsage(ctx, usr, "analysis", model, result.Usage, time.Since(started), "ok")
	s.send(ctx, chatID, card)
}

func (s *Service) handleFollowUp(ctx context.Context, chatID int64, usr *user.User, sess *session.Session, question string) {
	if sess == nil || sess.LastAnalysisJSON == "" {
		s.send(ctx, chatID, text.FollowUpNeedAnalysis())
		return
	}
	if !usr.HasKey() {
		s.send(ctx, chatID, text.NeedKey())
		return
	}
	if s.analyzer == nil {
		s.send(ctx, chatID, text.Unavailable("追问"))
		return
	}
	if s.limiter != nil {
		if ok, retry := s.limiter.AllowChat(usr.TelegramUserID); !ok {
			s.send(ctx, chatID, text.RateLimited(retry))
			return
		}
	}

	key, err := s.decryptKey(usr)
	if err != nil {
		s.send(ctx, chatID, text.KeyInvalid("无法解密已保存的 Key，请重新 /key"))
		return
	}
	model := usr.Model
	if model == "" {
		model = s.cfg.DefaultModel
	}
	if model == "" {
		s.send(ctx, chatID, text.NeedModel())
		return
	}

	messages, err := s.sessions.RecentMessages(ctx, sess.ID, 20)
	if err != nil {
		s.logger.Warn("读取会话消息失败", "chat", logging.HashID(chatID), "error", err)
		messages = nil
	}

	if len(messages) > 12 || estimateMessages(messages) > 3000 {
		cut := len(messages) - 8
		if cut > 0 {
			summaryResult, summaryErr := s.analyzer.Summarize(ctx, usr, key, sess.Summary, messages[:cut])
			if summaryErr != nil {
				s.logger.Warn("压缩会话失败，仅使用最近消息", "chat", logging.HashID(chatID), "error", summaryErr)
				if len(messages) > 8 {
					messages = messages[len(messages)-8:]
				}
			} else {
				s.recordUsage(ctx, usr, "summarize", model, summaryResult.Usage, 0, "ok")
				if err := s.sessions.UpdateSummary(ctx, sess.ID, summaryResult.Content, estimateTokens(summaryResult.Content)); err != nil {
					s.logger.Warn("保存会话摘要失败", "chat", logging.HashID(chatID), "error", err)
				} else {
					sess.Summary = summaryResult.Content
				}
				if err := s.sessions.TrimMessages(ctx, sess.ID, 8); err != nil {
					s.logger.Warn("裁剪会话消息失败", "chat", logging.HashID(chatID), "error", err)
				}
				if recent, recentErr := s.sessions.RecentMessages(ctx, sess.ID, 8); recentErr == nil {
					messages = recent
				}
			}
		}
	}

	var contextBuilder strings.Builder
	if sess.Summary != "" {
		contextBuilder.WriteString("会话摘要：\n")
		contextBuilder.WriteString(sess.Summary)
		contextBuilder.WriteString("\n\n")
	}
	contextBuilder.WriteString("最近一次分析：\n")
	contextBuilder.WriteString(sess.LastAnalysisJSON)
	contextBuilder.WriteString("\n\n")
	if len(messages) > 0 {
		contextBuilder.WriteString("最近对话：\n")
		for _, message := range messages {
			contextBuilder.WriteString(message.Role)
			contextBuilder.WriteString(": ")
			contextBuilder.WriteString(message.Content)
			contextBuilder.WriteString("\n")
		}
	}

	s.send(ctx, chatID, text.Thinking())
	started := time.Now()
	result, err := s.analyzer.Chat(ctx, usr, key, question, contextBuilder.String())
	if err != nil {
		s.recordUsage(ctx, usr, "chat", model, nil, time.Since(started), "error")
		s.logger.Error("追问失败", "chat", logging.HashID(chatID), "error", err)
		s.send(ctx, chatID, text.AnalysisError(reason(err)))
		return
	}

	_ = s.sessions.AppendMessage(ctx, sess.ID, "user", question, estimateTokens(question))
	_ = s.sessions.AppendMessage(ctx, sess.ID, "assistant", result.Content, estimateTokens(result.Content))
	s.recordUsage(ctx, usr, "chat", model, result.Usage, time.Since(started), "ok")
	s.send(ctx, chatID, render.EscapeHTML(result.Content))
}

func (s *Service) handleReset(ctx context.Context, chatID int64, usr *user.User) {
	if s.sessions != nil && usr.ID != 0 {
		if _, err := s.sessions.Reset(ctx, usr.ID, chatID); err != nil {
			s.logger.Warn("重置会话失败", "chat", logging.HashID(chatID), "error", err)
		}
	}
	s.send(ctx, chatID, text.Reset())
}

func (s *Service) ensureSession(ctx context.Context, usr *user.User, chatID int64) (*session.Session, error) {
	if s.sessions == nil {
		return nil, nil
	}
	if usr == nil || usr.ID == 0 {
		return nil, errors.New("用户尚未持久化")
	}
	return s.sessions.GetOrCreateSession(ctx, usr.ID, chatID)
}

func (s *Service) isFollowUp(raw string, sess *session.Session) bool {
	if sess == nil || sess.LastAnalysisJSON == "" {
		return false
	}
	for _, r := range raw {
		if r > unicode.MaxASCII {
			return true
		}
	}
	if strings.ContainsAny(raw, " ?!？!，,。.：:") {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "why", "how", "what", "when", "entry", "stop", "target", "risk", "support", "resistance":
		return true
	}
	return false
}

func (s *Service) recordUsage(ctx context.Context, usr *user.User, kind, model string, completion *llm.CompletionResponse, latency time.Duration, status string) {
	if s.usage == nil || usr == nil || usr.ID == 0 {
		return
	}
	event := usage.Event{
		UserID:    usr.ID,
		RequestID: fmt.Sprintf("%d", time.Now().UnixNano()),
		Kind:      kind,
		Model:     model,
		LatencyMS: int(latency.Milliseconds()),
		Status:    status,
	}
	if completion != nil {
		event.PromptTokens = completion.PromptTokens
		event.CompletionTokens = completion.CompletionTokens
	}
	if err := s.usage.Record(ctx, event); err != nil {
		s.logger.Warn("保存用量失败", "kind", kind, "error", err)
	}
}

func estimateTokens(text string) int {
	count := len([]rune(text))
	if count == 0 {
		return 0
	}
	return count/2 + 1
}

func estimateMessages(messages []session.Message) int {
	total := 0
	for _, message := range messages {
		total += message.Tokens
	}
	return total
}

func (s *Service) ensureUser(ctx context.Context, msg *models.Message) (*user.User, error) {
	userID := msg.Chat.ID
	username := ""
	if msg.From != nil {
		userID = msg.From.ID
		username = msg.From.Username
	}

	if s.store == nil {
		return &user.User{
			TelegramUserID: userID,
			Username:       username,
			KeyStatus:      "missing",
			APIBaseURL:     s.cfg.OpenAIBaseURL,
			Mode:           s.cfg.DefaultMode,
			Status:         "active",
		}, nil
	}

	return s.store.GetOrCreate(ctx, userID, username, s.cfg.OpenAIBaseURL, s.cfg.DefaultMode)
}

func (s *Service) handleStatus(ctx context.Context, chatID int64, usr *user.User, sess *session.Session) {
	key := "未配置"
	if usr.HasKey() {
		plaintext, err := s.decryptKey(usr)
		if err != nil {
			key = "解密失败，请重新 /key"
		} else {
			key = secrets.Mask(plaintext)
		}
	}

	model := usr.Model
	if model == "" {
		model = s.cfg.DefaultModel
	}
	mode := usr.Mode
	if mode == "" {
		mode = s.cfg.DefaultMode
	}
	endpoint := endpointHost(usr.APIBaseURL)
	if endpoint == "" {
		endpoint = endpointHost(s.cfg.OpenAIBaseURL)
	}

	message := text.Status(
		s.cfg.InstanceName,
		version.Version,
		s.cfg.AppEnv,
		model,
		mode,
		key,
		endpoint,
		time.Since(s.started),
	)
	if sess != nil && sess.CurrentSymbol != "" {
		message += fmt.Sprintf("\n当前标的：<code>%s</code>", render.EscapeHTML(sess.CurrentSymbol))
	}
	if s.usage != nil && usr.ID != 0 {
		if summary, err := s.usage.Today(ctx, usr.ID); err == nil {
			message += fmt.Sprintf("\n今日请求：%d\n今日 tokens：%d",
				summary.Requests, summary.PromptTokens+summary.CompletionTokens)
		}
	}
	s.send(ctx, chatID, message)
}

func (s *Service) handleKeyInput(ctx context.Context, msg *models.Message, usr *user.User) {
	// 无论校验是否成功，先删除包含 Key 的消息。
	s.deleteMessage(ctx, msg.Chat.ID, msg.ID)

	rawKey := strings.TrimSpace(msg.Text)
	if rawKey == "" || strings.ContainsAny(rawKey, " \t\r\n") {
		s.send(ctx, msg.Chat.ID, text.KeyInvalid("Key 格式不正确"))
		return
	}
	if s.store == nil || s.cipher == nil || s.validator == nil {
		s.send(ctx, msg.Chat.ID, text.Unavailable("Key 配置"))
		return
	}

	baseURL := usr.APIBaseURL
	if baseURL == "" {
		baseURL = s.cfg.OpenAIBaseURL
	}
	if err := s.validator.ValidateKey(ctx, baseURL, rawKey); err != nil {
		s.logger.Warn("API Key 校验失败", "chat", logging.HashID(msg.Chat.ID), "error", err)
		s.send(ctx, msg.Chat.ID, text.KeyInvalid(reason(err)))
		return
	}

	ciphertext, nonce, err := s.cipher.Encrypt(rawKey, keyAAD(usr.TelegramUserID))
	if err != nil {
		s.logger.Error("加密用户 Key 失败", "chat", logging.HashID(msg.Chat.ID), "error", err)
		s.send(ctx, msg.Chat.ID, text.GenericError())
		return
	}
	if err := s.store.UpdateKey(ctx, usr.TelegramUserID, ciphertext, nonce, cipherVersion(s.cipher)); err != nil {
		s.logger.Error("保存用户 Key 失败", "chat", logging.HashID(msg.Chat.ID), "error", err)
		s.send(ctx, msg.Chat.ID, text.GenericError())
		return
	}

	s.send(ctx, msg.Chat.ID, text.KeySaved(secrets.Mask(rawKey), endpointHost(baseURL)))
}

func (s *Service) handleKeyDelete(ctx context.Context, chatID int64, usr *user.User) {
	if s.store == nil {
		s.send(ctx, chatID, text.Unavailable("Key 配置"))
		return
	}
	if err := s.store.DeleteKey(ctx, usr.TelegramUserID); err != nil {
		s.logger.Error("删除用户 Key 失败", "chat", logging.HashID(chatID), "error", err)
		s.send(ctx, chatID, text.GenericError())
		return
	}
	s.send(ctx, chatID, text.KeyDeleted())
}

func (s *Service) handleBaseURLCommand(ctx context.Context, chatID int64, args string, usr *user.User) {
	if !s.cfg.AllowCustomBaseURL {
		s.send(ctx, chatID, "自定义 base_url 已禁用。")
		return
	}
	switch strings.ToLower(strings.TrimSpace(args)) {
	case "":
		s.setPending(chatID, pendingBaseURL)
		s.send(ctx, chatID, text.BaseURLPrompt())
	case "reset":
		if s.store == nil {
			s.send(ctx, chatID, text.Unavailable("base_url 配置"))
			return
		}
		if err := s.store.UpdateBaseURL(ctx, usr.TelegramUserID, s.cfg.OpenAIBaseURL); err != nil {
			s.logger.Error("重置 base_url 失败", "chat", logging.HashID(chatID), "error", err)
			s.send(ctx, chatID, text.GenericError())
			return
		}
		s.send(ctx, chatID, text.BaseURLReset(endpointHost(s.cfg.OpenAIBaseURL)))
	default:
		s.processBaseURL(ctx, chatID, args, usr)
	}
}

func (s *Service) processBaseURL(ctx context.Context, chatID int64, raw string, usr *user.User) {
	if !s.cfg.AllowCustomBaseURL {
		s.send(ctx, chatID, "自定义 base_url 已禁用。")
		return
	}
	normalized, err := s.guard.Validate(ctx, raw)
	if err != nil {
		s.logger.Warn("base_url 校验失败", "chat", logging.HashID(chatID), "error", err)
		s.send(ctx, chatID, text.BaseURLInvalid(reason(err)))
		return
	}
	if s.store == nil {
		s.send(ctx, chatID, text.Unavailable("base_url 配置"))
		return
	}
	if err := s.store.UpdateBaseURL(ctx, usr.TelegramUserID, normalized); err != nil {
		s.logger.Error("保存 base_url 失败", "chat", logging.HashID(chatID), "error", err)
		s.send(ctx, chatID, text.GenericError())
		return
	}
	s.send(ctx, chatID, text.BaseURLSaved(endpointHost(normalized)))
}

func (s *Service) handleModelCommand(ctx context.Context, chatID int64, args string, usr *user.User) {
	model := strings.TrimSpace(args)
	if model != "" {
		if !modelNamePattern.MatchString(model) {
			s.send(ctx, chatID, text.ModelInvalid("模型名格式不正确"))
			return
		}
		if len(s.cfg.ModelAllowlist) > 0 && !contains(s.cfg.ModelAllowlist, model) {
			s.send(ctx, chatID, text.ModelInvalid("模型不在允许列表中"))
			return
		}

		if s.cfg.StrictModelList {
			if !usr.HasKey() {
				s.send(ctx, chatID, text.NeedKey())
				return
			}
			if s.cipher == nil || s.validator == nil {
				s.send(ctx, chatID, text.Unavailable("模型校验"))
				return
			}
			key, err := s.decryptKey(usr)
			if err != nil {
				s.send(ctx, chatID, text.KeyInvalid("无法解密已保存的 Key，请重新 /key"))
				return
			}
			models, listErr := s.validator.ListModels(ctx, usr.APIBaseURL, key)
			if listErr != nil {
				if errors.Is(listErr, llm.ErrInvalidKey) {
					s.send(ctx, chatID, text.KeyInvalid(reason(listErr)))
				} else {
					s.send(ctx, chatID, text.ModelInvalid("严格模式下必须能读取 endpoint 的 /models 列表"))
				}
				return
			}
			if len(models) == 0 {
				s.send(ctx, chatID, text.ModelInvalid("endpoint 没有返回模型列表"))
				return
			}
			if !contains(models, model) {
				s.send(ctx, chatID, text.ModelInvalid("endpoint 未返回该模型"))
				return
			}
		} else if usr.HasKey() && s.cipher != nil && s.validator != nil {
			// 非严格模式：能读到列表时校验；代理不支持 /models 时允许手动填写。
			key, err := s.decryptKey(usr)
			if err != nil {
				s.send(ctx, chatID, text.KeyInvalid("无法解密已保存的 Key，请重新 /key"))
				return
			}
			models, listErr := s.validator.ListModels(ctx, usr.APIBaseURL, key)
			if errors.Is(listErr, llm.ErrInvalidKey) {
				s.send(ctx, chatID, text.KeyInvalid(reason(listErr)))
				return
			}
			if listErr == nil && len(models) > 0 && !contains(models, model) {
				s.send(ctx, chatID, text.ModelInvalid("endpoint 未返回该模型"))
				return
			}
		}

		if s.store == nil {
			s.send(ctx, chatID, text.Unavailable("模型配置"))
			return
		}
		if err := s.store.UpdateModel(ctx, usr.TelegramUserID, model); err != nil {
			s.logger.Error("保存模型失败", "chat", logging.HashID(chatID), "error", err)
			s.send(ctx, chatID, text.GenericError())
			return
		}
		s.send(ctx, chatID, text.ModelSet(model))
		return
	}

	if !usr.HasKey() {
		s.send(ctx, chatID, text.NeedKey())
		return
	}
	if s.cipher == nil || s.validator == nil {
		s.send(ctx, chatID, text.Unavailable("模型列表"))
		return
	}

	key, err := s.decryptKey(usr)
	if err != nil {
		s.send(ctx, chatID, text.KeyInvalid("无法解密已保存的 Key，请重新 /key"))
		return
	}

	models, err := s.validator.ListModels(ctx, usr.APIBaseURL, key)
	if err != nil {
		if s.cfg.StrictModelList {
			s.send(ctx, chatID, text.ModelInvalid(reason(err)))
			return
		}
		if len(s.cfg.ModelAllowlist) > 0 {
			models = append([]string(nil), s.cfg.ModelAllowlist...)
		} else {
			s.send(ctx, chatID, text.ModelInvalid(reason(err)))
			return
		}
	}
	if len(models) == 0 {
		s.send(ctx, chatID, text.ModelInvalid("endpoint 没有返回可用模型"))
		return
	}
	if len(models) > 30 {
		models = models[:30]
	}

	current := usr.Model
	if current == "" {
		current = s.cfg.DefaultModel
	}
	s.send(ctx, chatID, text.ModelList(current, models))
}

func (s *Service) handleModeCommand(ctx context.Context, chatID int64, args string, usr *user.User) {
	mode := strings.ToLower(strings.TrimSpace(args))
	current := usr.Mode
	if current == "" {
		current = s.cfg.DefaultMode
	}

	switch mode {
	case "":
		s.send(ctx, chatID, text.ModeCurrent(current))
	case "short", "swing":
		if s.store == nil {
			s.send(ctx, chatID, text.Unavailable("模式配置"))
			return
		}
		if err := s.store.UpdateMode(ctx, usr.TelegramUserID, mode); err != nil {
			s.logger.Error("保存模式失败", "chat", logging.HashID(chatID), "error", err)
			s.send(ctx, chatID, text.GenericError())
			return
		}
		s.send(ctx, chatID, text.ModeSet(mode))
	default:
		s.send(ctx, chatID, text.ModeCurrent(current))
	}
}

func (s *Service) decryptKey(usr *user.User) (string, error) {
	if s.cipher == nil || !usr.HasKey() {
		return "", errors.New("没有可解密的 Key")
	}
	return s.cipher.Decrypt(usr.KeyCiphertext, usr.KeyNonce, keyAAD(usr.TelegramUserID))
}

func (s *Service) deleteMessage(ctx context.Context, chatID int64, messageID int) {
	if s.deleter == nil {
		return
	}
	if _, err := s.deleter.DeleteMessage(ctx, &bot.DeleteMessageParams{
		ChatID:    chatID,
		MessageID: messageID,
	}); err != nil {
		s.logger.Warn("删除 Telegram 消息失败，请用户手动删除",
			"chat", logging.HashID(chatID),
			"message_id", messageID,
			"error", err,
		)
	}
}

func (s *Service) setPending(chatID int64, kind pendingKind) {
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	if s.pending == nil {
		s.pending = make(map[int64]pendingState)
	}
	s.pending[chatID] = pendingState{
		kind:    kind,
		expires: time.Now().Add(5 * time.Minute),
	}
}

func (s *Service) takePending(chatID int64) (pendingKind, bool) {
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	state, ok := s.pending[chatID]
	if !ok {
		return 0, false
	}
	delete(s.pending, chatID)
	if time.Now().After(state.expires) {
		return 0, false
	}
	return state.kind, true
}

func (s *Service) send(ctx context.Context, chatID int64, message string) {
	if err := render.SendHTML(ctx, s.sender, chatID, message); err != nil {
		s.logger.Error("发送 Telegram 消息失败",
			"chat", logging.HashID(chatID),
			"error", err,
		)
		return
	}
	s.logger.Debug("Telegram 消息已发送",
		"chat", logging.HashID(chatID),
		"chars", len([]rune(message)),
	)
}

func keyAAD(userID int64) []byte {
	return []byte(fmt.Sprintf("user:%d", userID))
}

func cipherVersion(c secrets.Cipher) int {
	if withVersion, ok := c.(interface{ Version() int }); ok {
		return withVersion.Version()
	}
	return 1
}

func endpointHost(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err == nil && parsed.Host != "" {
		return parsed.Host
	}
	return raw
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func reason(err error) string {
	switch {
	case errors.Is(err, llm.ErrInvalidKey):
		return "Key 无效或没有权限"
	case errors.Is(err, llm.ErrModelsUnavailable):
		return "endpoint 不支持 /models，无法完成校验"
	case errors.Is(err, llm.ErrValidationUnavailable):
		return "暂时无法连接上游，请稍后再试"
	case errors.Is(err, market.ErrSymbolNotFound):
		return "找不到该交易对，或暂不支持（目前仅支持 Binance USDT 现货）"
	case errors.Is(err, market.ErrDataUnavailable):
		return "行情数据暂时不可用，请稍后再试"
	case errors.Is(err, agent.ErrNoModel):
		return "请先使用 /model 选择模型"
	case errors.Is(err, agent.ErrInvalidOutput):
		return "模型输出格式异常，请重试或换一个模型"
	case errors.Is(err, netguard.ErrInsecureScheme):
		return "必须使用 https"
	case errors.Is(err, netguard.ErrPrivateAddress):
		return "地址指向私网或保留地址"
	case errors.Is(err, netguard.ErrHostNotAllowed):
		return "域名不在白名单"
	case errors.Is(err, netguard.ErrPathRequired):
		return "地址必须包含路径，例如 /v1"
	case errors.Is(err, netguard.ErrUserInfo):
		return "地址不能包含用户名或密码"
	case errors.Is(err, netguard.ErrQueryOrFragment):
		return "地址不能包含 query 或 fragment"
	case errors.Is(err, netguard.ErrRedirect):
		return "不允许重定向"
	case errors.Is(err, context.DeadlineExceeded):
		return "上游响应超时，请稍后再试"
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return "上游响应超时，请稍后再试"
	}
	return "请检查地址和 Key 是否正确"
}

// parseCommand 解析 /cmd@BotName args 形式的文本。
func parseCommand(raw string) (command, args string) {
	if !strings.HasPrefix(raw, "/") {
		return "", raw
	}

	fields := strings.Fields(raw)
	if len(fields) == 0 {
		return "", ""
	}
	command = fields[0]
	if index := strings.Index(command, "@"); index >= 0 {
		command = command[:index]
	}
	command = strings.ToLower(command)
	args = strings.TrimSpace(strings.TrimPrefix(raw, fields[0]))
	return command, args
}

func looksLikeSymbol(value string) bool {
	if len(value) < 2 || len(value) > 24 {
		return false
	}
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '/' || r == '-':
		default:
			return false
		}
	}
	return true
}
