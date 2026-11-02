package llm

import (
	"context"
	"errors"
)

var (
	// ErrInvalidKey 表示上游明确拒绝该 API Key。
	ErrInvalidKey = errors.New("API Key 无效或没有权限")
	// ErrModelsUnavailable 表示 endpoint 不支持 /models，无法完成校验。
	ErrModelsUnavailable = errors.New("endpoint 不支持 /models")
	// ErrValidationUnavailable 表示暂时无法完成校验（限流、网络、服务端错误）。
	ErrValidationUnavailable = errors.New("暂时无法校验 API Key")
)

// Validator 负责校验用户 Key 和列出模型。
type Validator interface {
	ValidateKey(ctx context.Context, baseURL, apiKey string) error
	ListModels(ctx context.Context, baseURL, apiKey string) ([]string, error)
}
