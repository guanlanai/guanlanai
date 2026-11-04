package agent

import (
	"context"
	"testing"
	"time"

	"github.com/guanlanai/guanlanai/internal/analysis"
	"github.com/guanlanai/guanlanai/internal/market"
)

type observedRequest struct {
	kind   string
	status string
}

type fakeMetrics struct {
	inflight    int
	inflightMax int
	requests    []observedRequest
}

func (f *fakeMetrics) AddInflight(_ string, delta int) {
	f.inflight += delta
	if f.inflight > f.inflightMax {
		f.inflightMax = f.inflight
	}
}

func (f *fakeMetrics) ObserveRequest(kind, status string, _ time.Duration) {
	f.requests = append(f.requests, observedRequest{kind: kind, status: status})
}

func TestAnalyzeRecordsMetrics(t *testing.T) {
	recorder := &fakeMetrics{}
	completer := &fakeCompleter{responses: []string{validOpinion()}}
	service := New(fakeResolver{}, fakeProvider{}, completer, "", 1200, 4, testLogger(), WithMetrics(recorder))

	if _, err := service.Analyze(context.Background(), testUser(), "sk-test", "BTC", "swing", analysis.StrategySingle, market.TF1D, analysis.DefaultIndicators(), 0); err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if len(recorder.requests) != 1 {
		t.Fatalf("requests = %#v", recorder.requests)
	}
	if recorder.requests[0] != (observedRequest{"analysis", "ok"}) {
		t.Fatalf("request = %#v", recorder.requests[0])
	}
	if recorder.inflight != 0 || recorder.inflightMax != 1 {
		t.Fatalf("inflight = %d, max = %d", recorder.inflight, recorder.inflightMax)
	}
}

func TestAnalyzeRecordsErrorMetrics(t *testing.T) {
	recorder := &fakeMetrics{}
	service := New(fakeResolver{}, fakeProvider{}, &fakeCompleter{}, "", 1200, 4, testLogger(), WithMetrics(recorder))

	if _, err := service.Analyze(context.Background(), testUser(), "sk-test", "NOPE", "swing", analysis.StrategySingle, market.TF1D, analysis.DefaultIndicators(), 0); err == nil {
		t.Fatal("Analyze() 应当返回错误")
	}
	if len(recorder.requests) != 1 || recorder.requests[0] != (observedRequest{"analysis", "error"}) {
		t.Fatalf("requests = %#v", recorder.requests)
	}
	if recorder.inflight != 0 {
		t.Fatalf("inflight = %d, want 0", recorder.inflight)
	}
}

func TestChatRecordsMetrics(t *testing.T) {
	recorder := &fakeMetrics{}
	completer := &fakeCompleter{responses: []string{"这是追问回答"}}
	service := New(fakeResolver{}, fakeProvider{}, completer, "", 1200, 4, testLogger(),
		WithMetrics(recorder), WithMaxToolRounds(0))

	if _, err := service.Chat(context.Background(), testUser(), "sk-test", "止损呢？", "context", 1); err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if len(recorder.requests) != 1 || recorder.requests[0] != (observedRequest{"chat", "ok"}) {
		t.Fatalf("requests = %#v", recorder.requests)
	}
	if recorder.inflight != 0 {
		t.Fatalf("inflight = %d, want 0", recorder.inflight)
	}
}
