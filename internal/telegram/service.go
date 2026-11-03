package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/guanlanai/guanlanai/internal/agent"
	"github.com/guanlanai/guanlanai/internal/analysis"
	"github.com/guanlanai/guanlanai/internal/config"
	"github.com/guanlanai/guanlanai/internal/llm"
	"github.com/guanlanai/guanlanai/internal/logging"
	"github.com/guanlanai/guanlanai/internal/market"
	"github.com/guanlanai/guanlanai/internal/netguard"
	"github.com/guanlanai/guanlanai/internal/ratelimit"
	"github.com/guanlanai/guanlanai/internal/render"
	"github.com/guanlanai/guanlanai/internal/resilience"
	"github.com/guanlanai/guanlanai/internal/risk"
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
	Analyze(ctx context.Context, usr *user.User, apiKey, query, mode string, strategy analysis.Strategy, timeframe market.Timeframe, indicators []analysis.Indicator, sessionID int64) (*agent.Result, error)
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

	botUsername string
	botID       int64
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
	s.sender = NewRateLimitedSender(b, time.Second, 25, 2)
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
	s.botUsername = me.Username
	s.botID = me.ID
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
	switch msg.Chat.Type {
	case models.ChatTypePrivate:
		if strings.TrimSpace(msg.Text) == "" {
			s.send(ctx, msg.Chat.ID, text.TextOnly())
			return
		}
	case models.ChatTypeGroup, models.ChatTypeSupergroup:
		if !s.cfg.GroupsEnabled {
			return
		}
		if msg.From == nil {
			return
		}
		if strings.TrimSpace(msg.Text) == "" {
			return
		}
		// 群组只处理提及、回复 Bot 或明确的命令，其余消息不回复也不落库。
		if !s.groupAddressed(msg) {
			return
		}
	default:
		// 频道等类型直接忽略。
		return
	}

	s.logger.Info("收到 Telegram 消息",
		"update_id", update.ID,
		"chat", logging.HashID(msg.Chat.ID),
		"chat_type", msg.Chat.Type,
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
	scope := scopeForChat(msg.Chat.Type)

	usr, err := s.ensureUser(ctx, msg)
	if err != nil {
		s.logger.Error("读取用户配置失败", "chat", logging.HashID(msg.Chat.ID), "error", err)
		s.send(ctx, msg.Chat.ID, text.GenericError())
		return
	}

	// 待输入状态只存在于私聊；命令会取消待输入并继续按命令处理。
	if scope == session.ScopePrivate {
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
	}

	s.logger.Info("处理 Telegram 消息",
		"chat", logging.HashID(msg.Chat.ID),
		"chat_type", msg.Chat.Type,
		"command", command,
		"text_len", len([]rune(raw)),
	)

	// 群组只能配置 Key 和管理私聊会话；统一引导回私聊。
	if scope == session.ScopeGroup && privateOnlyCommand(command) {
		s.send(ctx, msg.Chat.ID, text.PrivateConfigHint())
		return
	}

	sess, err := s.ensureSession(ctx, usr, msg.Chat.ID, scope)
	if err != nil {
		s.logger.Warn("读取会话失败，继续无会话处理", "chat", logging.HashID(msg.Chat.ID), "error", err)
	}

	switch command {
	case "/start":
		if scope == session.ScopeGroup {
			s.send(ctx, msg.Chat.ID, text.GroupHelp(s.botUsername))
			return
		}
		message := text.Welcome()
		if !usr.HasKey() {
			message += "\n\n" + text.NeedKey()
		}
		s.send(ctx, msg.Chat.ID, message)
	case "/help":
		if scope == session.ScopeGroup {
			s.send(ctx, msg.Chat.ID, text.GroupHelp(s.botUsername))
			return
		}
		s.send(ctx, msg.Chat.ID, text.Help())
	case "/status":
		s.handleStatus(ctx, msg.Chat.ID, usr, sess, scope)
	case "/reset":
		s.handleReset(ctx, msg.Chat.ID, sess)
	case "/cancel":
		s.send(ctx, msg.Chat.ID, "已取消待处理操作。")
	case "/new":
		s.handleNewSession(ctx, msg.Chat.ID, usr, args)
	case "/sessions":
		s.handleSessionList(ctx, msg.Chat.ID, usr, sess)
	case "/switch":
		s.handleSessionSwitch(ctx, msg.Chat.ID, usr, args)
	case "/rename":
		s.handleSessionRename(ctx, msg.Chat.ID, usr, args)
	case "/delete":
		s.handleSessionDelete(ctx, msg.Chat.ID, usr, args)
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
	case "/timeframe":
		s.handleTimeframeCommand(ctx, msg.Chat.ID, args, usr)
	case "/indicators":
		s.handleIndicatorsCommand(ctx, msg.Chat.ID, args, usr)
	case "/risk":
		s.handleRiskCommand(ctx, msg.Chat.ID, args, usr)
	case "/strategy":
		s.handleStrategyCommand(ctx, msg.Chat.ID, args, usr)
	case "/analyze":
		if args == "" {
			s.send(ctx, msg.Chat.ID, text.NeedSymbol())
			return
		}
		s.handleAnalyze(ctx, msg.Chat.ID, usr, sess, args, scope)
	case "":
		query := raw
		if scope == session.ScopeGroup {
			query = s.stripBotMention(raw)
		}
		if strings.TrimSpace(query) == "" {
			return
		}
		if looksLikeSymbol(query) && !s.isFollowUp(query, sess) {
			s.handleAnalyze(ctx, msg.Chat.ID, usr, sess, query, scope)
			return
		}
		s.handleFollowUp(ctx, msg.Chat.ID, usr, sess, query, scope)
	default:
		s.send(ctx, msg.Chat.ID, text.UnknownCommand())
	}
}

func (s *Service) handleAnalyze(ctx context.Context, chatID int64, usr *user.User, sess *session.Session, query string, scope session.Scope) {
	if !usr.HasKey() {
		s.send(ctx, chatID, s.needKeyMessage(scope))
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
		sess, err = s.ensureSession(ctx, usr, chatID, scope)
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
	// 在发起分析时捕获策略、周期和指标，避免用户在途修改偏好影响本次结果。
	strategy, _ := s.effectiveStrategy(usr)
	timeframe, _ := s.effectiveTimeframe(usr)
	indicators, _ := s.effectiveIndicators(usr)
	timeframeLabel := analysis.TimeframeLabel(strategy, timeframe)
	sessionID := int64(0)
	if sess != nil {
		sessionID = sess.ID
	}

	started := time.Now()
	if sess != nil {
		_ = s.sessions.AppendMessage(ctx, sess.ID, "user",
			"分析 "+query+"（"+timeframeLabel+" · "+mode+"）", estimateTokens(query))
	}
	s.send(ctx, chatID, text.Analyzing(query, timeframeLabel, mode))
	result, err := s.analyzer.Analyze(ctx, usr, key, query, mode, strategy, timeframe, indicators, sessionID)
	if err != nil {
		s.recordUsage(ctx, usr, "analysis", model, nil, time.Since(started), "error")
		s.logger.Error("分析失败",
			"chat", logging.HashID(chatID),
			"query", query,
			"mode", mode,
			"strategy", string(strategy),
			"timeframe", timeframeLabel,
			"error", err,
		)
		s.send(ctx, chatID, text.AnalysisError(reason(err)))
		return
	}

	position := s.positionFor(scope, usr, result)
	card := render.OpinionCard(result.Report, result.Opinion, position)
	if sess != nil {
		payload, marshalErr := json.Marshal(map[string]any{
			"report":   result.Report,
			"opinion":  result.Opinion,
			"position": position,
		})
		if marshalErr == nil {
			if _, saveErr := s.sessions.SaveAnalysis(ctx, sess.ID, result.Report.Symbol, result.Report.Market, timeframeLabel, string(payload)); saveErr != nil {
				s.logger.Warn("保存分析上下文失败", "chat", logging.HashID(chatID), "error", saveErr)
			}
		}
		_ = s.sessions.AppendMessage(ctx, sess.ID, "assistant", card, estimateTokens(card))
	}
	s.recordUsage(ctx, usr, "analysis", model, result.Usage, time.Since(started), "ok")
	s.send(ctx, chatID, card)
}

func (s *Service) handleFollowUp(ctx context.Context, chatID int64, usr *user.User, sess *session.Session, question string, scope session.Scope) {
	if sess == nil || sess.LastAnalysisJSON == "" {
		s.send(ctx, chatID, text.FollowUpNeedAnalysis())
		return
	}
	if !usr.HasKey() {
		s.send(ctx, chatID, s.needKeyMessage(scope))
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

func (s *Service) handleReset(ctx context.Context, chatID int64, sess *session.Session) {
	if s.sessions != nil && sess != nil {
		if _, err := s.sessions.Reset(ctx, sess.ID); err != nil && !errors.Is(err, session.ErrNotFound) {
			s.logger.Warn("重置会话失败", "chat", logging.HashID(chatID), "error", err)
		}
	}
	s.send(ctx, chatID, text.Reset())
}

func (s *Service) ensureSession(ctx context.Context, usr *user.User, chatID int64, scope session.Scope) (*session.Session, error) {
	if s.sessions == nil {
		return nil, nil
	}
	if usr == nil || usr.ID == 0 {
		return nil, errors.New("用户尚未持久化")
	}
	if scope == session.ScopeGroup {
		return s.sessions.GetOrCreateGroup(ctx, usr.ID, chatID)
	}
	return s.sessions.GetOrCreateActivePrivate(ctx, usr.ID, chatID)
}

func (s *Service) handleNewSession(ctx context.Context, chatID int64, usr *user.User, args string) {
	if s.sessions == nil || usr == nil || usr.ID == 0 {
		s.send(ctx, chatID, text.Unavailable("会话管理"))
		return
	}
	title, ok := normalizeTitleInput(args)
	if !ok {
		s.send(ctx, chatID, text.SessionTitleTooLong(session.MaxTitleLength))
		return
	}
	sess, err := s.sessions.CreatePrivate(ctx, usr.ID, chatID, title)
	if err != nil {
		if errors.Is(err, session.ErrLimitReached) {
			s.send(ctx, chatID, text.SessionLimitReached(s.maxPrivateSessions()))
			return
		}
		s.logger.Error("创建会话失败", "chat", logging.HashID(chatID), "error", err)
		s.send(ctx, chatID, text.GenericError())
		return
	}
	s.send(ctx, chatID, text.SessionCreated(sess.ID, sess.Title))
}

func (s *Service) handleSessionList(ctx context.Context, chatID int64, usr *user.User, active *session.Session) {
	if s.sessions == nil || usr == nil || usr.ID == 0 {
		s.send(ctx, chatID, text.Unavailable("会话管理"))
		return
	}
	list, err := s.sessions.ListPrivate(ctx, usr.ID, chatID)
	if err != nil {
		s.logger.Error("读取会话列表失败", "chat", logging.HashID(chatID), "error", err)
		s.send(ctx, chatID, text.GenericError())
		return
	}
	items := make([]text.SessionItem, 0, len(list))
	for _, sess := range list {
		items = append(items, text.SessionItem{ID: sess.ID, Title: sess.Title})
		if len(items) >= s.maxPrivateSessions() {
			break
		}
	}
	activeID := int64(0)
	if active != nil {
		activeID = active.ID
	}
	s.send(ctx, chatID, text.SessionList(items, activeID, len(list)))
}

func (s *Service) handleSessionSwitch(ctx context.Context, chatID int64, usr *user.User, args string) {
	sessionID, ok := parseSessionID(args)
	if !ok {
		s.send(ctx, chatID, text.SessionUsage("/switch"))
		return
	}
	if s.sessions == nil || usr == nil || usr.ID == 0 {
		s.send(ctx, chatID, text.Unavailable("会话管理"))
		return
	}
	if err := s.sessions.SetActivePrivate(ctx, usr.ID, chatID, sessionID); err != nil {
		if errors.Is(err, session.ErrNotFound) {
			s.send(ctx, chatID, text.SessionNotFound())
			return
		}
		s.logger.Error("切换会话失败", "chat", logging.HashID(chatID), "error", err)
		s.send(ctx, chatID, text.GenericError())
		return
	}
	sess, err := s.sessions.GetPrivate(ctx, usr.ID, chatID, sessionID)
	if err != nil {
		s.send(ctx, chatID, text.SessionNotFound())
		return
	}
	s.send(ctx, chatID, text.SessionSwitched(sess.ID, sess.Title))
}

func (s *Service) handleSessionRename(ctx context.Context, chatID int64, usr *user.User, args string) {
	trimmed := strings.TrimSpace(args)
	index := strings.IndexFunc(trimmed, unicode.IsSpace)
	if index <= 0 {
		s.send(ctx, chatID, text.SessionUsage("/rename"))
		return
	}
	sessionID, ok := parseSessionID(trimmed[:index])
	if !ok {
		s.send(ctx, chatID, text.SessionUsage("/rename"))
		return
	}
	title, titleOK := normalizeTitleInput(trimmed[index+1:])
	if !titleOK || title == "" {
		if !titleOK {
			s.send(ctx, chatID, text.SessionTitleTooLong(session.MaxTitleLength))
			return
		}
		s.send(ctx, chatID, text.SessionUsage("/rename"))
		return
	}
	if s.sessions == nil || usr == nil || usr.ID == 0 {
		s.send(ctx, chatID, text.Unavailable("会话管理"))
		return
	}
	if err := s.sessions.RenamePrivate(ctx, usr.ID, chatID, sessionID, title); err != nil {
		if errors.Is(err, session.ErrNotFound) {
			s.send(ctx, chatID, text.SessionNotFound())
			return
		}
		if errors.Is(err, session.ErrTitleRequired) {
			s.send(ctx, chatID, text.SessionUsage("/rename"))
			return
		}
		s.logger.Error("重命名会话失败", "chat", logging.HashID(chatID), "error", err)
		s.send(ctx, chatID, text.GenericError())
		return
	}
	s.send(ctx, chatID, text.SessionRenamed(sessionID, title))
}

func (s *Service) handleSessionDelete(ctx context.Context, chatID int64, usr *user.User, args string) {
	fields := strings.Fields(args)
	sessionID, ok := int64(0), false
	if len(fields) > 0 {
		sessionID, ok = parseSessionID(fields[0])
	}
	if !ok {
		s.send(ctx, chatID, text.SessionUsage("/delete"))
		return
	}
	if s.sessions == nil || usr == nil || usr.ID == 0 {
		s.send(ctx, chatID, text.Unavailable("会话管理"))
		return
	}
	confirmed := len(fields) >= 2 && strings.EqualFold(fields[1], "confirm")
	if !confirmed {
		sess, err := s.sessions.GetPrivate(ctx, usr.ID, chatID, sessionID)
		if err != nil {
			if errors.Is(err, session.ErrNotFound) {
				s.send(ctx, chatID, text.SessionNotFound())
				return
			}
			s.logger.Error("读取待删除会话失败", "chat", logging.HashID(chatID), "error", err)
			s.send(ctx, chatID, text.GenericError())
			return
		}
		s.send(ctx, chatID, text.SessionDeleteConfirm(sess.ID, sess.Title))
		return
	}
	if err := s.sessions.DeletePrivate(ctx, usr.ID, chatID, sessionID); err != nil {
		switch {
		case errors.Is(err, session.ErrLastSession):
			s.send(ctx, chatID, text.SessionLastOne())
		case errors.Is(err, session.ErrNotFound):
			s.send(ctx, chatID, text.SessionNotFound())
		default:
			s.logger.Error("删除会话失败", "chat", logging.HashID(chatID), "error", err)
			s.send(ctx, chatID, text.GenericError())
		}
		return
	}
	s.send(ctx, chatID, text.SessionDeleted(sessionID))
}

func parseSessionID(raw string) (int64, bool) {
	value := strings.TrimSpace(raw)
	if value == "" || strings.ContainsAny(value, " \t\r\n") {
		return 0, false
	}
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

func normalizeTitleInput(raw string) (string, bool) {
	title := strings.Join(strings.Fields(raw), " ")
	if len([]rune(title)) > session.MaxTitleLength {
		return "", false
	}
	return title, true
}

func (s *Service) needKeyMessage(scope session.Scope) string {
	if scope == session.ScopeGroup {
		return text.NeedKeyInGroup()
	}
	return text.NeedKey()
}

func (s *Service) maxPrivateSessions() int {
	if s.cfg.MaxPrivateSessions > 0 {
		return s.cfg.MaxPrivateSessions
	}
	return session.MaxPrivateSessions
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

func (s *Service) handleStatus(ctx context.Context, chatID int64, usr *user.User, sess *session.Session, scope session.Scope) {
	if scope == session.ScopeGroup {
		s.handleGroupStatus(ctx, chatID, usr, sess)
		return
	}

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
	strategy, _ := s.effectiveStrategy(usr)
	timeframe, timeframeDefault := s.effectiveTimeframe(usr)
	if strategy == analysis.StrategyTripleScreen {
		timeframeDefault = false
	}
	timeframeLabel := analysis.TimeframeLabel(strategy, timeframe)

	message := text.Status(
		s.cfg.InstanceName,
		version.Version,
		s.cfg.AppEnv,
		model,
		mode,
		analysis.StrategyLabel(strategy),
		timeframeLabel,
		timeframeDefault,
		key,
		endpoint,
		time.Since(s.started),
	)
	if usr.RiskPerTrade > 0 {
		message += fmt.Sprintf("\n每笔风险：<code>%s USDT</code>", strconv.FormatFloat(usr.RiskPerTrade, 'f', -1, 64))
	} else {
		message += "\n每笔风险：未设置"
	}
	if sess != nil {
		message += fmt.Sprintf("\n当前会话：#%d <b>%s</b>", sess.ID, render.EscapeHTML(sess.Title))
		if sess.CurrentSymbol != "" {
			message += fmt.Sprintf("\n当前标的：<code>%s</code>", render.EscapeHTML(sess.CurrentSymbol))
		}
		if sess.CurrentTimeframe != "" && sess.CurrentTimeframe != timeframe.String() {
			message += fmt.Sprintf("\n最近分析周期：<code>%s</code>", render.EscapeHTML(sess.CurrentTimeframe))
		}
	}
	if s.usage != nil && usr.ID != 0 {
		if summary, err := s.usage.Today(ctx, usr.ID); err == nil {
			message += fmt.Sprintf("\n今日请求：%d\n今日 tokens：%d",
				summary.Requests, summary.PromptTokens+summary.CompletionTokens)
		}
	}
	s.send(ctx, chatID, message)
}

func (s *Service) handleGroupStatus(ctx context.Context, chatID int64, usr *user.User, sess *session.Session) {
	model := usr.Model
	if model == "" {
		model = s.cfg.DefaultModel
	}
	mode := usr.Mode
	if mode == "" {
		mode = s.cfg.DefaultMode
	}
	strategy, _ := s.effectiveStrategy(usr)
	timeframe, timeframeDefault := s.effectiveTimeframe(usr)
	if strategy == analysis.StrategyTripleScreen {
		timeframeDefault = false
	}
	message := text.GroupStatus(
		s.cfg.InstanceName,
		model,
		mode,
		analysis.StrategyLabel(strategy),
		analysis.TimeframeLabel(strategy, timeframe),
		timeframeDefault,
	)
	if sess != nil {
		message += fmt.Sprintf("\n当前会话：<b>%s</b>", render.EscapeHTML(sess.Title))
		if sess.CurrentSymbol != "" {
			message += fmt.Sprintf("\n当前标的：<code>%s</code>", render.EscapeHTML(sess.CurrentSymbol))
		}
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

func (s *Service) handleTimeframeCommand(ctx context.Context, chatID int64, args string, usr *user.User) {
	current, isDefault := s.effectiveTimeframe(usr)
	value := strings.TrimSpace(args)
	if value == "" {
		s.send(ctx, chatID, text.TimeframeCurrent(current.String(), isDefault))
		return
	}
	if strings.EqualFold(value, "reset") {
		if s.store == nil {
			s.send(ctx, chatID, text.Unavailable("周期配置"))
			return
		}
		if err := s.store.UpdateTimeframe(ctx, usr.TelegramUserID, ""); err != nil {
			s.logger.Error("重置常用周期失败", "chat", logging.HashID(chatID), "error", err)
			s.send(ctx, chatID, text.GenericError())
			return
		}
		usr.PreferredTimeframe = ""
		defaultTimeframe := market.DefaultTimeframe
		if s.cfg.DefaultTimeframe.Valid() {
			defaultTimeframe = s.cfg.DefaultTimeframe
		}
		s.send(ctx, chatID, text.TimeframeReset(defaultTimeframe.String()))
		return
	}

	timeframe, err := market.ParseTimeframe(value)
	if err != nil {
		s.send(ctx, chatID, text.TimeframeInvalid(market.TimeframesLabel()))
		return
	}
	if s.store == nil {
		s.send(ctx, chatID, text.Unavailable("周期配置"))
		return
	}
	if err := s.store.UpdateTimeframe(ctx, usr.TelegramUserID, timeframe.String()); err != nil {
		s.logger.Error("保存常用周期失败", "chat", logging.HashID(chatID), "error", err)
		s.send(ctx, chatID, text.GenericError())
		return
	}
	usr.PreferredTimeframe = timeframe.String()
	s.send(ctx, chatID, text.TimeframeSet(timeframe.String()))
}

// positionFor 只在私聊且用户设置了风险金额时计算仓位；群组不展示、不保存风险金额。
func (s *Service) positionFor(scope session.Scope, usr *user.User, result *agent.Result) *risk.Position {
	if scope != session.ScopePrivate || usr == nil || usr.RiskPerTrade <= 0 || result == nil || result.Opinion == nil {
		return nil
	}
	plan := result.Opinion.TradePlan
	if plan == nil {
		return nil
	}
	position, err := risk.Calculate(
		result.Opinion.Direction,
		plan.EntryLow,
		plan.EntryHigh,
		plan.StopLoss,
		usr.RiskPerTrade,
	)
	if err != nil {
		s.logger.Warn("仓位计算失败", "direction", result.Opinion.Direction, "error", err)
		return nil
	}
	return position
}

func (s *Service) handleStrategyCommand(ctx context.Context, chatID int64, args string, usr *user.User) {
	current, isDefault := s.effectiveStrategy(usr)
	value := strings.TrimSpace(args)
	if value == "" {
		s.send(ctx, chatID, text.StrategyCurrent(analysis.StrategyLabel(current), tripleScreenLabel(), isDefault))
		return
	}
	if strings.EqualFold(value, "reset") {
		if s.store == nil {
			s.send(ctx, chatID, text.Unavailable("策略配置"))
			return
		}
		if err := s.store.UpdateStrategy(ctx, usr.TelegramUserID, ""); err != nil {
			s.logger.Error("重置分析策略失败", "chat", logging.HashID(chatID), "error", err)
			s.send(ctx, chatID, text.GenericError())
			return
		}
		usr.PreferredStrategy = ""
		s.send(ctx, chatID, text.StrategyReset(analysis.StrategyLabel(analysis.StrategySingle)))
		return
	}

	strategy, err := analysis.ParseStrategy(value)
	if err != nil {
		s.send(ctx, chatID, text.StrategyInvalid(tripleScreenLabel()))
		return
	}
	stored := string(strategy)
	if strategy == analysis.StrategySingle {
		stored = ""
	}
	if s.store == nil {
		s.send(ctx, chatID, text.Unavailable("策略配置"))
		return
	}
	if err := s.store.UpdateStrategy(ctx, usr.TelegramUserID, stored); err != nil {
		s.logger.Error("保存分析策略失败", "chat", logging.HashID(chatID), "error", err)
		s.send(ctx, chatID, text.GenericError())
		return
	}
	usr.PreferredStrategy = stored
	s.send(ctx, chatID, text.StrategySet(strategy == analysis.StrategyTripleScreen, tripleScreenLabel()))
}

func tripleScreenLabel() string {
	parts := make([]string, 0, len(analysis.TripleScreenTemplate))
	for _, spec := range analysis.TripleScreenTemplate {
		parts = append(parts, spec.Timeframe.String())
	}
	return strings.Join(parts, "/")
}

// effectiveStrategy 返回用户偏好或默认单周期，并标明是否使用默认值。
func (s *Service) effectiveStrategy(usr *user.User) (analysis.Strategy, bool) {
	if usr != nil && strings.TrimSpace(usr.PreferredStrategy) != "" {
		if strategy, err := analysis.ParseStrategy(usr.PreferredStrategy); err == nil {
			return strategy, false
		}
	}
	return analysis.StrategySingle, true
}

func (s *Service) handleRiskCommand(ctx context.Context, chatID int64, args string, usr *user.User) {
	value := strings.TrimSpace(args)
	if value == "" {
		if usr.RiskPerTrade > 0 {
			s.send(ctx, chatID, text.RiskCurrent(usr.RiskPerTrade))
		} else {
			s.send(ctx, chatID, text.RiskNotSet())
		}
		return
	}
	if strings.EqualFold(value, "reset") {
		if s.store == nil {
			s.send(ctx, chatID, text.Unavailable("风险配置"))
			return
		}
		if err := s.store.UpdateRiskPerTrade(ctx, usr.TelegramUserID, 0); err != nil {
			s.logger.Error("清除每笔最大亏损失败", "chat", logging.HashID(chatID), "error", err)
			s.send(ctx, chatID, text.GenericError())
			return
		}
		usr.RiskPerTrade = 0
		s.send(ctx, chatID, text.RiskReset())
		return
	}

	amount, ok := parseRiskAmount(value)
	if !ok {
		s.send(ctx, chatID, text.RiskInvalid())
		return
	}
	if s.store == nil {
		s.send(ctx, chatID, text.Unavailable("风险配置"))
		return
	}
	if err := s.store.UpdateRiskPerTrade(ctx, usr.TelegramUserID, amount); err != nil {
		s.logger.Error("保存每笔最大亏损失败", "chat", logging.HashID(chatID), "error", err)
		s.send(ctx, chatID, text.GenericError())
		return
	}
	usr.RiskPerTrade = amount
	s.send(ctx, chatID, text.RiskSet(amount))
}

func parseRiskAmount(raw string) (float64, bool) {
	value := strings.ToLower(strings.TrimSpace(raw))
	value = strings.TrimPrefix(value, "$")
	value = strings.TrimSpace(strings.TrimSuffix(value, "usdt"))
	value = strings.ReplaceAll(value, ",", "")
	amount, err := strconv.ParseFloat(value, 64)
	if err != nil || amount <= 0 || math.IsNaN(amount) || math.IsInf(amount, 0) || amount > 1e12 {
		return 0, false
	}
	return amount, true
}

func (s *Service) handleIndicatorsCommand(ctx context.Context, chatID int64, args string, usr *user.User) {
	current, isDefault := s.effectiveIndicators(usr)
	value := strings.TrimSpace(args)
	if value == "" {
		s.send(ctx, chatID, text.IndicatorsCurrent(analysis.JoinIndicators(current), isDefault))
		return
	}
	if strings.EqualFold(value, "reset") {
		if s.store == nil {
			s.send(ctx, chatID, text.Unavailable("指标配置"))
			return
		}
		if err := s.store.UpdateIndicators(ctx, usr.TelegramUserID, ""); err != nil {
			s.logger.Error("重置常用指标失败", "chat", logging.HashID(chatID), "error", err)
			s.send(ctx, chatID, text.GenericError())
			return
		}
		usr.PreferredIndicators = ""
		s.send(ctx, chatID, text.IndicatorsReset(analysis.IndicatorsLabel()))
		return
	}

	indicators, err := analysis.ParseIndicators(value)
	if err != nil {
		s.send(ctx, chatID, text.IndicatorsInvalid(err.Error(), analysis.IndicatorsLabel()))
		return
	}
	if s.store == nil {
		s.send(ctx, chatID, text.Unavailable("指标配置"))
		return
	}
	normalized := analysis.JoinIndicators(indicators)
	if err := s.store.UpdateIndicators(ctx, usr.TelegramUserID, normalized); err != nil {
		s.logger.Error("保存常用指标失败", "chat", logging.HashID(chatID), "error", err)
		s.send(ctx, chatID, text.GenericError())
		return
	}
	usr.PreferredIndicators = normalized
	s.send(ctx, chatID, text.IndicatorsSet(normalized))
}

// effectiveIndicators 返回用户偏好或默认全部指标，并标明是否使用默认值。
func (s *Service) effectiveIndicators(usr *user.User) ([]analysis.Indicator, bool) {
	if usr != nil && strings.TrimSpace(usr.PreferredIndicators) != "" {
		if indicators, err := analysis.ParseIndicators(usr.PreferredIndicators); err == nil {
			return indicators, false
		}
	}
	return analysis.DefaultIndicators(), true
}

// effectiveTimeframe 返回用户偏好或系统默认周期，并标明是否使用默认值。
func (s *Service) effectiveTimeframe(usr *user.User) (market.Timeframe, bool) {
	if usr != nil {
		if timeframe, err := market.ParseTimeframe(usr.PreferredTimeframe); err == nil {
			return timeframe, false
		}
	}
	if s.cfg.DefaultTimeframe.Valid() {
		return s.cfg.DefaultTimeframe, true
	}
	return market.DefaultTimeframe, true
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
		return "找不到该交易对，或暂不支持（目前仅支持 Binance USDT 现货；股票暂不支持）"
	case errors.Is(err, market.ErrDataUnavailable):
		return "行情数据暂时不可用，请稍后再试"
	case errors.Is(err, agent.ErrNoModel):
		return "请先使用 /model 选择模型"
	case errors.Is(err, agent.ErrInvalidOutput):
		return "模型输出格式异常，请重试或换一个模型"
	case errors.Is(err, llm.ErrEmptyResponse):
		return "模型没有返回有效内容，请稍后重试；如果持续出现，请换一个模型"
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
	case errors.Is(err, resilience.ErrOpen):
		return "上游服务暂时熔断，请稍后再试"
	case errors.Is(err, context.DeadlineExceeded):
		return "上游响应超时，请稍后再试"
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return "上游响应超时，请稍后再试"
	}
	return "请检查地址和 Key 是否正确"
}

func scopeForChat(chatType models.ChatType) session.Scope {
	if chatType == models.ChatTypeGroup || chatType == models.ChatTypeSupergroup {
		return session.ScopeGroup
	}
	return session.ScopePrivate
}

func privateOnlyCommand(command string) bool {
	switch command {
	case "/key", "/baseurl", "/model", "/mode", "/timeframe", "/indicators", "/risk", "/strategy", "/new", "/sessions", "/switch", "/rename", "/delete":
		return true
	default:
		return false
	}
}

// groupAddressed 判断群消息是否明确指向本 Bot。
// 命令（Telegram 只会把发给 Bot 的命令投递过来）以及 @提及、回复 Bot 都算。
func (s *Service) groupAddressed(msg *models.Message) bool {
	if msg.ReplyToMessage != nil && msg.ReplyToMessage.From != nil {
		from := msg.ReplyToMessage.From
		if s.botID != 0 && from.ID == s.botID {
			return true
		}
		if s.botUsername != "" && strings.EqualFold(from.Username, s.botUsername) {
			return true
		}
	}

	textValue := strings.TrimSpace(msg.Text)
	fields := strings.Fields(textValue)
	if len(fields) == 0 {
		return false
	}
	first := fields[0]
	if strings.HasPrefix(first, "/") {
		if index := strings.Index(first, "@"); index >= 0 {
			target := strings.Trim(first[index+1:], ",，:：")
			return s.botUsername != "" && strings.EqualFold(target, s.botUsername)
		}
		return true
	}
	return s.hasBotMention(textValue)
}

func (s *Service) hasBotMention(value string) bool {
	if s.botUsername == "" {
		return false
	}
	for _, field := range strings.Fields(value) {
		if s.isBotMentionToken(field) {
			return true
		}
	}
	return false
}

func (s *Service) isBotMentionToken(token string) bool {
	if s.botUsername == "" {
		return false
	}
	token = strings.Trim(token, " \t\r\n,，.。:：;；!！?？、")
	token = strings.TrimPrefix(token, "@")
	return strings.EqualFold(token, s.botUsername)
}

func (s *Service) stripBotMention(value string) string {
	fields := strings.Fields(value)
	kept := make([]string, 0, len(fields))
	for _, field := range fields {
		if s.isBotMentionToken(field) {
			continue
		}
		kept = append(kept, field)
	}
	return strings.Join(kept, " ")
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
