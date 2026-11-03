package config

import (
	"encoding/base64"
	"strings"
	"testing"
)

var configEnvKeys = []string{
	"APP_ENV", "INSTANCE_NAME", "TELEGRAM_BOT_TOKEN", "APP_MASTER_KEY",
	"DATABASE_PATH", "HTTP_ADDR", "LOG_LEVEL",
	"DEFAULT_MODE", "DEFAULT_MODEL", "MODEL_ALLOWLIST", "STRICT_MODEL_LIST",
	"OPENAI_BASE_URL", "OPENAI_API_KEY",
	"BINANCE_BASE_URL", "BINANCE_FALLBACK_BASE_URL", "COINGECKO_BASE_URL",
	"ALLOW_SERVER_KEY", "ALLOW_CUSTOM_BASE_URL", "CUSTOM_BASE_URL_ALLOWLIST",
	"ALLOW_INSECURE_BASE_URL", "ALLOW_PRIVATE_BASE_URL_FOR_DEV",
	"CUSTOM_BASE_URL_TIMEOUT", "CUSTOM_BASE_URL_MAX_RESPONSE_BYTES",
	"DATA_RETENTION_DAYS", "MAX_PRIVATE_SESSIONS", "GROUPS_ENABLED",
	"ANALYSIS_RATE_PER_MIN", "ANALYSIS_RATE_PER_HOUR", "CHAT_RATE_PER_MIN",
	"TELEGRAM_MAX_CONCURRENCY", "LLM_MAX_CONCURRENCY", "MARKET_MAX_CONCURRENCY",
	"MAX_PROMPT_TOKENS", "MAX_OUTPUT_TOKENS", "GLOBAL_DAILY_LLM_TOKENS",
	"TRADING_ENABLED",
}

func clearConfigEnv(t *testing.T) {
	t.Helper()
	for _, key := range configEnvKeys {
		t.Setenv(key, "")
	}
}

func validMasterKey() string {
	return base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32)))
}

func TestLoadDefaults(t *testing.T) {
	clearConfigEnv(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.AppEnv != "dev" {
		t.Fatalf("AppEnv = %q, want dev", cfg.AppEnv)
	}
	if cfg.HTTPAddr != ":8080" {
		t.Fatalf("HTTPAddr = %q, want :8080", cfg.HTTPAddr)
	}
	if cfg.BinanceBaseURL != "https://data-api.binance.vision" {
		t.Fatalf("BinanceBaseURL = %q", cfg.BinanceBaseURL)
	}
	if cfg.DefaultMode != "swing" {
		t.Fatalf("DefaultMode = %q, want swing", cfg.DefaultMode)
	}
	if cfg.TradingEnabled {
		t.Fatal("TradingEnabled 默认必须为 false")
	}
	if !cfg.StrictModelList {
		t.Fatal("StrictModelList 默认必须为 true")
	}
	if cfg.MaxPrivateSessions != 20 {
		t.Fatalf("MaxPrivateSessions = %d, want 20", cfg.MaxPrivateSessions)
	}
	if cfg.GroupsEnabled {
		t.Fatal("GroupsEnabled 默认必须为 false")
	}
}

func TestLoadRejectsTradingEnabled(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("TRADING_ENABLED", "true")

	if _, err := Load(); err == nil {
		t.Fatal("TRADING_ENABLED=true 时应当报错")
	}
}

func TestLoadRejectsInvalidMasterKey(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("APP_MASTER_KEY", "not-base64")

	if _, err := Load(); err == nil {
		t.Fatal("无效 APP_MASTER_KEY 应当报错")
	}
}

func TestLoadProdRequiresMasterKey(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("APP_ENV", "prod")

	if _, err := Load(); err == nil {
		t.Fatal("prod 缺少 APP_MASTER_KEY 应当报错")
	}
}

func TestLoadProdRequiresTelegramToken(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("APP_ENV", "prod")
	t.Setenv("APP_MASTER_KEY", validMasterKey())

	if _, err := Load(); err == nil {
		t.Fatal("prod 缺少 TELEGRAM_BOT_TOKEN 应当报错")
	}
}

func TestLoadRejectsInsecureBaseURLInProd(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("APP_ENV", "prod")
	t.Setenv("APP_MASTER_KEY", validMasterKey())
	t.Setenv("ALLOW_INSECURE_BASE_URL", "true")

	if _, err := Load(); err == nil {
		t.Fatal("prod 允许 http base_url 应当报错")
	}
}

func TestLoadRejectsInvalidMode(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("DEFAULT_MODE", "day")

	if _, err := Load(); err == nil {
		t.Fatal("无效 DEFAULT_MODE 应当报错")
	}
}
