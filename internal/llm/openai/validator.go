package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/guanlanai/guanlanai/internal/llm"
	"github.com/guanlanai/guanlanai/internal/netguard"
)

// Validator 通过 OpenAI 兼容 HTTP 接口校验 Key 和列出模型。
type Validator struct {
	policy       netguard.Policy
	client       *http.Client
	maxBytes     int64
	defaultModel string
	allowlist    []string
}

// NewValidator 创建校验器。
func NewValidator(policy netguard.Policy, timeout time.Duration, maxBytes int64, defaultModel string, allowlist []string) *Validator {
	if maxBytes <= 0 {
		maxBytes = 2 * 1024 * 1024
	}
	return &Validator{
		policy:       policy,
		client:       netguard.NewHTTPClient(policy, timeout),
		maxBytes:     maxBytes,
		defaultModel: defaultModel,
		allowlist:    allowlist,
	}
}

// ValidateKey 先请求 /models，失败时尝试最小 Chat Completions 请求。
func (v *Validator) ValidateKey(ctx context.Context, baseURL, apiKey string) error {
	baseURL, err := v.policy.Validate(ctx, baseURL)
	if err != nil {
		return err
	}

	status, body, err := v.request(ctx, http.MethodGet, baseURL+"/models", apiKey, nil)
	if err != nil {
		return fmt.Errorf("%w: %v", llm.ErrValidationUnavailable, err)
	}

	switch status {
	case http.StatusOK:
		return nil
	case http.StatusUnauthorized, http.StatusForbidden:
		return llm.ErrInvalidKey
	case http.StatusNotFound, http.StatusMethodNotAllowed:
		return v.validateWithChat(ctx, baseURL, apiKey)
	case http.StatusTooManyRequests:
		return fmt.Errorf("%w: 上游限流", llm.ErrValidationUnavailable)
	default:
		return fmt.Errorf("%w: HTTP %d %s", llm.ErrValidationUnavailable, status, snippet(body))
	}
}

// ListModels 返回 endpoint 支持的模型 ID。
func (v *Validator) ListModels(ctx context.Context, baseURL, apiKey string) ([]string, error) {
	baseURL, err := v.policy.Validate(ctx, baseURL)
	if err != nil {
		return nil, err
	}

	status, body, err := v.request(ctx, http.MethodGet, baseURL+"/models", apiKey, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", llm.ErrValidationUnavailable, err)
	}
	switch status {
	case http.StatusOK:
		var payload struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			return nil, fmt.Errorf("%w: 响应不是合法 JSON", llm.ErrValidationUnavailable)
		}
		models := make([]string, 0, len(payload.Data))
		for _, item := range payload.Data {
			if item.ID != "" {
				models = append(models, item.ID)
			}
		}
		sort.Strings(models)
		return models, nil
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, llm.ErrInvalidKey
	case http.StatusNotFound, http.StatusMethodNotAllowed:
		return nil, llm.ErrModelsUnavailable
	default:
		return nil, fmt.Errorf("%w: HTTP %d %s", llm.ErrValidationUnavailable, status, snippet(body))
	}
}

func (v *Validator) validateWithChat(ctx context.Context, baseURL, apiKey string) error {
	model := v.defaultModel
	if model == "" && len(v.allowlist) > 0 {
		model = v.allowlist[0]
	}
	if model == "" {
		return fmt.Errorf("%w，且没有可用于校验的默认模型", llm.ErrModelsUnavailable)
	}

	payload := map[string]any{
		"model":      model,
		"messages":   []map[string]string{{"role": "user", "content": "ping"}},
		"max_tokens": 1,
	}
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("构造校验请求: %w", err)
	}

	status, body, err := v.request(ctx, http.MethodPost, baseURL+"/chat/completions", apiKey, bodyBytes)
	if err != nil {
		return fmt.Errorf("%w: %v", llm.ErrValidationUnavailable, err)
	}
	switch status {
	case http.StatusOK:
		return nil
	case http.StatusUnauthorized, http.StatusForbidden:
		return llm.ErrInvalidKey
	case http.StatusTooManyRequests:
		return fmt.Errorf("%w: 上游限流", llm.ErrValidationUnavailable)
	default:
		return fmt.Errorf("%w: /models 不可用，Chat Completions HTTP %d %s", llm.ErrModelsUnavailable, status, snippet(body))
	}
}

func (v *Validator) request(ctx context.Context, method, endpoint, apiKey string, body []byte) (int, []byte, error) {
	var reader io.Reader
	if body != nil {
		reader = strings.NewReader(string(body))
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := v.client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()

	limited := io.LimitReader(resp.Body, v.maxBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return 0, nil, err
	}
	if int64(len(data)) > v.maxBytes {
		return 0, nil, fmt.Errorf("响应超过 %d 字节", v.maxBytes)
	}
	return resp.StatusCode, data, nil
}

func snippet(body []byte) string {
	text := strings.TrimSpace(string(body))
	if len(text) > 200 {
		text = text[:200] + "..."
	}
	return text
}
