package render

import (
	"context"
	"html"
	"strings"
	"unicode/utf8"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

// MaxMessageRunes 留出余量，避免触达 Telegram 4096 的上限。
const MaxMessageRunes = 3900

// Sender 是发送消息所需的最小接口，方便测试时替换。
type Sender interface {
	SendMessage(ctx context.Context, params *bot.SendMessageParams) (*models.Message, error)
}

// EscapeHTML 转义用户输入和外部文本。
func EscapeHTML(s string) string {
	return html.EscapeString(s)
}

// SendHTML 以 HTML 模式发送消息，超长时拆成多条。
// 当前消息都较短；后续加入长观点时需要升级为标签感知拆分。
func SendHTML(ctx context.Context, sender Sender, chatID int64, text string) error {
	for _, chunk := range Split(text, MaxMessageRunes) {
		if _, err := sender.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:    chatID,
			Text:      chunk,
			ParseMode: models.ParseModeHTML,
		}); err != nil {
			return err
		}
	}
	return nil
}

// Split 按 rune 数量拆分长文本，尽量保留原文内容。
func Split(text string, limit int) []string {
	if limit <= 0 {
		limit = MaxMessageRunes
	}
	if text == "" {
		return nil
	}
	if utf8.RuneCountInString(text) <= limit {
		return []string{text}
	}

	var chunks []string
	var current strings.Builder
	count := 0

	flush := func() {
		if current.Len() == 0 {
			return
		}
		chunks = append(chunks, current.String())
		current.Reset()
		count = 0
	}

	for _, r := range text {
		if count >= limit {
			flush()
		}
		current.WriteRune(r)
		count++
	}
	flush()

	return chunks
}
