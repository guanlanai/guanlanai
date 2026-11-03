package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/guanlanai/guanlanai/internal/llm"
)

type failingStream struct{}

func (failingStream) Recv() (string, error)             { return "", errors.New("stream broke") }
func (failingStream) Response() *llm.CompletionResponse { return &llm.CompletionResponse{} }
func (failingStream) Close() error                      { return nil }

type streamFailCompleter struct {
	completeCalls int
	streamCalls   int
}

func (f *streamFailCompleter) Complete(_ context.Context, _ llm.CompletionRequest) (*llm.CompletionResponse, error) {
	f.completeCalls++
	return &llm.CompletionResponse{Content: "fallback"}, nil
}

func (f *streamFailCompleter) Stream(_ context.Context, _ llm.CompletionRequest) (llm.Stream, error) {
	f.streamCalls++
	return failingStream{}, nil
}

func TestChatStreamFallsBackWhenStreamFailsBeforeContent(t *testing.T) {
	completer := &streamFailCompleter{}
	service := New(fakeResolver{}, fakeProvider{}, completer, "", 1200, 4, testLogger(),
		WithMaxToolRounds(0), WithStreaming(true))

	result, err := service.ChatStream(context.Background(), testUser(), "sk-test", "问题", "context", 0, func(string) error {
		return nil
	})
	if err != nil {
		t.Fatalf("ChatStream() error = %v", err)
	}
	if result.Content != "fallback" {
		t.Fatalf("content = %q", result.Content)
	}
	if completer.streamCalls != 1 || completer.completeCalls != 1 {
		t.Fatalf("stream/completer calls = %d/%d", completer.streamCalls, completer.completeCalls)
	}
}

func TestChatStreamDisabledUsesComplete(t *testing.T) {
	completer := &streamFailCompleter{}
	service := New(fakeResolver{}, fakeProvider{}, completer, "", 1200, 4, testLogger(),
		WithMaxToolRounds(0), WithStreaming(false))

	if _, err := service.ChatStream(context.Background(), testUser(), "sk-test", "问题", "context", 0, func(string) error {
		return nil
	}); err != nil {
		t.Fatalf("ChatStream() error = %v", err)
	}
	if completer.streamCalls != 0 || completer.completeCalls != 1 {
		t.Fatalf("stream/completer calls = %d/%d", completer.streamCalls, completer.completeCalls)
	}
}
