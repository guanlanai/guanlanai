package app

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/guanlanai/guanlanai/internal/agent"
	"github.com/guanlanai/guanlanai/internal/config"
	"github.com/guanlanai/guanlanai/internal/health"
	"github.com/guanlanai/guanlanai/internal/llm/openai"
	"github.com/guanlanai/guanlanai/internal/market/binance"
	"github.com/guanlanai/guanlanai/internal/netguard"
	"github.com/guanlanai/guanlanai/internal/ratelimit"
	"github.com/guanlanai/guanlanai/internal/secrets"
	"github.com/guanlanai/guanlanai/internal/storage/sqlite"
	"github.com/guanlanai/guanlanai/internal/telegram"
	"github.com/guanlanai/guanlanai/internal/version"
	"github.com/guanlanai/guanlanai/migrations"
)

// Run 启动进程，直到 ctx 被取消后优雅退出。
func Run(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	db, err := sqlite.Open(cfg.DatabasePath)
	if err != nil {
		return fmt.Errorf("打开数据库: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			logger.Error("关闭数据库失败", "error", err)
		}
	}()

	if err := sqlite.Migrate(ctx, db, migrations.FS); err != nil {
		return fmt.Errorf("执行数据库迁移: %w", err)
	}

	store := sqlite.NewStore(db, sqlite.WithMaxPrivateSessions(cfg.MaxPrivateSessions))

	var keyCipher secrets.Cipher
	if cfg.MasterKey != "" {
		keyCipher, err = secrets.NewAESGCM(cfg.MasterKey, 1)
		if err != nil {
			return fmt.Errorf("初始化 Key 加密器: %w", err)
		}
	} else {
		logger.Warn("APP_MASTER_KEY 未配置；/key 功能不可用")
	}

	guard := netguard.Policy{
		AllowHTTP:    cfg.AllowInsecureBaseURL,
		AllowPrivate: cfg.AllowPrivateBaseURLForDev,
		AllowedHosts: cfg.CustomBaseURLAllowlist,
	}
	validator := openai.NewValidator(
		guard,
		cfg.CustomBaseURLTimeout,
		cfg.CustomBaseURLMaxBytes,
		cfg.DefaultModel,
		cfg.ModelAllowlist,
	)

	marketClient := binance.NewClient(cfg.BinanceBaseURL, cfg.BinanceFallbackBaseURL, 10*time.Second)
	completer := openai.NewCompleter(guard, 60*time.Second)
	analyzer := agent.New(
		marketClient,
		marketClient,
		completer,
		cfg.DefaultModel,
		cfg.MaxOutputTokens,
		cfg.LLMMaxConcurrency,
		logger,
	)
	limiter := ratelimit.New(cfg.AnalysisRatePerMin, cfg.AnalysisRatePerHour, cfg.ChatRatePerMin)
	go cleanupLoop(ctx, store, limiter, cfg.DataRetentionDays, logger)

	var telegramErr chan error
	if cfg.TelegramToken != "" {
		tg, err := telegram.New(cfg, logger, store, store, store, keyCipher, validator, guard, analyzer, limiter)
		if err != nil {
			return fmt.Errorf("初始化 Telegram 服务: %w", err)
		}
		telegramErr = make(chan error, 1)
		go func() {
			telegramErr <- tg.Start(ctx)
		}()
	} else {
		logger.Warn("TELEGRAM_BOT_TOKEN 未配置；仅启动健康检查")
	}

	healthServer := health.New(cfg.HTTPAddr, db, logger, cfg.InstanceName)
	healthServer.Start()

	logger.Info("服务已启动",
		"instance", cfg.InstanceName,
		"env", cfg.AppEnv,
		"version", version.Version,
		"commit", version.Commit,
		"http_addr", cfg.HTTPAddr,
		"database", cfg.DatabasePath,
		"telegram", cfg.TelegramToken != "",
	)

	var runErr error
	select {
	case <-ctx.Done():
	case err := <-telegramErr:
		if err != nil {
			runErr = fmt.Errorf("Telegram 服务异常: %w", err)
		}
		telegramErr = nil
	}
	logger.Info("收到退出信号，开始关闭")

	if telegramErr != nil {
		select {
		case err := <-telegramErr:
			if err != nil && runErr == nil {
				runErr = fmt.Errorf("Telegram 服务异常: %w", err)
			}
		case <-time.After(15 * time.Second):
			logger.Warn("Telegram 服务未在超时内退出")
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := healthServer.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("关闭健康检查服务: %w", err)
	}

	return runErr
}

func cleanupLoop(ctx context.Context, store *sqlite.Store, limiter *ratelimit.Limiter, days int, logger *slog.Logger) {
	if days <= 0 {
		days = 30
	}
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()

	cleanup := func() {
		before := time.Now().AddDate(0, 0, -days)
		count, err := store.CleanupMessages(ctx, before)
		if err != nil {
			logger.Warn("清理过期消息失败", "error", err)
		} else if count > 0 {
			logger.Info("已清理过期消息", "count", count)
		}
		limiter.CleanupIdle(time.Now(), 24*time.Hour)
	}
	cleanup()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cleanup()
		}
	}
}
