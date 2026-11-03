package logging

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

func TestRedactRemovesCommonSecrets(t *testing.T) {
	cases := []string{
		`upstream error: sk-abcdefgh12345678`,
		`Authorization: Bearer abcdefgh12345678`,
		`api_key=sk-live-abcdefgh`,
		`access_token: "abcdefgh12345678"`,
	}
	for _, input := range cases {
		got := Redact(input)
		if strings.Contains(got, "abcdefgh") || strings.Contains(got, "sk-live") {
			t.Fatalf("Redact(%q) = %q", input, got)
		}
		if !strings.Contains(got, "[REDACTED]") {
			t.Fatalf("Redact(%q) 缺少标记: %q", input, got)
		}
	}
}

func TestRedactKeepsNormalFields(t *testing.T) {
	input := "prompt_tokens=10 completion_tokens=5"
	if got := Redact(input); got != input {
		t.Fatalf("正常字段被误伤: %q", got)
	}
}

func TestRedactingHandler(t *testing.T) {
	var buffer bytes.Buffer
	logger := slog.New(&redactingHandler{next: slog.NewJSONHandler(&buffer, nil)})
	logger.Error("调用失败",
		"error", errors.New("upstream echoed sk-abcdefgh12345678"),
		"prompt_tokens", 10,
	)

	output := buffer.String()
	if strings.Contains(output, "sk-abcdefgh") {
		t.Fatalf("日志未脱敏: %s", output)
	}
	if !strings.Contains(output, "[REDACTED]") {
		t.Fatalf("日志缺少脱敏标记: %s", output)
	}
	if !strings.Contains(output, "prompt_tokens") {
		t.Fatalf("正常字段丢失: %s", output)
	}
}

func TestRedactingHandlerWithAttrs(t *testing.T) {
	var buffer bytes.Buffer
	logger := slog.New(&redactingHandler{next: slog.NewJSONHandler(&buffer, nil)}).
		With("api_key", "sk-abcdefgh12345678")
	logger.Info("ok")

	if strings.Contains(buffer.String(), "sk-abcdefgh") {
		t.Fatalf("WithAttrs 未脱敏: %s", buffer.String())
	}
}
