package logging

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

type traceKey struct{}

// NewTraceID 生成一个短 trace ID，用于串联一次请求内的行情、LLM 和摘要调用。
func NewTraceID() string {
	var buffer [8]byte
	if _, err := rand.Read(buffer[:]); err == nil {
		return hex.EncodeToString(buffer[:])
	}
	return fmt.Sprintf("%x", time.Now().UnixNano())
}

// EnsureTrace 返回带有 trace ID 的 context；已有 trace 时保持不变。
func EnsureTrace(ctx context.Context) (context.Context, string) {
	if id := TraceID(ctx); id != "" {
		return ctx, id
	}
	id := NewTraceID()
	return context.WithValue(ctx, traceKey{}, id), id
}

// TraceID 从 context 读取 trace ID；不存在时返回空字符串。
func TraceID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	id, _ := ctx.Value(traceKey{}).(string)
	return id
}
