package metrics

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestMetricsRecordAndExpose(t *testing.T) {
	m := New(WithBuildInfo("guanlanai-test", "test", "v1", "abc"))

	m.ObserveRequest("analysis", "ok", 1500*time.Millisecond)
	m.ObserveRequest("analysis", "error", 2*time.Second)
	m.AddInflight("analysis", 1)
	m.AddInflight("analysis", -1)
	m.ObserveLLMCall("complete", "ok", time.Second)
	m.AddLLMTokens("complete", 120, 30)
	m.ObserveRateLimit("chat")
	m.ObserveTelegramUpdate("accepted")
	m.ObserveTelegramMessage("ok")
	m.ObserveMarketCall("snapshot", "ok", 200*time.Millisecond)

	if got := testutil.ToFloat64(m.requests.WithLabelValues("analysis", "ok")); got != 1 {
		t.Fatalf("analysis ok requests = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.requests.WithLabelValues("analysis", "error")); got != 1 {
		t.Fatalf("analysis error requests = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.inflight.WithLabelValues("analysis")); got != 0 {
		t.Fatalf("inflight analysis = %v, want 0", got)
	}
	if got := testutil.ToFloat64(m.llmCalls.WithLabelValues("complete", "ok")); got != 1 {
		t.Fatalf("llm complete calls = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.llmTokens.WithLabelValues("complete", "prompt")); got != 120 {
		t.Fatalf("prompt tokens = %v, want 120", got)
	}
	if got := testutil.ToFloat64(m.llmTokens.WithLabelValues("complete", "completion")); got != 30 {
		t.Fatalf("completion tokens = %v, want 30", got)
	}
	if got := testutil.ToFloat64(m.rateLimitHits.WithLabelValues("chat")); got != 1 {
		t.Fatalf("rate limit hits = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.telegramUpdates.WithLabelValues("accepted")); got != 1 {
		t.Fatalf("telegram updates = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.telegramMessages.WithLabelValues("ok")); got != 1 {
		t.Fatalf("telegram messages = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.marketCalls.WithLabelValues("snapshot", "ok")); got != 1 {
		t.Fatalf("market calls = %v, want 1", got)
	}

	recorder := httptest.NewRecorder()
	m.Handler().ServeHTTP(recorder, httptest.NewRequest("GET", "/metrics", nil))
	if recorder.Code != 200 {
		t.Fatalf("/metrics status = %d, want 200", recorder.Code)
	}
	body := recorder.Body.String()
	for _, name := range []string{
		"guanlanai_requests_total",
		"guanlanai_request_duration_seconds",
		"guanlanai_llm_tokens_total",
		"guanlanai_build_info",
		"go_goroutines",
	} {
		if !strings.Contains(body, name) {
			t.Fatalf("/metrics 缺少 %s", name)
		}
	}
}

func TestNilMetricsIsSafe(t *testing.T) {
	var m *Metrics
	m.ObserveRequest("analysis", "ok", time.Second)
	m.AddInflight("analysis", 1)
	m.ObserveLLMCall("complete", "ok", time.Second)
	m.AddLLMTokens("complete", 1, 1)
	m.ObserveRateLimit("analysis")
	m.ObserveTelegramUpdate("accepted")
	m.ObserveTelegramMessage("ok")
	m.ObserveMarketCall("resolve", "ok", time.Second)
	if m.Registry() != nil {
		t.Fatal("nil Metrics 的 Registry 必须为 nil")
	}
	if m.Handler() == nil {
		t.Fatal("nil Metrics 的 Handler 不能为 nil")
	}
}

func TestSeriesInitialized(t *testing.T) {
	m := New()
	recorder := httptest.NewRecorder()
	m.Handler().ServeHTTP(recorder, httptest.NewRequest("GET", "/metrics", nil))
	body := recorder.Body.String()
	for _, expected := range []string{
		`guanlanai_requests_total{kind="analysis",status="ok"} 0`,
		`guanlanai_llm_calls_total{operation="stream",status="cancelled"} 0`,
		`guanlanai_market_calls_total{operation="snapshot",status="error"} 0`,
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("/metrics 缺少初始序列 %q", expected)
		}
	}
}

func TestIndependentRegistries(t *testing.T) {
	first := New()
	second := New()
	first.ObserveRateLimit("analysis")
	if got := testutil.ToFloat64(second.rateLimitHits.WithLabelValues("analysis")); got != 0 {
		t.Fatalf("第二个 registry 不应共享指标，got %v", got)
	}
}
