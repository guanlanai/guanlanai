package app

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/guanlanai/guanlanai/internal/config"
	"github.com/guanlanai/guanlanai/internal/health"
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

	var telegramErr chan error
	if cfg.TelegramToken != "" {
		tg, err := telegram.New(cfg, logger)
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
