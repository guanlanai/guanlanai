package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/guanlanai/guanlanai/internal/app"
	"github.com/guanlanai/guanlanai/internal/backup"
	"github.com/guanlanai/guanlanai/internal/config"
	"github.com/guanlanai/guanlanai/internal/logging"
	"github.com/guanlanai/guanlanai/internal/version"
)

func main() {
	// 数据库、WAL/SHM 和备份文件默认只允许当前用户读写。
	syscall.Umask(0o077)

	if len(os.Args) < 2 {
		runServe()
		return
	}

	switch os.Args[1] {
	case "serve":
		runServe()
	case "backup":
		runBackup(os.Args[2:])
	case "genkey":
		runGenKey()
	case "version":
		fmt.Printf("version=%s commit=%s\n", version.Version, version.Commit)
	case "help", "-h", "--help":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "未知命令: %s\n\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func runServe() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "配置错误:", err)
		os.Exit(1)
	}

	logger, err := logging.New(cfg.LogLevel)
	if err != nil {
		fmt.Fprintln(os.Stderr, "日志配置错误:", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := app.Run(ctx, cfg, logger); err != nil {
		logger.Error("进程退出", "error", err)
		os.Exit(1)
	}
}

func runBackup(args []string) {
	fs := flag.NewFlagSet("backup", flag.ExitOnError)
	dbPath := fs.String("db", envOr("DATABASE_PATH", "./data/guanlanai.db"), "SQLite 数据库路径")
	outDir := fs.String("out", envOr("BACKUP_DIR", "/data/backups"), "备份输出目录")
	if err := fs.Parse(args); err != nil {
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	path, err := backup.Create(ctx, *dbPath, *outDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "备份失败:", err)
		os.Exit(1)
	}
	fmt.Println(path)
}

func runGenKey() {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		fmt.Fprintln(os.Stderr, "生成密钥失败:", err)
		os.Exit(1)
	}
	fmt.Println(base64.StdEncoding.EncodeToString(buf))
}

func printUsage() {
	fmt.Fprint(os.Stderr, `用法: guanlanai <command>

命令:
  serve     启动 Bot 和健康检查（默认）
  backup    使用 VACUUM INTO 生成一致性备份
  genkey    生成 APP_MASTER_KEY
  version   打印版本
`)
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
