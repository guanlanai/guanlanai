package logging

import (
	"context"
	"log/slog"
	"regexp"
)

var redactPatterns = []*regexp.Regexp{
	// OpenAI 风格的密钥。
	regexp.MustCompile(`(?i)\bsk-[a-z0-9_-]{8,}\b`),
	// Authorization: Bearer ...
	regexp.MustCompile(`(?i)\bbearer\s+[a-z0-9._~+/=-]{8,}`),
	// api_key=... / access_token: ...
	regexp.MustCompile(`(?i)\b(api[_-]?key|access[_-]?token|secret[_-]?key)\b\s*[:=]\s*["']?[^\s"',}]+`),
}

// Redact 移除日志文本中的常见密钥形式。
// 这是兜底措施，不替代“不要把密钥写进日志”的约定。
func Redact(text string) string {
	for _, pattern := range redactPatterns {
		text = pattern.ReplaceAllString(text, "[REDACTED]")
	}
	return text
}

// redactingHandler 在日志落盘前统一脱敏消息和字段。
type redactingHandler struct {
	next slog.Handler
}

func (h *redactingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *redactingHandler) Handle(ctx context.Context, record slog.Record) error {
	clone := slog.NewRecord(record.Time, record.Level, Redact(record.Message), record.PC)
	record.Attrs(func(attr slog.Attr) bool {
		clone.AddAttrs(redactAttr(attr))
		return true
	})
	return h.next.Handle(ctx, clone)
}

func (h *redactingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	redacted := make([]slog.Attr, len(attrs))
	for index, attr := range attrs {
		redacted[index] = redactAttr(attr)
	}
	return &redactingHandler{next: h.next.WithAttrs(redacted)}
}

func (h *redactingHandler) WithGroup(name string) slog.Handler {
	return &redactingHandler{next: h.next.WithGroup(name)}
}

func redactAttr(attr slog.Attr) slog.Attr {
	switch attr.Value.Kind() {
	case slog.KindString:
		attr.Value = slog.StringValue(Redact(attr.Value.String()))
	case slog.KindGroup:
		group := attr.Value.Group()
		redacted := make([]slog.Attr, len(group))
		for index, item := range group {
			redacted[index] = redactAttr(item)
		}
		attr.Value = slog.GroupValue(redacted...)
	case slog.KindAny:
		if err, ok := attr.Value.Any().(error); ok {
			attr.Value = slog.StringValue(Redact(err.Error()))
		}
	}
	return attr
}
