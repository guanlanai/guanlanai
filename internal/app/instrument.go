package app

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/guanlanai/guanlanai/internal/llm"
	"github.com/guanlanai/guanlanai/internal/market"
)

type llmMetrics interface {
	ObserveLLMCall(operation, status string, duration time.Duration)
	AddLLMTokens(operation string, prompt, completion int)
}

type marketMetrics interface {
	ObserveMarketCall(operation, status string, duration time.Duration)
}

// instrumentedCompleter 在 Completer 外层记录调用次数、耗时和 token。
type instrumentedCompleter struct {
	next    llm.Completer
	metrics llmMetrics
}

func newInstrumentedCompleter(next llm.Completer, metrics llmMetrics) *instrumentedCompleter {
	return &instrumentedCompleter{next: next, metrics: metrics}
}

func (c *instrumentedCompleter) Complete(ctx context.Context, req llm.CompletionRequest) (*llm.CompletionResponse, error) {
	started := time.Now()
	response, err := c.next.Complete(ctx, req)
	c.metrics.ObserveLLMCall("complete", statusFor(err), time.Since(started))
	if err == nil && response != nil {
		c.metrics.AddLLMTokens("complete", response.PromptTokens, response.CompletionTokens)
	}
	return response, err
}

// Stream 让包装后的 Completer 仍然满足 llm.Streamer；底层不支持时返回可降级错误。
func (c *instrumentedCompleter) Stream(ctx context.Context, req llm.CompletionRequest) (llm.Stream, error) {
	streamer, ok := c.next.(llm.Streamer)
	if !ok {
		return nil, llm.ErrUnsupportedFeature
	}
	started := time.Now()
	stream, err := streamer.Stream(ctx, req)
	if err != nil {
		c.metrics.ObserveLLMCall("stream", statusFor(err), time.Since(started))
		return nil, err
	}
	return &instrumentedStream{
		next:    stream,
		metrics: c.metrics,
		started: started,
	}, nil
}

type instrumentedStream struct {
	next    llm.Stream
	metrics llmMetrics
	started time.Time
	once    sync.Once
}

func (s *instrumentedStream) Recv() (string, error) {
	delta, err := s.next.Recv()
	switch {
	case err == nil:
		return delta, nil
	case errors.Is(err, io.EOF):
		s.finish("ok")
		return "", io.EOF
	default:
		s.finish("error")
		return "", err
	}
}

func (s *instrumentedStream) Response() *llm.CompletionResponse {
	return s.next.Response()
}

func (s *instrumentedStream) Close() error {
	err := s.next.Close()
	status := "cancelled"
	if err != nil {
		status = "error"
	}
	s.finish(status)
	return err
}

func (s *instrumentedStream) finish(status string) {
	s.once.Do(func() {
		s.metrics.ObserveLLMCall("stream", status, time.Since(s.started))
		if status == "ok" {
			if response := s.next.Response(); response != nil {
				s.metrics.AddLLMTokens("stream", response.PromptTokens, response.CompletionTokens)
			}
		}
	})
}

// instrumentedMarket 在行情端口外层记录调用次数和耗时。
type instrumentedMarket struct {
	resolver market.Resolver
	provider market.Provider
	metrics  marketMetrics
}

func newInstrumentedMarket(resolver market.Resolver, provider market.Provider, metrics marketMetrics) *instrumentedMarket {
	return &instrumentedMarket{resolver: resolver, provider: provider, metrics: metrics}
}

func (m *instrumentedMarket) Resolve(ctx context.Context, query string) (market.Symbol, error) {
	started := time.Now()
	symbol, err := m.resolver.Resolve(ctx, query)
	m.metrics.ObserveMarketCall("resolve", statusFor(err), time.Since(started))
	return symbol, err
}

func (m *instrumentedMarket) Snapshot(ctx context.Context, symbol market.Symbol, timeframes []market.Timeframe) (*market.Snapshot, error) {
	started := time.Now()
	snapshot, err := m.provider.Snapshot(ctx, symbol, timeframes)
	m.metrics.ObserveMarketCall("snapshot", statusFor(err), time.Since(started))
	return snapshot, err
}

func statusFor(err error) string {
	if err == nil {
		return "ok"
	}
	return "error"
}
