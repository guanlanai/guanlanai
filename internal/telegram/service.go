package telegram

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/guanlanai/guanlanai/internal/config"
	"github.com/guanlanai/guanlanai/internal/logging"
	"github.com/guanlanai/guanlanai/internal/render"
	"github.com/guanlanai/guanlanai/internal/text"
	"github.com/guanlanai/guanlanai/internal/version"
)

// Service 负责 Telegram 长轮询、命令路由和消息发送。
type Service struct {
	bot        *bot.Bot
	sender     render.Sender
	cfg        config.Config
	logger     *slog.Logger
	dispatcher *Dispatcher
	started    time.Time
}

// New 初始化 Telegram Bot。token 无效时会在启动阶段直接失败。
func New(cfg config.Config, logger *slog.Logger) (*Service, error) {
	if strings.TrimSpace(cfg.TelegramToken) == "" {
		return nil, errors.New("TELEGRAM_BOT_TOKEN 不能为空")
	}

	s := &Service{
		cfg:     cfg,
		logger:  logger,
		started: time.Now(),
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

	s.logger.Info("处理 Telegram 消息",
		"chat", logging.HashID(msg.Chat.ID),
		"command", command,
		"text_len", len([]rune(raw)),
	)

	switch command {
	case "/start":
		s.send(ctx, msg.Chat.ID, text.Welcome())
	case "/help":
		s.send(ctx, msg.Chat.ID, text.Help())
	case "/status":
		s.send(ctx, msg.Chat.ID, text.Status(
			s.cfg.InstanceName,
			version.Version,
			s.cfg.AppEnv,
			s.cfg.DefaultModel,
			s.cfg.DefaultMode,
			time.Since(s.started),
		))
	case "/reset":
		s.send(ctx, msg.Chat.ID, text.Reset())
	case "/analyze":
		if args == "" {
			s.send(ctx, msg.Chat.ID, text.NeedSymbol())
			return
		}
		s.send(ctx, msg.Chat.ID, text.Phase1(args))
	case "/key":
		s.send(ctx, msg.Chat.ID, "Key 配置将在 Phase 2 接入；现在不需要发送 API Key。")
	case "/baseurl":
		s.send(ctx, msg.Chat.ID, "自定义 OpenAI 兼容地址将在 Phase 2 接入。")
	case "/model":
		s.send(ctx, msg.Chat.ID, "模型切换将在 Phase 2 接入。")
	case "/mode":
		s.send(ctx, msg.Chat.ID, "模式切换将在 Phase 4 接入；当前默认模式为 "+s.cfg.DefaultMode+"。")
	case "":
		if looksLikeSymbol(raw) {
			s.send(ctx, msg.Chat.ID, text.Phase1(raw))
			return
		}
		s.send(ctx, msg.Chat.ID, text.Unsupported())
	default:
		s.send(ctx, msg.Chat.ID, text.UnknownCommand())
	}
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
