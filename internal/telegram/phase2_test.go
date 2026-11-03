package telegram

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/guanlanai/guanlanai/internal/agent"
	"github.com/guanlanai/guanlanai/internal/analysis"
	"github.com/guanlanai/guanlanai/internal/config"
	"github.com/guanlanai/guanlanai/internal/llm"
	"github.com/guanlanai/guanlanai/internal/market"
	"github.com/guanlanai/guanlanai/internal/netguard"
	"github.com/guanlanai/guanlanai/internal/session"
	"github.com/guanlanai/guanlanai/internal/user"
)

type fakeStore struct {
	usr user.User
}

func (f *fakeStore) GetOrCreate(_ context.Context, telegramUserID int64, username, defaultBaseURL, defaultMode string) (*user.User, error) {
	if f.usr.TelegramUserID == 0 {
		f.usr = user.User{
			TelegramUserID: telegramUserID,
			Username:       username,
			APIBaseURL:     defaultBaseURL,
			Mode:           defaultMode,
			KeyStatus:      "missing",
			Status:         "active",
		}
	}
	f.usr.Username = username
	return &f.usr, nil
}

func (f *fakeStore) UpdateKey(_ context.Context, _ int64, ciphertext, nonce []byte, version int) error {
	f.usr.KeyCiphertext = ciphertext
	f.usr.KeyNonce = nonce
	f.usr.KeyVersion = version
	f.usr.KeyStatus = "valid"
	return nil
}

func (f *fakeStore) DeleteKey(_ context.Context, _ int64) error {
	f.usr.KeyCiphertext = nil
	f.usr.KeyNonce = nil
	f.usr.KeyVersion = 0
	f.usr.KeyStatus = "missing"
	return nil
}

func (f *fakeStore) UpdateBaseURL(_ context.Context, _ int64, baseURL string) error {
	f.usr.APIBaseURL = baseURL
	return nil
}

func (f *fakeStore) UpdateModel(_ context.Context, _ int64, model string) error {
	f.usr.Model = model
	return nil
}

func (f *fakeStore) UpdateMode(_ context.Context, _ int64, mode string) error {
	f.usr.Mode = mode
	return nil
}

func (f *fakeStore) UpdateTimeframe(_ context.Context, _ int64, timeframe string) error {
	f.usr.PreferredTimeframe = timeframe
	return nil
}

func (f *fakeStore) UpdateIndicators(_ context.Context, _ int64, indicators string) error {
	f.usr.PreferredIndicators = indicators
	return nil
}

func (f *fakeStore) UpdateRiskPerTrade(_ context.Context, _ int64, amount float64) error {
	f.usr.RiskPerTrade = amount
	return nil
}

func (f *fakeStore) UpdateStrategy(_ context.Context, _ int64, strategy string) error {
	f.usr.PreferredStrategy = strategy
	return nil
}

type fakeCipher struct{}

func (fakeCipher) Encrypt(plaintext string, _ []byte) ([]byte, []byte, error) {
	return append([]byte("enc:"), []byte(plaintext)...), []byte("nonce"), nil
}

func (fakeCipher) Decrypt(ciphertext, _ []byte, _ []byte) (string, error) {
	return strings.TrimPrefix(string(ciphertext), "enc:"), nil
}

func (fakeCipher) Version() int { return 1 }

type fakeValidator struct {
	validateErr      error
	models           []string
	listErr          error
	validatedBaseURL string
	validatedKey     string
}

func (f *fakeValidator) ValidateKey(_ context.Context, baseURL, apiKey string) error {
	f.validatedBaseURL = baseURL
	f.validatedKey = apiKey
	return f.validateErr
}

func (f *fakeValidator) ListModels(_ context.Context, _, _ string) ([]string, error) {
	return f.models, f.listErr
}

type fakeDeleter struct {
	deleted []int
}

func (f *fakeDeleter) DeleteMessage(_ context.Context, params *bot.DeleteMessageParams) (bool, error) {
	f.deleted = append(f.deleted, params.MessageID)
	return true, nil
}

func newPhase2Service() (*Service, *fakeSender, *fakeStore, *fakeDeleter, *fakeValidator) {
	sender := &fakeSender{}
	store := &fakeStore{}
	deleter := &fakeDeleter{}
	validator := &fakeValidator{}

	service := &Service{
		cfg: config.Config{
			InstanceName:           "test",
			AppEnv:                 "dev",
			OpenAIBaseURL:          "https://api.openai.com/v1",
			DefaultMode:            "swing",
			AllowCustomBaseURL:     true,
			CustomBaseURLAllowlist: nil,
			StrictModelList:        true,
		},
		sender:    sender,
		deleter:   deleter,
		store:     store,
		cipher:    fakeCipher{},
		validator: validator,
		guard:     netguard.Policy{},
		logger:    testLogger(),
		started:   time.Now(),
		pending:   make(map[int64]pendingState),
	}
	return service, sender, store, deleter, validator
}

func privateUpdateID(text string, id int) *models.Update {
	return &models.Update{
		Message: &models.Message{
			ID:   id,
			Text: text,
			Chat: models.Chat{ID: 1, Type: models.ChatTypePrivate},
		},
	}
}

func TestKeyFlow(t *testing.T) {
	service, sender, store, deleter, validator := newPhase2Service()
	ctx := context.Background()

	service.handle(ctx, privateUpdate("/key"))
	if !strings.Contains(sender.last().Text, "下一条消息") {
		t.Fatalf("Key 提示 = %q", sender.last().Text)
	}

	service.handle(ctx, privateUpdateID("sk-test-key", 99))
	if len(deleter.deleted) != 1 || deleter.deleted[0] != 99 {
		t.Fatalf("删除消息记录 = %#v", deleter.deleted)
	}
	if !store.usr.HasKey() {
		t.Fatal("Key 未保存")
	}
	if validator.validatedKey != "sk-test-key" {
		t.Fatalf("校验时使用的 Key = %q", validator.validatedKey)
	}
	if !strings.Contains(sender.last().Text, "API Key 已保存") {
		t.Fatalf("保存回复 = %q", sender.last().Text)
	}
}

func TestInvalidKeyFlow(t *testing.T) {
	service, sender, store, _, validator := newPhase2Service()
	validator.validateErr = llm.ErrInvalidKey
	ctx := context.Background()

	service.handle(ctx, privateUpdate("/key"))
	service.handle(ctx, privateUpdate("sk-bad-key"))

	if store.usr.HasKey() {
		t.Fatal("无效 Key 不应保存")
	}
	if !strings.Contains(sender.last().Text, "校验失败") {
		t.Fatalf("回复 = %q", sender.last().Text)
	}
}

func TestKeyDelete(t *testing.T) {
	service, sender, store, _, _ := newPhase2Service()
	ctx := context.Background()

	service.handle(ctx, privateUpdate("/key"))
	service.handle(ctx, privateUpdate("sk-test-key"))
	service.handle(ctx, privateUpdate("/key delete"))

	if store.usr.HasKey() {
		t.Fatal("Key 未删除")
	}
	if !strings.Contains(sender.last().Text, "已删除") {
		t.Fatalf("回复 = %q", sender.last().Text)
	}
}

func TestStatusShowsMaskedKey(t *testing.T) {
	service, sender, store, _, _ := newPhase2Service()
	ctx := context.Background()

	service.handle(ctx, privateUpdate("/key"))
	service.handle(ctx, privateUpdate("sk-test-key"))
	service.handle(ctx, privateUpdate("/status"))

	if !strings.Contains(sender.last().Text, "sk...ey") {
		t.Fatalf("状态未显示掩码 Key: %q", sender.last().Text)
	}
	if !store.usr.HasKey() {
		t.Fatal("Key 丢失")
	}
}

func TestBaseURLFlow(t *testing.T) {
	service, sender, store, _, _ := newPhase2Service()
	ctx := context.Background()

	service.handle(ctx, privateUpdate("/baseurl"))
	if !strings.Contains(sender.last().Text, "base_url") {
		t.Fatalf("提示 = %q", sender.last().Text)
	}

	service.handle(ctx, privateUpdate("https://1.1.1.1/v1"))
	if store.usr.APIBaseURL != "https://1.1.1.1/v1" {
		t.Fatalf("base_url = %q", store.usr.APIBaseURL)
	}
	if !strings.Contains(sender.last().Text, "已保存 endpoint") {
		t.Fatalf("回复 = %q", sender.last().Text)
	}

	service.handle(ctx, privateUpdate("/baseurl http://1.1.1.1/v1"))
	if !strings.Contains(sender.last().Text, "校验失败") {
		t.Fatalf("http 回复 = %q", sender.last().Text)
	}
}

func TestModelFlow(t *testing.T) {
	service, sender, store, _, validator := newPhase2Service()
	ctx := context.Background()
	store.usr = user.User{
		TelegramUserID: 1,
		KeyCiphertext:  []byte("enc:sk-test-key"),
		KeyNonce:       []byte("nonce"),
		KeyStatus:      "valid",
		APIBaseURL:     "https://api.openai.com/v1",
		Mode:           "swing",
	}
	validator.models = []string{"m1", "m2"}

	service.handle(ctx, privateUpdate("/model"))
	if !strings.Contains(sender.last().Text, "m1") || !strings.Contains(sender.last().Text, "m2") {
		t.Fatalf("模型列表 = %q", sender.last().Text)
	}

	service.handle(ctx, privateUpdate("/model m1"))
	if store.usr.Model != "m1" {
		t.Fatalf("model = %q", store.usr.Model)
	}

	service.handle(ctx, privateUpdate("/model short"))
	if store.usr.Model != "m1" {
		t.Fatalf("无效模型不应覆盖: %q", store.usr.Model)
	}
	if !strings.Contains(sender.last().Text, "失败") {
		t.Fatalf("无效模型回复 = %q", sender.last().Text)
	}
}

func TestStrictModelRequiresModelsList(t *testing.T) {
	service, sender, store, _, validator := newPhase2Service()
	ctx := context.Background()
	store.usr = user.User{
		TelegramUserID: 1,
		KeyCiphertext:  []byte("enc:sk-test-key"),
		KeyNonce:       []byte("nonce"),
		KeyStatus:      "valid",
		APIBaseURL:     "https://api.openai.com/v1",
		Model:          "m1",
		Mode:           "swing",
	}
	validator.listErr = llm.ErrModelsUnavailable

	service.handle(ctx, privateUpdate("/model m2"))

	if store.usr.Model != "m1" {
		t.Fatalf("严格模式不应保存模型: %q", store.usr.Model)
	}
	if !strings.Contains(sender.last().Text, "严格模式") {
		t.Fatalf("回复 = %q", sender.last().Text)
	}
}

func TestNonStrictAllowsManualModel(t *testing.T) {
	service, _, store, _, validator := newPhase2Service()
	service.cfg.StrictModelList = false
	ctx := context.Background()
	store.usr = user.User{
		TelegramUserID: 1,
		KeyCiphertext:  []byte("enc:sk-test-key"),
		KeyNonce:       []byte("nonce"),
		KeyStatus:      "valid",
		APIBaseURL:     "https://api.openai.com/v1",
		Mode:           "swing",
	}
	validator.listErr = llm.ErrModelsUnavailable

	service.handle(ctx, privateUpdate("/model m2"))

	if store.usr.Model != "m2" {
		t.Fatalf("非严格模式应允许手动模型: %q", store.usr.Model)
	}
}

type fakeAnalyzer struct {
	calls      int
	query      string
	mode       string
	strategy   analysis.Strategy
	timeframe  market.Timeframe
	indicators []analysis.Indicator
	sessionID  int64
	result     *agent.Result
	err        error
}

func (f *fakeAnalyzer) Analyze(_ context.Context, _ *user.User, _, query, mode string, strategy analysis.Strategy, timeframe market.Timeframe, indicators []analysis.Indicator, sessionID int64) (*agent.Result, error) {
	f.calls++
	f.query = query
	f.mode = mode
	f.strategy = strategy
	f.timeframe = timeframe
	f.indicators = indicators
	f.sessionID = sessionID
	return f.result, f.err
}

func (f *fakeAnalyzer) Chat(_ context.Context, _ *user.User, _, _, _ string) (*agent.ChatResult, error) {
	return &agent.ChatResult{Content: "answer"}, f.err
}

func (f *fakeAnalyzer) Summarize(_ context.Context, _ *user.User, _, _ string, _ []session.Message) (*agent.ChatResult, error) {
	return &agent.ChatResult{Content: "summary"}, f.err
}

func TestAnalyzeFlow(t *testing.T) {
	service, sender, store, _, _ := newPhase2Service()
	ctx := context.Background()
	store.usr = user.User{
		TelegramUserID: 1,
		KeyCiphertext:  []byte("enc:sk-test-key"),
		KeyNonce:       []byte("nonce"),
		KeyStatus:      "valid",
		APIBaseURL:     "https://api.openai.com/v1",
		Model:          "gpt-5-mini",
		Mode:           "swing",
	}
	analyzer := &fakeAnalyzer{result: &agent.Result{
		Report: &analysis.Report{
			Symbol: "BTC", Pair: "BTCUSDT", Quote: "USDT", Market: "crypto",
			Mode: "swing", Price: 100, ChangePct: 1.2, Source: "test",
			AsOf: time.Now().UTC(),
		},
		Opinion: &agent.Opinion{
			Direction: "bullish", Confidence: "medium", Summary: "test",
			Reasons: []string{"r"}, Risks: []string{"x"},
			KeyLevels: agent.KeyLevels{Support: []float64{90}, Resistance: []float64{110}},
		},
	}}
	service.analyzer = analyzer

	service.handle(ctx, privateUpdate("/analyze BTC"))

	if analyzer.calls != 1 || analyzer.query != "BTC" || analyzer.mode != "swing" {
		t.Fatalf("analyzer = %#v", analyzer)
	}
	if !strings.Contains(sender.last().Text, "BTC/USDT") || !strings.Contains(sender.last().Text, "偏多") {
		t.Fatalf("观点卡片 = %q", sender.last().Text)
	}
}

func TestAnalyzeFlowRequiresKey(t *testing.T) {
	service, sender, _, _, _ := newPhase2Service()

	service.handle(context.Background(), privateUpdate("/analyze BTC"))

	if !strings.Contains(sender.last().Text, "/key") {
		t.Fatalf("回复 = %q", sender.last().Text)
	}
}

func TestSymbolNotFoundReasonMentionsStocks(t *testing.T) {
	message := reason(market.ErrSymbolNotFound)
	if !strings.Contains(message, "股票") {
		t.Fatalf("提示未说明股票不支持: %q", message)
	}
}

func TestModeFlow(t *testing.T) {
	service, sender, store, _, _ := newPhase2Service()
	ctx := context.Background()

	service.handle(ctx, privateUpdate("/mode short"))
	if store.usr.Mode != "short" {
		t.Fatalf("mode = %q", store.usr.Mode)
	}
	if !strings.Contains(sender.last().Text, "short") {
		t.Fatalf("回复 = %q", sender.last().Text)
	}
}
