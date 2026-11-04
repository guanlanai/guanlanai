package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/guanlanai/guanlanai/internal/market"
)

// Config 是进程的完整运行配置，全部来自环境变量。
type Config struct {
	AppEnv        string
	InstanceName  string
	TelegramToken string
	MasterKey     string

	DatabasePath string
	HTTPAddr     string
	LogLevel     string

	MetricsEnabled bool

	DataRetentionDays  int
	MaxPrivateSessions int
	GroupsEnabled      bool
	DefaultMode        string
	DefaultTimeframe   market.Timeframe
	DefaultModel       string
	ModelAllowlist     []string
	StrictModelList    bool

	OpenAIBaseURL   string
	OpenAIServerKey string
	AllowServerKey  bool

	BinanceBaseURL         string
	BinanceFallbackBaseURL string
	CoinGeckoBaseURL       string

	AllowCustomBaseURL        bool
	CustomBaseURLAllowlist    []string
	AllowInsecureBaseURL      bool
	AllowPrivateBaseURLForDev bool
	CustomBaseURLTimeout      time.Duration
	CustomBaseURLMaxBytes     int64

	AnalysisRatePerMin  int
	AnalysisRatePerHour int
	ChatRatePerMin      int

	TelegramMaxConcurrency int
	LLMMaxConcurrency      int
	MarketMaxConcurrency   int
	MaxPromptTokens        int
	MaxOutputTokens        int
	GlobalDailyLLMTokens   int64
	AgentMaxToolRounds     int
	LLMStreaming           bool

	TradingEnabled bool
}

// Load 从环境变量读取配置，并在启动时尽早失败。
func Load() (Config, error) {
	cfg := Config{
		AppEnv:        getenv("APP_ENV", "dev"),
		InstanceName:  strings.TrimSpace(os.Getenv("INSTANCE_NAME")),
		TelegramToken: strings.TrimSpace(os.Getenv("TELEGRAM_BOT_TOKEN")),
		MasterKey:     strings.TrimSpace(os.Getenv("APP_MASTER_KEY")),

		DatabasePath: getenv("DATABASE_PATH", "./data/guanlanai.db"),
		HTTPAddr:     getenv("HTTP_ADDR", ":8080"),
		LogLevel:     getenv("LOG_LEVEL", "info"),

		DefaultMode:      getenv("DEFAULT_MODE", "swing"),
		DefaultTimeframe: market.DefaultTimeframe,
		DefaultModel:     strings.TrimSpace(os.Getenv("DEFAULT_MODEL")),
		ModelAllowlist:   splitCSV(os.Getenv("MODEL_ALLOWLIST")),

		OpenAIBaseURL:   getenv("OPENAI_BASE_URL", "https://api.openai.com/v1"),
		OpenAIServerKey: strings.TrimSpace(os.Getenv("OPENAI_API_KEY")),

		BinanceBaseURL:         getenv("BINANCE_BASE_URL", "https://data-api.binance.vision"),
		BinanceFallbackBaseURL: getenv("BINANCE_FALLBACK_BASE_URL", "https://api.binance.com"),
		CoinGeckoBaseURL:       getenv("COINGECKO_BASE_URL", "https://api.coingecko.com/api/v3"),

		CustomBaseURLAllowlist: splitCSV(os.Getenv("CUSTOM_BASE_URL_ALLOWLIST")),
	}

	if cfg.InstanceName == "" {
		cfg.InstanceName = "guanlanai-" + cfg.AppEnv
	}

	timeframe, err := market.ParseTimeframe(getenv("DEFAULT_TIMEFRAME", market.DefaultTimeframe.String()))
	if err != nil {
		return Config{}, fmt.Errorf("DEFAULT_TIMEFRAME 必须是标准周期（%s）: %w", market.TimeframesLabel(), err)
	}
	cfg.DefaultTimeframe = timeframe

	if cfg.DataRetentionDays, err = intEnv("DATA_RETENTION_DAYS", 30); err != nil {
		return Config{}, err
	}
	if cfg.MetricsEnabled, err = boolEnv("METRICS_ENABLED", true); err != nil {
		return Config{}, err
	}
	if cfg.MaxPrivateSessions, err = intEnv("MAX_PRIVATE_SESSIONS", 20); err != nil {
		return Config{}, err
	}
	if cfg.GroupsEnabled, err = boolEnv("GROUPS_ENABLED", false); err != nil {
		return Config{}, err
	}
	if cfg.StrictModelList, err = boolEnv("STRICT_MODEL_LIST", true); err != nil {
		return Config{}, err
	}
	if cfg.AllowServerKey, err = boolEnv("ALLOW_SERVER_KEY", false); err != nil {
		return Config{}, err
	}
	if cfg.AllowCustomBaseURL, err = boolEnv("ALLOW_CUSTOM_BASE_URL", true); err != nil {
		return Config{}, err
	}
	if cfg.AllowInsecureBaseURL, err = boolEnv("ALLOW_INSECURE_BASE_URL", false); err != nil {
		return Config{}, err
	}
	if cfg.AllowPrivateBaseURLForDev, err = boolEnv("ALLOW_PRIVATE_BASE_URL_FOR_DEV", false); err != nil {
		return Config{}, err
	}
	if cfg.CustomBaseURLTimeout, err = durationEnv("CUSTOM_BASE_URL_TIMEOUT", 10*time.Second); err != nil {
		return Config{}, err
	}
	if cfg.CustomBaseURLMaxBytes, err = int64Env("CUSTOM_BASE_URL_MAX_RESPONSE_BYTES", 2*1024*1024); err != nil {
		return Config{}, err
	}
	if cfg.AnalysisRatePerMin, err = intEnv("ANALYSIS_RATE_PER_MIN", 6); err != nil {
		return Config{}, err
	}
	if cfg.AnalysisRatePerHour, err = intEnv("ANALYSIS_RATE_PER_HOUR", 30); err != nil {
		return Config{}, err
	}
	if cfg.ChatRatePerMin, err = intEnv("CHAT_RATE_PER_MIN", 20); err != nil {
		return Config{}, err
	}
	if cfg.TelegramMaxConcurrency, err = intEnv("TELEGRAM_MAX_CONCURRENCY", 32); err != nil {
		return Config{}, err
	}
	if cfg.LLMMaxConcurrency, err = intEnv("LLM_MAX_CONCURRENCY", 16); err != nil {
		return Config{}, err
	}
	if cfg.MarketMaxConcurrency, err = intEnv("MARKET_MAX_CONCURRENCY", 8); err != nil {
		return Config{}, err
	}
	if cfg.MaxPromptTokens, err = intEnv("MAX_PROMPT_TOKENS", 6000); err != nil {
		return Config{}, err
	}
	if cfg.MaxOutputTokens, err = intEnv("MAX_OUTPUT_TOKENS", 1200); err != nil {
		return Config{}, err
	}
	if cfg.GlobalDailyLLMTokens, err = int64Env("GLOBAL_DAILY_LLM_TOKENS", 2_000_000); err != nil {
		return Config{}, err
	}
	if cfg.AgentMaxToolRounds, err = intEnv("AGENT_MAX_TOOL_ROUNDS", 3); err != nil {
		return Config{}, err
	}
	if cfg.LLMStreaming, err = boolEnv("LLM_STREAMING", true); err != nil {
		return Config{}, err
	}
	if cfg.TradingEnabled, err = boolEnv("TRADING_ENABLED", false); err != nil {
		return Config{}, err
	}

	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) validate() error {
	switch c.AppEnv {
	case "dev", "staging", "prod":
	default:
		return fmt.Errorf("APP_ENV 只能是 dev/staging/prod，当前为 %q", c.AppEnv)
	}

	if c.DatabasePath == "" {
		return errors.New("DATABASE_PATH 不能为空")
	}
	if c.HTTPAddr == "" {
		return errors.New("HTTP_ADDR 不能为空")
	}
	if c.DefaultMode != "short" && c.DefaultMode != "swing" {
		return fmt.Errorf("DEFAULT_MODE 只能是 short/swing，当前为 %q", c.DefaultMode)
	}
	if !c.DefaultTimeframe.Valid() {
		return fmt.Errorf("DEFAULT_TIMEFRAME 必须是标准周期（%s），当前为 %q", market.TimeframesLabel(), c.DefaultTimeframe)
	}

	if c.MasterKey == "" {
		if c.AppEnv != "dev" {
			return errors.New("staging/prod 必须设置 APP_MASTER_KEY（32 字节随机值的 base64）")
		}
	} else {
		raw, err := base64.StdEncoding.DecodeString(c.MasterKey)
		if err != nil || len(raw) != 32 {
			return errors.New("APP_MASTER_KEY 必须是 32 字节随机值的 base64")
		}
	}

	if c.TelegramToken == "" && c.AppEnv != "dev" {
		return errors.New("staging/prod 必须设置 TELEGRAM_BOT_TOKEN")
	}
	if c.TradingEnabled {
		return errors.New("TRADING_ENABLED 暂不支持：MVP 不接真实下单")
	}
	if c.AppEnv == "prod" && c.AllowInsecureBaseURL {
		return errors.New("prod 环境禁止 ALLOW_INSECURE_BASE_URL=true")
	}
	if c.AppEnv == "prod" && c.AllowPrivateBaseURLForDev {
		return errors.New("prod 环境禁止 ALLOW_PRIVATE_BASE_URL_FOR_DEV=true")
	}

	if c.CustomBaseURLTimeout <= 0 {
		return errors.New("CUSTOM_BASE_URL_TIMEOUT 必须大于 0")
	}
	if c.CustomBaseURLMaxBytes <= 0 {
		return errors.New("CUSTOM_BASE_URL_MAX_RESPONSE_BYTES 必须大于 0")
	}
	if c.DataRetentionDays <= 0 {
		return errors.New("DATA_RETENTION_DAYS 必须大于 0")
	}
	if c.MaxPrivateSessions <= 0 {
		return errors.New("MAX_PRIVATE_SESSIONS 必须大于 0")
	}
	if c.AnalysisRatePerMin <= 0 || c.AnalysisRatePerHour <= 0 || c.ChatRatePerMin <= 0 {
		return errors.New("限流配置必须大于 0")
	}
	if c.TelegramMaxConcurrency <= 0 {
		return errors.New("TELEGRAM_MAX_CONCURRENCY 必须大于 0")
	}
	if c.LLMMaxConcurrency <= 0 || c.MarketMaxConcurrency <= 0 {
		return errors.New("并发配置必须大于 0")
	}
	if c.MaxPromptTokens <= 0 || c.MaxOutputTokens <= 0 {
		return errors.New("token 上限必须大于 0")
	}
	if c.GlobalDailyLLMTokens <= 0 {
		return errors.New("GLOBAL_DAILY_LLM_TOKENS 必须大于 0")
	}
	if c.AgentMaxToolRounds < 0 {
		return errors.New("AGENT_MAX_TOOL_ROUNDS 不能小于 0")
	}
	if c.OpenAIBaseURL == "" || c.BinanceBaseURL == "" || c.CoinGeckoBaseURL == "" {
		return errors.New("上游 API base URL 不能为空")
	}

	return nil
}

func getenv(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func splitCSV(value string) []string {
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func intEnv(key string, fallback int) (int, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s 必须是整数: %w", key, err)
	}
	return value, nil
}

func int64Env(key string, fallback int64) (int64, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s 必须是整数: %w", key, err)
	}
	return value, nil
}

func boolEnv(key string, fallback bool) (bool, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("%s 必须是布尔值: %w", key, err)
	}
	return value, nil
}

func durationEnv(key string, fallback time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}
	value, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%s 必须是时间长度（如 10s）: %w", key, err)
	}
	return value, nil
}
