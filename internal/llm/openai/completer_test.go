package openai

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/guanlanai/guanlanai/internal/llm"
	"github.com/guanlanai/guanlanai/internal/netguard"
)

func TestComplete(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("Authorization") != "Bearer sk-test" {
			t.Fatalf("Authorization = %q", r.Header.Get("Authorization"))
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("解析请求失败: %v", err)
		}
		format, _ := payload["response_format"].(map[string]any)
		if format["type"] != "json_object" {
			t.Fatalf("response_format = %#v", payload["response_format"])
		}
		_, _ = w.Write([]byte(`{
			"id": "chatcmpl-test",
			"object": "chat.completion",
			"created": 1,
			"model": "gpt-test",
			"choices": [{"index": 0, "finish_reason": "stop", "message": {"role": "assistant", "content": "{\"ok\":true}"}}],
			"usage": {"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15}
		}`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	completer := NewCompleter(netguard.Policy{AllowHTTP: true, AllowPrivate: true}, 5*time.Second)
	response, err := completer.Complete(context.Background(), llm.CompletionRequest{
		BaseURL:         server.URL + "/v1",
		APIKey:          "sk-test",
		Model:           "gpt-test",
		System:          "system",
		User:            "user",
		MaxOutputTokens: 100,
		Temperature:     0.2,
		JSONMode:        true,
	})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if response.Content != `{"ok":true}` {
		t.Fatalf("Content = %q", response.Content)
	}
	if response.PromptTokens != 10 || response.CompletionTokens != 5 {
		t.Fatalf("usage = %#v", response)
	}
}

func TestCompleteOmitsTemperatureForReasoningModel(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("解析请求失败: %v", err)
		}
		if _, exists := payload["temperature"]; exists {
			t.Fatalf("gpt-5-mini 请求不应包含 temperature: %#v", payload)
		}
		_, _ = w.Write([]byte(`{
			"id": "chatcmpl-test",
			"object": "chat.completion",
			"created": 1,
			"model": "gpt-5-mini",
			"choices": [{"index": 0, "finish_reason": "stop", "message": {"role": "assistant", "content": "{}"}}],
			"usage": {"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2}
		}`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	completer := NewCompleter(netguard.Policy{AllowHTTP: true, AllowPrivate: true}, 5*time.Second)
	_, err := completer.Complete(context.Background(), llm.CompletionRequest{
		BaseURL:         server.URL + "/v1",
		APIKey:          "sk-test",
		Model:           "gpt-5-mini",
		System:          "system",
		User:            "user",
		MaxOutputTokens: 100,
		Temperature:     0.2,
		JSONMode:        true,
	})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
}

func TestCompleteMapsInvalidKey(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid key","type":"invalid_request_error"}}`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	completer := NewCompleter(netguard.Policy{AllowHTTP: true, AllowPrivate: true}, 5*time.Second)
	_, err := completer.Complete(context.Background(), llm.CompletionRequest{
		BaseURL: server.URL + "/v1",
		APIKey:  "sk-bad",
		Model:   "gpt-test",
		System:  "system",
		User:    "user",
	})
	if !errors.Is(err, llm.ErrInvalidKey) {
		t.Fatalf("Complete() error = %v, want ErrInvalidKey", err)
	}
}
