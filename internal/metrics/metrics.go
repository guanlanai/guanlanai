// Package metrics 提供进程内的 Prometheus 指标注册和采集。
//
// 指标只使用有界的标签值（operation、status、kind 等），不包含用户 ID、
// chat ID、币种或模型名，避免标签基数失控。
package metrics

import (
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const namespace = "guanlanai"

// Metrics 持有本进程独立的 Prometheus registry 和全部指标。
type Metrics struct {
	registry *prometheus.Registry
	handler  http.Handler

	requests           *prometheus.CounterVec
	requestDuration    *prometheus.HistogramVec
	inflight           *prometheus.GaugeVec
	llmCalls           *prometheus.CounterVec
	llmCallDuration    *prometheus.HistogramVec
	llmTokens          *prometheus.CounterVec
	rateLimitHits      *prometheus.CounterVec
	telegramUpdates    *prometheus.CounterVec
	telegramMessages   *prometheus.CounterVec
	marketCalls        *prometheus.CounterVec
	marketCallDuration *prometheus.HistogramVec
}

type options struct {
	instance string
	env      string
	version  string
	commit   string
}

// Option 调整指标注册时的附加信息。
type Option func(*options)

// WithBuildInfo 为 guanlanai_build_info 增加实例和版本标签。
func WithBuildInfo(instance, env, version, commit string) Option {
	return func(o *options) {
		o.instance = instance
		o.env = env
		o.version = version
		o.commit = commit
	}
}

// New 创建独立的指标集合，并注册 Go runtime 和 process 指标。
// 使用独立 registry 而不是 prometheus.DefaultRegisterer，避免测试或多次构造时重复注册。
func New(opts ...Option) *Metrics {
	settings := options{}
	for _, opt := range opts {
		opt(&settings)
	}

	registry := prometheus.NewRegistry()
	registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)

	m := &Metrics{
		registry: registry,
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "requests_total",
			Help:      "Total user-facing agent requests by kind and status.",
		}, []string{"kind", "status"}),
		requestDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "request_duration_seconds",
			Help:      "User-facing agent request latency in seconds by kind.",
			Buckets:   []float64{0.1, 0.25, 0.5, 1, 2.5, 5, 10, 20, 30, 45, 60, 90, 120},
		}, []string{"kind"}),
		inflight: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "requests_in_flight",
			Help:      "Number of agent requests currently being processed by kind.",
		}, []string{"kind"}),
		llmCalls: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "llm_calls_total",
			Help:      "Total LLM calls by operation and status.",
		}, []string{"operation", "status"}),
		llmCallDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "llm_call_duration_seconds",
			Help:      "LLM call latency in seconds by operation.",
			Buckets:   []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 20, 30, 45, 60, 90, 120},
		}, []string{"operation"}),
		llmTokens: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "llm_tokens_total",
			Help:      "Total LLM tokens by operation and token type.",
		}, []string{"operation", "type"}),
		rateLimitHits: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "rate_limit_hits_total",
			Help:      "Total rejected requests due to per-user rate limits by kind.",
		}, []string{"kind"}),
		telegramUpdates: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "telegram_updates_total",
			Help:      "Total Telegram updates by handling result.",
		}, []string{"result"}),
		telegramMessages: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "telegram_messages_total",
			Help:      "Total Telegram send operations by status.",
		}, []string{"status"}),
		marketCalls: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "market_calls_total",
			Help:      "Total market data calls by operation and status.",
		}, []string{"operation", "status"}),
		marketCallDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "market_call_duration_seconds",
			Help:      "Market data call latency in seconds by operation.",
			Buckets:   []float64{0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 20, 30},
		}, []string{"operation"}),
	}

	buildInfo := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "build_info",
		Help:      "Build and deployment information for this process.",
	}, []string{"instance", "env", "version", "commit"})
	buildInfo.WithLabelValues(settings.instance, settings.env, settings.version, settings.commit).Set(1)

	registry.MustRegister(
		m.requests,
		m.requestDuration,
		m.inflight,
		m.llmCalls,
		m.llmCallDuration,
		m.llmTokens,
		m.rateLimitHits,
		m.telegramUpdates,
		m.telegramMessages,
		m.marketCalls,
		m.marketCallDuration,
		buildInfo,
	)

	m.handler = promhttp.HandlerFor(registry, promhttp.HandlerOpts{
		EnableOpenMetrics: true,
	})
	m.initSeries()
	return m
}

// initSeries 预先创建有界标签组合，让仪表盘在第一次请求前就能看到 0 值序列。
func (m *Metrics) initSeries() {
	for _, kind := range []string{"analysis", "chat", "summarize"} {
		m.inflight.WithLabelValues(kind)
		m.requestDuration.WithLabelValues(kind)
		for _, status := range []string{"ok", "error"} {
			m.requests.WithLabelValues(kind, status)
		}
	}
	for _, operation := range []string{"complete", "stream"} {
		m.llmCallDuration.WithLabelValues(operation)
		for _, status := range []string{"ok", "error", "cancelled"} {
			m.llmCalls.WithLabelValues(operation, status)
		}
		for _, tokenType := range []string{"prompt", "completion"} {
			m.llmTokens.WithLabelValues(operation, tokenType)
		}
	}
	for _, kind := range []string{"analysis", "chat"} {
		m.rateLimitHits.WithLabelValues(kind)
	}
	for _, result := range []string{"accepted", "ignored", "overloaded"} {
		m.telegramUpdates.WithLabelValues(result)
	}
	for _, status := range []string{"ok", "error"} {
		m.telegramMessages.WithLabelValues(status)
	}
	for _, operation := range []string{"resolve", "snapshot"} {
		m.marketCallDuration.WithLabelValues(operation)
		for _, status := range []string{"ok", "error"} {
			m.marketCalls.WithLabelValues(operation, status)
		}
	}
}

// Handler 返回 /metrics 的 HTTP handler。
func (m *Metrics) Handler() http.Handler {
	if m == nil {
		return http.NotFoundHandler()
	}
	return m.handler
}

// Registry 返回底层 registry，主要用于测试。
func (m *Metrics) Registry() *prometheus.Registry {
	if m == nil {
		return nil
	}
	return m.registry
}

// ObserveRequest 记录一次用户侧请求的结果和耗时。
func (m *Metrics) ObserveRequest(kind, status string, duration time.Duration) {
	if m == nil {
		return
	}
	m.requests.WithLabelValues(kind, status).Inc()
	m.requestDuration.WithLabelValues(kind).Observe(duration.Seconds())
}

// AddInflight 调整正在处理的请求数。
func (m *Metrics) AddInflight(kind string, delta int) {
	if m == nil {
		return
	}
	m.inflight.WithLabelValues(kind).Add(float64(delta))
}

// ObserveLLMCall 记录一次 LLM 调用的结果和耗时。
func (m *Metrics) ObserveLLMCall(operation, status string, duration time.Duration) {
	if m == nil {
		return
	}
	m.llmCalls.WithLabelValues(operation, status).Inc()
	m.llmCallDuration.WithLabelValues(operation).Observe(duration.Seconds())
}

// AddLLMTokens 累加一次 LLM 调用的输入或输出 token。
func (m *Metrics) AddLLMTokens(operation string, prompt, completion int) {
	if m == nil {
		return
	}
	if prompt > 0 {
		m.llmTokens.WithLabelValues(operation, "prompt").Add(float64(prompt))
	}
	if completion > 0 {
		m.llmTokens.WithLabelValues(operation, "completion").Add(float64(completion))
	}
}

// ObserveRateLimit 记录一次被限流拒绝的请求。
func (m *Metrics) ObserveRateLimit(kind string) {
	if m == nil {
		return
	}
	m.rateLimitHits.WithLabelValues(kind).Inc()
}

// ObserveTelegramUpdate 记录一个 Telegram update 的处理结果。
func (m *Metrics) ObserveTelegramUpdate(result string) {
	if m == nil {
		return
	}
	m.telegramUpdates.WithLabelValues(result).Inc()
}

// ObserveTelegramMessage 记录一次 Telegram 发送操作的结果。
func (m *Metrics) ObserveTelegramMessage(status string) {
	if m == nil {
		return
	}
	m.telegramMessages.WithLabelValues(status).Inc()
}

// ObserveMarketCall 记录一次行情调用的结果和耗时。
func (m *Metrics) ObserveMarketCall(operation, status string, duration time.Duration) {
	if m == nil {
		return
	}
	m.marketCalls.WithLabelValues(operation, status).Inc()
	m.marketCallDuration.WithLabelValues(operation).Observe(duration.Seconds())
}
