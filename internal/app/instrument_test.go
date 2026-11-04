package app

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/guanlanai/guanlanai/internal/llm"
	"github.com/guanlanai/guanlanai/internal/market"
)

type recordedLLMCall struct {
	operation string
	status    string
}

type fakeLLMMetrics struct {
	calls      []recordedLLMCall
	prompt     int
	completion int
}

func (f *fakeLLMMetrics) ObserveLLMCall(operation, status string, _ time.Duration) {
	f.calls = append(f.calls, recordedLLMCall{operation: operation, status: status})
}

func (f *fakeLLMMetrics) AddLLMTokens(_ string, prompt, completion int) {
	f.prompt += prompt
	f.completion += completion
}

type fakeMarketMetrics struct {
	calls []recordedLLMCall
}

func (f *fakeMarketMetrics) ObserveMarketCall(operation, status string, _ time.Duration) {
	f.calls = append(f.calls, recordedLLMCall{operation: operation, status: status})
}

type stubCompleter struct {
	response *llm.CompletionResponse
	err      error
}

func (s stubCompleter) Complete(context.Context, llm.CompletionRequest) (*llm.CompletionResponse, error) {
	return s.response, s.err
}

type stubStream struct {
	deltas   []string
	response *llm.CompletionResponse
	err      error
	index    int
	closed   bool
}

func (s *stubStream) Recv() (string, error) {
	if s.index < len(s.deltas) {
		delta := s.deltas[s.index]
		s.index++
		return delta, nil
	}
	if s.err != nil {
		return "", s.err
	}
	return "", io.EOF
}

func (s *stubStream) Response() *llm.CompletionResponse { return s.response }
func (s *stubStream) Close() error {
	s.closed = true
	return nil
}

type stubStreamer struct {
	stubCompleter
	stream *stubStream
}

func (s stubStreamer) Stream(context.Context, llm.CompletionRequest) (llm.Stream, error) {
	return s.stream, nil
}

func TestInstrumentedCompleterRecordsSuccess(t *testing.T) {
	recorder := &fakeLLMMetrics{}
	completer := newInstrumentedCompleter(stubCompleter{response: &llm.CompletionResponse{
		PromptTokens: 10, CompletionTokens: 4,
	}}, recorder)

	if _, err := completer.Complete(context.Background(), llm.CompletionRequest{}); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if len(recorder.calls) != 1 || recorder.calls[0] != (recordedLLMCall{"complete", "ok"}) {
		t.Fatalf("calls = %#v", recorder.calls)
	}
	if recorder.prompt != 10 || recorder.completion != 4 {
		t.Fatalf("tokens = %d/%d", recorder.prompt, recorder.completion)
	}
}

func TestInstrumentedCompleterRecordsError(t *testing.T) {
	recorder := &fakeLLMMetrics{}
	completer := newInstrumentedCompleter(stubCompleter{err: errors.New("boom")}, recorder)

	if _, err := completer.Complete(context.Background(), llm.CompletionRequest{}); err == nil {
		t.Fatal("Complete() 应当返回错误")
	}
	if len(recorder.calls) != 1 || recorder.calls[0] != (recordedLLMCall{"complete", "error"}) {
		t.Fatalf("calls = %#v", recorder.calls)
	}
	if recorder.prompt != 0 || recorder.completion != 0 {
		t.Fatalf("错误调用不应记录 token: %d/%d", recorder.prompt, recorder.completion)
	}
}

func TestInstrumentedStreamRecordsOnce(t *testing.T) {
	recorder := &fakeLLMMetrics{}
	stream := &stubStream{
		deltas:   []string{"hello", " world"},
		response: &llm.CompletionResponse{PromptTokens: 7, CompletionTokens: 2},
	}
	completer := newInstrumentedCompleter(stubStreamer{stream: stream}, recorder)

	observed, err := completer.Stream(context.Background(), llm.CompletionRequest{})
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	for {
		_, err := observed.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("Recv() error = %v", err)
		}
	}
	if err := observed.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	if len(recorder.calls) != 1 || recorder.calls[0] != (recordedLLMCall{"stream", "ok"}) {
		t.Fatalf("calls = %#v", recorder.calls)
	}
	if recorder.prompt != 7 || recorder.completion != 2 {
		t.Fatalf("tokens = %d/%d", recorder.prompt, recorder.completion)
	}
}

func TestInstrumentedStreamRecordsError(t *testing.T) {
	recorder := &fakeLLMMetrics{}
	stream := &stubStream{err: errors.New("stream broke")}
	completer := newInstrumentedCompleter(stubStreamer{stream: stream}, recorder)

	observed, err := completer.Stream(context.Background(), llm.CompletionRequest{})
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	if _, err := observed.Recv(); err == nil || errors.Is(err, io.EOF) {
		t.Fatalf("Recv() error = %v, want non-EOF error", err)
	}
	if err := observed.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	if len(recorder.calls) != 1 || recorder.calls[0] != (recordedLLMCall{"stream", "error"}) {
		t.Fatalf("calls = %#v", recorder.calls)
	}
}

func TestInstrumentedCompleterFallsBackWhenStreamUnsupported(t *testing.T) {
	recorder := &fakeLLMMetrics{}
	completer := newInstrumentedCompleter(stubCompleter{}, recorder)

	if _, err := completer.Stream(context.Background(), llm.CompletionRequest{}); !errors.Is(err, llm.ErrUnsupportedFeature) {
		t.Fatalf("Stream() error = %v, want ErrUnsupportedFeature", err)
	}
	if len(recorder.calls) != 0 {
		t.Fatalf("未发起调用时不应记录指标: %#v", recorder.calls)
	}
}

type stubMarket struct {
	symbol   market.Symbol
	snapshot *market.Snapshot
	err      error
}

func (s stubMarket) Resolve(context.Context, string) (market.Symbol, error) {
	return s.symbol, s.err
}

func (s stubMarket) Snapshot(context.Context, market.Symbol, []market.Timeframe) (*market.Snapshot, error) {
	return s.snapshot, s.err
}

func TestInstrumentedMarketRecordsCalls(t *testing.T) {
	recorder := &fakeMarketMetrics{}
	client := stubMarket{
		symbol:   market.Symbol{Code: "BTC"},
		snapshot: &market.Snapshot{Price: 100},
	}
	instrumented := newInstrumentedMarket(client, client, recorder)

	if _, err := instrumented.Resolve(context.Background(), "BTC"); err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if _, err := instrumented.Snapshot(context.Background(), market.Symbol{}, nil); err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if len(recorder.calls) != 2 {
		t.Fatalf("calls = %#v", recorder.calls)
	}
	if recorder.calls[0] != (recordedLLMCall{"resolve", "ok"}) || recorder.calls[1] != (recordedLLMCall{"snapshot", "ok"}) {
		t.Fatalf("calls = %#v", recorder.calls)
	}
}
