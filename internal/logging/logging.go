package logging

import (
	"fmt"
	"log/slog"
	"os"
)

// New 创建 JSON 格式的结构化日志器。
func New(level string) (*slog.Logger, error) {
	var l slog.Level
	if err := l.UnmarshalText([]byte(level)); err != nil {
		return nil, fmt.Errorf("无效的 LOG_LEVEL %q: %w", level, err)
	}

	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: l,
	})
	return slog.New(handler), nil
}
