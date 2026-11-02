package openai

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/guanlanai/guanlanai/internal/llm"
	"github.com/guanlanai/guanlanai/internal/netguard"
)

func testPolicy() netguard.Policy {
	return netguard.Policy{AllowHTTP: true, AllowPrivate: true}
}

func testValidator(t *testing.T, handler http.Handler) (*Validator, string) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	validator := NewValidator(testPolicy(), 2*time.Second, 1024*1024, "default-model", nil)
	return validator, server.URL + "/v1"
}

func TestValidateKeySuccess(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sk-test" {
			t.Fatalf("Authorization = %q", r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"m2"},{"id":"m1"}]}`))
	})
	validator, baseURL := testValidator(t, mux)

	if err := validator.ValidateKey(context.Background(), baseURL, "sk-test"); err != nil {
		t.Fatalf("ValidateKey() error = %v", err)
	}
}

func TestValidateKeyInvalid(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	validator, baseURL := testValidator(t, mux)

	if err := validator.ValidateKey(context.Background(), baseURL, "sk-test"); !errors.Is(err, llm.ErrInvalidKey) {
		t.Fatalf("ValidateKey() error = %v, want ErrInvalidKey", err)
	}
}

func TestValidateKeyFallsBackToChat(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"pong"}}]}`))
	})
	validator, baseURL := testValidator(t, mux)

	if err := validator.ValidateKey(context.Background(), baseURL, "sk-test"); err != nil {
		t.Fatalf("ValidateKey() error = %v", err)
	}
}

func TestValidateKeyModelsUnavailable(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	})
	validator := NewValidator(testPolicy(), 2*time.Second, 1024*1024, "", nil)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	err := validator.ValidateKey(context.Background(), server.URL+"/v1", "sk-test")
	if !errors.Is(err, llm.ErrModelsUnavailable) {
		t.Fatalf("ValidateKey() error = %v, want ErrModelsUnavailable", err)
	}
}

func TestListModels(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"m2"},{"id":"m1"}]}`))
	})
	validator, baseURL := testValidator(t, mux)

	models, err := validator.ListModels(context.Background(), baseURL, "sk-test")
	if err != nil {
		t.Fatalf("ListModels() error = %v", err)
	}
	if len(models) != 2 || models[0] != "m1" || models[1] != "m2" {
		t.Fatalf("ListModels() = %#v", models)
	}
}

func TestListModelsInvalidKey(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	})
	validator, baseURL := testValidator(t, mux)

	if _, err := validator.ListModels(context.Background(), baseURL, "sk-test"); !errors.Is(err, llm.ErrInvalidKey) {
		t.Fatalf("ListModels() error = %v, want ErrInvalidKey", err)
	}
}

func TestListModelsBadJSON(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`not-json`))
	})
	validator, baseURL := testValidator(t, mux)

	if _, err := validator.ListModels(context.Background(), baseURL, "sk-test"); !errors.Is(err, llm.ErrValidationUnavailable) {
		t.Fatalf("ListModels() error = %v, want ErrValidationUnavailable", err)
	}
}
